package clientbuilder

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/toolchain"
)

type BuiltAssetKind string

const (
	BuiltAssetEntry     BuiltAssetKind = "entry"
	BuiltAssetChunk     BuiltAssetKind = "chunk"
	BuiltAssetVendor    BuiltAssetKind = "vendor"
	BuiltAssetCSS       BuiltAssetKind = "css"
	BuiltAssetAsset     BuiltAssetKind = "asset"
	BuiltAssetSourceMap BuiltAssetKind = "sourcemap"
)

type BuiltClientFile struct {
	Kind string
	URL  string
	File string
}

type ClientDependencyGraph struct {
	Entry          string                      `json:"entry"`
	EntryFile      string                      `json:"entryFile"`
	Framework      string                      `json:"framework"`
	Component      string                      `json:"component"`
	OutputFiles    []string                    `json:"outputFiles"`
	ModuleFiles    []string                    `json:"moduleFiles"`
	Imports        []string                    `json:"imports"`
	DynamicImports []string                    `json:"dynamicImports"`
	Nodes          []ClientDependencyGraphNode `json:"nodes"`
}

type ClientDependencyGraphNode struct {
	File           string   `json:"file"`
	Imports        []string `json:"imports"`
	DynamicImports []string `json:"dynamicImports"`
}

type BuiltAsset struct {
	Asset      Asset
	ConfigPath string

	EntryURL  string
	EntryFile string

	Files []BuiltClientFile
	Graph ClientDependencyGraph
}

func InstalledViteAssets(assets []Asset) []Asset {
	out := make([]Asset, 0, len(assets))
	for _, asset := range assets {
		if IsViteWorkspaceInstalled(asset) {
			out = append(out, asset)
		}
	}
	return out
}

func IsViteWorkspaceInstalled(asset Asset) bool {
	root := strings.TrimSpace(asset.FrameworkRoot)
	if root == "" {
		root = asset.EffectiveRoot()
	}
	if root == "" {
		return false
	}
	if !fileExists(filepath.Join(root, "package.json")) {
		return false
	}
	if fileExists(filepath.Join(root, "node_modules", ".bin", "vite")) {
		return true
	}
	// Windows is not currently supported by GoX plugins, but keep the check harmless.
	if fileExists(filepath.Join(root, "node_modules", ".bin", "vite.cmd")) {
		return true
	}
	return false
}

func BuildViteAssets(node toolchain.NodePaths, pnpm toolchain.PNPMPaths, assets []Asset) ([]BuiltAsset, error) {
	built := make([]BuiltAsset, 0, len(assets))
	for _, group := range groupViteAssets(assets) {
		// Production component bundles import the stable framework vendor facades.
		// Guarantee their integrity here so every BuildViteAssets caller gets the
		// same recovery behavior, including cold builds and compiler rebuilds.
		if strings.EqualFold(strings.TrimSpace(group.Mode), ModeProd) {
			if err := BuildFrameworkVendors(node, pnpm, group.ProjectRoot, []string{group.Framework}); err != nil {
				return built, err
			}
		}

		items, err := buildViteAssetGroup(node, pnpm, group)
		if err != nil {
			return built, err
		}
		built = append(built, items...)
	}
	return built, nil
}

func BuildViteAsset(node toolchain.NodePaths, pnpm toolchain.PNPMPaths, asset Asset) (BuiltAsset, error) {
	if cached, ok := TryLoadCachedViteAsset(asset); ok {
		if err := postProcessGoxCssModuleFiles(asset, asset.OutputAbsDir); err != nil {
			return BuiltAsset{}, err
		}
		logs.Info("client bundle reused: %s", cached.EntryFile)
		return cached, nil
	}

	configPath := asset.ViteConfigPath()
	if strings.TrimSpace(configPath) == "" {
		return BuiltAsset{}, fmt.Errorf("missing vite config path for client %q", asset.Name)
	}
	if !fileExists(configPath) {
		return BuiltAsset{}, fmt.Errorf("missing vite config for client %q: %s", asset.Name, configPath)
	}

	root := asset.EffectiveRoot()
	if strings.TrimSpace(root) == "" {
		return BuiltAsset{}, fmt.Errorf("missing framework root for client %q", asset.Name)
	}

	logs.Info("building client component: %s %s", asset.Framework, asset.Path)
	cmd := toolchain.PNPMCommand(node, pnpm, root, "exec", "vite", "build", "--config", configPath, "--clearScreen=false")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		if text := strings.TrimSpace(stripClientGraphBlocks(out.String())); text != "" {
			logs.Error("vite build output:\n%s", text)
		}
		return BuiltAsset{}, fmt.Errorf("vite build failed for client %s/%s: %w", asset.Framework, asset.Path, err)
	}

	graph, err := ExtractClientDependencyGraphFromOutput(asset, out.String())
	if err != nil {
		return BuiltAsset{}, err
	}
	if err := postProcessGoxCssModuleFiles(asset, asset.OutputAbsDir); err != nil {
		return BuiltAsset{}, err
	}

	entryPath := filepath.Join(asset.OutputAbsDir, asset.PublicBaseName()+".js")
	if !fileExists(entryPath) {
		if text := strings.TrimSpace(out.String()); text != "" {
			logs.Error("vite build output:\n%s", text)
		}
		return BuiltAsset{}, fmt.Errorf("vite build did not produce entry bundle: %s", entryPath)
	}

	files, err := ScanBuiltClientFiles(asset, entryPath)
	if err != nil {
		return BuiltAsset{}, err
	}
	entryURL, files, err := FingerprintBuiltClientEntry(asset, entryPath, files)
	if err != nil {
		return BuiltAsset{}, err
	}

	logs.Info("client bundle ready: %s", entryPath)
	built := BuiltAsset{Asset: asset, ConfigPath: configPath, EntryURL: entryURL, EntryFile: entryPath, Files: files, Graph: graph}
	StoreCachedViteAsset(built)
	return built, nil
}

const (
	clientGraphBeginMarker = "__GOX_CLIENT_GRAPH_BEGIN__"
	clientGraphEndMarker   = "__GOX_CLIENT_GRAPH_END__"
)

func ExtractClientDependencyGraphFromOutput(asset Asset, output string) (ClientDependencyGraph, error) {
	graph := defaultClientDependencyGraph(asset)
	start := strings.LastIndex(output, clientGraphBeginMarker)
	end := strings.LastIndex(output, clientGraphEndMarker)
	if start < 0 || end < 0 || end <= start {
		return graph, nil
	}
	start += len(clientGraphBeginMarker)
	raw := strings.TrimSpace(output[start:end])
	if raw == "" {
		return graph, nil
	}
	if err := json.Unmarshal([]byte(raw), &graph); err != nil {
		return graph, fmt.Errorf("invalid vite dependency graph for client %q: %w", asset.Name, err)
	}
	return normalizeClientDependencyGraph(asset, graph), nil
}

func defaultClientDependencyGraph(asset Asset) ClientDependencyGraph {
	return ClientDependencyGraph{
		Entry:     asset.Path,
		EntryFile: filepath.ToSlash(asset.AbsPath),
		Framework: asset.Framework,
		Component: asset.Name,
	}
}

func normalizeClientDependencyGraph(asset Asset, graph ClientDependencyGraph) ClientDependencyGraph {
	graph.Entry = strings.TrimSpace(graph.Entry)
	if graph.Entry == "" {
		graph.Entry = asset.Path
	}
	graph.EntryFile = strings.TrimSpace(graph.EntryFile)
	if graph.EntryFile == "" {
		graph.EntryFile = filepath.ToSlash(asset.AbsPath)
	}
	graph.Framework = strings.TrimSpace(graph.Framework)
	if graph.Framework == "" {
		graph.Framework = asset.Framework
	}
	graph.Component = strings.TrimSpace(graph.Component)
	if graph.Component == "" {
		graph.Component = asset.Name
	}
	graph.OutputFiles = normalizeGraphOutputFiles(graph.OutputFiles)
	graph.ModuleFiles = normalizeGraphFiles(graph.ModuleFiles)
	graph.Imports = normalizeGraphFiles(graph.Imports)
	graph.DynamicImports = normalizeGraphFiles(graph.DynamicImports)
	graph.Nodes = normalizeGraphNodes(graph.Nodes)
	return graph
}

func normalizeGraphNodes(nodes []ClientDependencyGraphNode) []ClientDependencyGraphNode {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]ClientDependencyGraphNode, 0, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		file := normalizeGraphFileID(node.File)
		if file == "" || strings.HasPrefix(file, "\x00") {
			continue
		}
		if abs, err := filepath.Abs(file); err == nil {
			file = abs
		}
		file = filepath.ToSlash(file)
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		out = append(out, ClientDependencyGraphNode{File: file, Imports: normalizeGraphFiles(node.Imports), DynamicImports: normalizeGraphFiles(node.DynamicImports)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

func stripClientGraphBlocks(output string) string {
	for {
		start := strings.Index(output, clientGraphBeginMarker)
		if start < 0 {
			return output
		}
		end := strings.Index(output[start:], clientGraphEndMarker)
		if end < 0 {
			return strings.TrimSpace(output[:start])
		}
		end = start + end + len(clientGraphEndMarker)
		output = output[:start] + output[end:]
	}
}

func normalizeGraphFileID(file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		return ""
	}
	if i := strings.IndexAny(file, "?#"); i >= 0 {
		file = file[:i]
	}
	file = strings.TrimSpace(file)
	return file
}

func normalizeGraphOutputFiles(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, file := range files {
		file = normalizeGraphFileID(file)
		if file == "" || strings.HasPrefix(file, "\x00") {
			continue
		}
		file = filepath.ToSlash(filepath.Clean(filepath.FromSlash(file)))
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		out = append(out, file)
	}
	sort.Strings(out)
	return out
}

func normalizeGraphFiles(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, file := range files {
		file = normalizeGraphFileID(file)
		if file == "" || strings.HasPrefix(file, "\x00") {
			continue
		}
		if abs, err := filepath.Abs(file); err == nil {
			file = abs
		}
		file = filepath.ToSlash(file)
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		out = append(out, file)
	}
	sort.Strings(out)
	return out
}

func FingerprintBuiltClientEntry(asset Asset, entryPath string, files []BuiltClientFile) (string, []BuiltClientFile, error) {
	entryAbs, err := filepath.Abs(entryPath)
	if err != nil {
		return "", files, err
	}
	content, err := os.ReadFile(entryAbs)
	if err != nil {
		return "", files, err
	}
	sum := sha1.Sum(content)
	fingerprint := hex.EncodeToString(sum[:])[:8]
	base := sourcePublicName(asset.Path)
	if base == "" {
		base = sourcePublicName(asset.Entry)
	}
	base = sanitizePublicName(base)
	if base == "" {
		base = sanitizePublicName(asset.Name)
	}
	if base == "" {
		base = "client"
	}
	entryURL := PublicClientPrefix + "/" + base + "-" + fingerprint + ".js"
	mapURL := entryURL + ".map"
	stableEntryURL := asset.PublicEntryURL()
	stableMapURL := asset.PublicMapURL()

	mapPath := entryAbs + ".map"
	if fileExists(mapPath) {
		oldComment := []byte("//# sourceMappingURL=" + filepath.Base(entryAbs) + ".map")
		newComment := []byte("//# sourceMappingURL=" + filepath.Base(strings.TrimPrefix(mapURL, PublicClientPrefix+"/")))
		if bytes.Contains(content, oldComment) {
			content = bytes.ReplaceAll(content, oldComment, newComment)
			if err := os.WriteFile(entryAbs, content, 0644); err != nil {
				return "", files, err
			}
		}
	}

	out := make([]BuiltClientFile, len(files))
	copy(out, files)
	entryClean := filepath.Clean(entryAbs)
	mapClean := filepath.Clean(mapPath)
	seenEntry := false
	seenMap := false
	for i := range out {
		fileClean := filepath.Clean(out[i].File)
		url := strings.TrimSpace(out[i].URL)
		if fileClean == entryClean || url == stableEntryURL || strings.EqualFold(out[i].Kind, string(BuiltAssetEntry)) && fileClean == entryClean {
			out[i].Kind = string(BuiltAssetEntry)
			out[i].URL = entryURL
			out[i].File = entryAbs
			seenEntry = true
			continue
		}
		if fileClean == mapClean || url == stableMapURL || strings.EqualFold(out[i].Kind, string(BuiltAssetSourceMap)) && fileClean == mapClean {
			out[i].Kind = string(BuiltAssetSourceMap)
			out[i].URL = mapURL
			out[i].File = mapPath
			seenMap = true
		}
	}
	if !seenEntry {
		out = append(out, BuiltClientFile{Kind: string(BuiltAssetEntry), URL: entryURL, File: entryAbs})
	}
	if fileExists(mapPath) && !seenMap {
		out = append(out, BuiltClientFile{Kind: string(BuiltAssetSourceMap), URL: mapURL, File: mapPath})
	}
	return entryURL, out, nil
}

func ScanBuiltClientFiles(asset Asset, entryPath string) ([]BuiltClientFile, error) {
	root := strings.TrimSpace(asset.OutputAbsDir)
	if root == "" {
		return nil, fmt.Errorf("missing output dir for client %q", asset.Name)
	}

	entryAbs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	files := make([]BuiltClientFile, 0)
	err = filepath.WalkDir(rootAbs, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Base(path) == "vite.config.mjs" {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		url := PublicClientPrefix + "/" + relSlash
		kind := builtFileKind(path, entryAbs)
		if kind == string(BuiltAssetEntry) {
			url = asset.PublicEntryURL()
		} else if kind == string(BuiltAssetSourceMap) && filepath.Base(path) == filepath.Base(entryAbs)+".map" {
			url = asset.PublicMapURL()
		}
		files = append(files, BuiltClientFile{Kind: kind, URL: url, File: path})
		return nil
	})
	if err != nil {
		return nil, err
	}
	files = ensureBuiltEntryFiles(asset, files, entryAbs)

	sort.Slice(files, func(i, j int) bool {
		if files[i].Kind == files[j].Kind {
			return files[i].URL < files[j].URL
		}
		return assetKindRank(files[i].Kind) < assetKindRank(files[j].Kind)
	})
	return files, nil
}

func ensureBuiltEntryFiles(asset Asset, files []BuiltClientFile, entryAbs string) []BuiltClientFile {
	entryAbs = filepath.Clean(entryAbs)
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		seen[filepath.Clean(file.File)] = struct{}{}
	}
	if _, ok := seen[entryAbs]; !ok && fileExists(entryAbs) {
		files = append(files, BuiltClientFile{Kind: string(BuiltAssetEntry), URL: asset.PublicEntryURL(), File: entryAbs})
	}
	mapPath := entryAbs + ".map"
	if _, ok := seen[mapPath]; !ok && fileExists(mapPath) {
		files = append(files, BuiltClientFile{Kind: string(BuiltAssetSourceMap), URL: asset.PublicMapURL(), File: mapPath})
	}
	return files
}

func builtFileKind(path string, entryAbs string) string {
	abs, err := filepath.Abs(path)
	if err == nil && abs == entryAbs {
		return string(BuiltAssetEntry)
	}
	if strings.HasSuffix(path, ".map") {
		return string(BuiltAssetSourceMap)
	}
	if filepath.Ext(path) == ".js" || filepath.Ext(path) == ".mjs" {
		rel := filepath.ToSlash(path)
		if strings.Contains(rel, "/vendor/") || strings.HasPrefix(rel, "vendor/") {
			return string(BuiltAssetVendor)
		}
		return string(BuiltAssetChunk)
	}
	if filepath.Ext(path) == ".css" || mime.TypeByExtension(filepath.Ext(path)) == "text/css" {
		return string(BuiltAssetCSS)
	}
	return string(BuiltAssetAsset)
}

func assetKindRank(kind string) int {
	switch kind {
	case string(BuiltAssetEntry):
		return 0
	case string(BuiltAssetChunk):
		return 1
	case string(BuiltAssetVendor):
		return 2
	case string(BuiltAssetCSS):
		return 3
	case string(BuiltAssetAsset):
		return 4
	case string(BuiltAssetSourceMap):
		return 5
	default:
		return 9
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

type viteAssetGroup struct {
	Key           string
	Framework     string
	Mode          string
	ProjectRoot   string
	FrameworkRoot string
	OutputAbsDir  string
	Assets        []Asset
}

func groupViteAssets(assets []Asset) []viteAssetGroup {
	groups := map[string]*viteAssetGroup{}
	keys := make([]string, 0)
	for _, asset := range assets {
		framework := strings.ToLower(strings.TrimSpace(asset.Framework))
		mode := strings.ToLower(strings.TrimSpace(asset.Mode))
		if mode == "" {
			if asset.Dev {
				mode = ModeDev
			} else {
				mode = ModeProd
			}
		}
		projectRoot := cleanCacheRoot(asset.ProjectRoot)
		frameworkRoot := cleanCachePath(asset.FrameworkRoot)
		asset.Mode = mode
		asset.OutputAbsDir = filepath.Join(projectRoot, ".build", "client", framework, mode)
		asset.OutputDir = filepath.ToSlash(filepath.Join(".build", "client", framework, mode))
		componentKey := cleanCachePath(asset.AbsPath)
		if componentKey == "" {
			componentKey = filepath.ToSlash(filepath.Clean(asset.Path))
		}
		key := projectRoot + "\x00" + frameworkRoot + "\x00" + framework + "\x00" + mode + "\x00" + componentKey + "\x00" + strings.TrimSpace(asset.Name)
		group := groups[key]
		if group == nil {
			group = &viteAssetGroup{Key: key, Framework: framework, Mode: mode, ProjectRoot: projectRoot, FrameworkRoot: frameworkRoot, OutputAbsDir: asset.OutputAbsDir}
			groups[key] = group
			keys = append(keys, key)
		}
		group.Assets = append(group.Assets, asset)
	}
	sort.Strings(keys)
	out := make([]viteAssetGroup, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		sort.Slice(group.Assets, func(i, j int) bool { return group.Assets[i].PublicBaseName() < group.Assets[j].PublicBaseName() })
		out = append(out, *group)
	}
	return out
}

func buildViteFrameworkVendors(node toolchain.NodePaths, pnpm toolchain.PNPMPaths, group viteAssetGroup) error {
	if len(group.Assets) == 0 || strings.EqualFold(strings.TrimSpace(group.Mode), ModeDev) {
		return nil
	}
	first := group.Assets[0]
	facades := frameworkModuleFacades(first.Framework)
	if len(facades) == 0 {
		return nil
	}
	frameworkVersion, err := FrameworkVersion(group.ProjectRoot, first.Framework)
	if err != nil {
		return err
	}
	if frameworkVendorOutputsExist(group.OutputAbsDir, facades, frameworkVersion) {
		return nil
	}
	if err := writeFrameworkModuleFacades(first); err != nil {
		return err
	}
	root := first.EffectiveRoot()
	configPath := filepath.Join(group.OutputAbsDir, "vite", "vendor", "vite.config.mjs")
	content, err := GenerateViteFrameworkVendorConfig(first, group.OutputAbsDir, facades, frameworkVersion)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}
	if current, err := os.ReadFile(configPath); err != nil || !bytes.Equal(current, content) {
		if err := os.WriteFile(configPath, content, 0644); err != nil {
			return err
		}
	}
	logs.Info("building client framework vendors: %s %s entries=%d", group.Framework, group.Mode, len(facades))
	cmd := toolchain.PNPMCommand(node, pnpm, root, "exec", "vite", "build", "--config", configPath, "--clearScreen=false")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if text := strings.TrimSpace(stripClientGraphBlocks(out.String())); text != "" {
			logs.Error("vite vendor build output:\n%s", text)
		}
		return fmt.Errorf("vite framework vendor build failed for %s/%s: %w", group.Framework, group.Mode, err)
	}
	return nil
}

// FrameworkVendorOutputsExist reports whether every stable production vendor
// facade for framework already exists in the project client output directory.
func FrameworkVendorOutputsExist(projectRoot string, framework string) bool {
	framework = strings.ToLower(strings.TrimSpace(framework))
	if framework == "" {
		return false
	}
	facades := frameworkModuleFacades(framework)
	if len(facades) == 0 {
		return true
	}
	outputAbsDir := filepath.Join(cleanCacheRoot(projectRoot), ".build", "client", framework, ModeProd)
	version, err := FrameworkVersion(projectRoot, framework)
	if err != nil {
		return false
	}
	return frameworkVendorOutputsExist(outputAbsDir, facades, version)
}

// BuildFrameworkVendors builds only the stable production vendor files for the
// requested frameworks. Existing complete vendor sets are reused. Builds for
// the same project/framework are serialized so startup, compiler builds, and
// hot reload cannot write the same vendor output concurrently.
func BuildFrameworkVendors(node toolchain.NodePaths, pnpm toolchain.PNPMPaths, projectRoot string, frameworks []string) error {
	root := cleanCacheRoot(projectRoot)
	seen := make(map[string]struct{}, len(frameworks))
	for _, value := range frameworks {
		framework := strings.ToLower(strings.TrimSpace(value))
		if framework == "" {
			continue
		}
		if _, ok := seen[framework]; ok {
			continue
		}
		seen[framework] = struct{}{}
		if err := ensureFrameworkVendor(root, framework, func(group viteAssetGroup) error {
			return buildViteFrameworkVendors(node, pnpm, group)
		}); err != nil {
			return err
		}
	}
	return nil
}

var frameworkVendorBuildLocks sync.Map // map[string]*sync.Mutex

func frameworkVendorBuildLock(projectRoot, framework string) *sync.Mutex {
	key := filepath.Clean(projectRoot) + "\x00" + strings.ToLower(strings.TrimSpace(framework))
	value, _ := frameworkVendorBuildLocks.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func ensureFrameworkVendor(root, framework string, build func(viteAssetGroup) error) error {
	framework = strings.ToLower(strings.TrimSpace(framework))
	if framework == "" {
		return nil
	}

	lock := frameworkVendorBuildLock(root, framework)
	lock.Lock()
	defer lock.Unlock()

	// Re-check after acquiring the per-project/framework lock. Another caller
	// may have completed the vendor while this caller was waiting.
	if FrameworkVendorOutputsExist(root, framework) {
		logs.Info("client framework vendors ready: %s prod (existing)", framework)
		return nil
	}

	frameworkRoot := filepath.Join(root, "client", framework)
	asset := Asset{
		Name:          "vendor-" + framework,
		Framework:     framework,
		Mode:          ModeProd,
		Dev:           false,
		ProjectRoot:   root,
		FrameworkRoot: frameworkRoot,
		OutputAbsDir:  filepath.Join(root, ".build", "client", framework, ModeProd),
	}
	group := viteAssetGroup{
		Framework:     framework,
		Mode:          ModeProd,
		ProjectRoot:   root,
		FrameworkRoot: frameworkRoot,
		OutputAbsDir:  asset.OutputAbsDir,
		Assets:        []Asset{asset},
	}
	if err := build(group); err != nil {
		return err
	}
	logs.Info("client framework vendors ready: %s prod", framework)
	return nil
}

const frameworkVendorManifestName = ".gox-vendor-manifest.json"

type frameworkVendorManifest struct {
	Version int      `json:"version"`
	Files   []string `json:"files"`
}

func frameworkVendorOutputsExist(outputAbsDir string, facades []frameworkModuleFacade, frameworkVersion string) bool {
	if strings.TrimSpace(outputAbsDir) == "" || len(facades) == 0 {
		return false
	}
	for _, facade := range facades {
		if !fileExists(filepath.Join(outputAbsDir, filepath.FromSlash(facade.Name)+"."+frameworkVersion+".js")) {
			return false
		}
	}

	data, err := os.ReadFile(filepath.Join(outputAbsDir, frameworkVendorManifestName))
	if err != nil {
		return false
	}
	var manifest frameworkVendorManifest
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version != 1 || len(manifest.Files) == 0 {
		return false
	}

	outputRoot, err := filepath.Abs(outputAbsDir)
	if err != nil {
		return false
	}
	for _, name := range manifest.Files {
		name = strings.TrimSpace(name)
		if name == "" || filepath.IsAbs(name) {
			return false
		}
		path, err := filepath.Abs(filepath.Join(outputRoot, filepath.FromSlash(name)))
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(outputRoot, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
		if !fileExists(path) {
			return false
		}
	}
	return true
}

func buildViteAssetGroup(node toolchain.NodePaths, pnpm toolchain.PNPMPaths, group viteAssetGroup) ([]BuiltAsset, error) {
	if len(group.Assets) == 0 {
		return nil, nil
	}
	wrappedAssets, err := WriteFrameworkWrappers(group.Assets)
	if err != nil {
		return nil, err
	}
	group.Assets = wrappedAssets
	componentDir := filepath.Join(group.OutputAbsDir, group.Assets[0].PublicComponentDirName())
	if err := os.RemoveAll(componentDir); err != nil {
		return nil, err
	}
	configPath := filepath.Join(componentDir, "vite.config.mjs")
	content, err := GenerateViteGroupConfig(group.Assets, group.OutputAbsDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(configPath, content, 0644); err != nil {
		return nil, err
	}
	root := group.Assets[0].EffectiveRoot()
	logs.Info("building client framework: %s %s entries=%d", group.Framework, group.Mode, len(group.Assets))
	cmd := toolchain.PNPMCommand(node, pnpm, root, "exec", "vite", "build", "--config", configPath, "--clearScreen=false")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if text := strings.TrimSpace(stripClientGraphBlocks(out.String())); text != "" {
			logs.Error("vite build output:\n%s", text)
		}
		return nil, fmt.Errorf("vite framework build failed for %s/%s: %w", group.Framework, group.Mode, err)
	}
	graphs, err := ExtractClientDependencyGraphsFromOutput(group.Assets, out.String())
	if err != nil {
		return nil, err
	}
	for _, asset := range group.Assets {
		if err := postProcessGoxCssModuleFiles(asset, group.OutputAbsDir); err != nil {
			return nil, err
		}
	}
	built := make([]BuiltAsset, 0, len(group.Assets))
	for _, asset := range group.Assets {
		entryPath := filepath.Join(group.OutputAbsDir, asset.PublicComponentDirName(), asset.PublicBaseName()+".js")
		if !fileExists(entryPath) {
			if text := strings.TrimSpace(stripClientGraphBlocks(out.String())); text != "" {
				logs.Error("vite build output:\n%s", text)
			}
			return built, fmt.Errorf("vite build did not produce entry bundle: %s", entryPath)
		}
		graph := graphs[clientGraphKey(asset)]
		if strings.TrimSpace(graph.EntryFile) == "" {
			graph = defaultClientDependencyGraph(asset)
		}
		files, err := ScanBuiltClientGroupFiles(asset, group.OutputAbsDir, entryPath, graph.OutputFiles)
		if err != nil {
			return built, err
		}
		entryURL, files, err := FingerprintBuiltClientEntry(asset, entryPath, files)
		if err != nil {
			return built, err
		}
		item := BuiltAsset{Asset: asset, ConfigPath: configPath, EntryURL: entryURL, EntryFile: entryPath, Files: files, Graph: graph}
		logs.Info("client bundle ready: %s", entryPath)
		StoreCachedViteAsset(item)
		built = append(built, item)
	}
	return built, nil
}

func clientGraphKey(asset Asset) string {
	framework := strings.ToLower(strings.TrimSpace(asset.Framework))
	mode := strings.ToLower(strings.TrimSpace(asset.Mode))
	if mode == "" {
		if asset.Dev {
			mode = ModeDev
		} else {
			mode = ModeProd
		}
	}
	path := cleanCachePath(asset.AbsPath)
	if path == "" {
		path = filepath.ToSlash(filepath.Clean(asset.Path))
	}
	return framework + "\x00" + path + "\x00" + mode + "\x00" + strings.TrimSpace(asset.Name)
}

func ExtractClientDependencyGraphsFromOutput(assets []Asset, output string) (map[string]ClientDependencyGraph, error) {
	out := make(map[string]ClientDependencyGraph, len(assets))
	assetByLoose := make(map[string]Asset, len(assets))
	for _, asset := range assets {
		loose := strings.ToLower(strings.TrimSpace(asset.Framework)) + "\x00" + cleanCachePath(asset.AbsPath) + "\x00" + strings.TrimSpace(asset.Name)
		assetByLoose[loose] = asset
	}
	remaining := output
	for {
		start := strings.Index(remaining, clientGraphBeginMarker)
		if start < 0 {
			break
		}
		remaining = remaining[start+len(clientGraphBeginMarker):]
		end := strings.Index(remaining, clientGraphEndMarker)
		if end < 0 {
			break
		}
		raw := strings.TrimSpace(remaining[:end])
		remaining = remaining[end+len(clientGraphEndMarker):]
		if raw == "" {
			continue
		}
		var graph ClientDependencyGraph
		if err := json.Unmarshal([]byte(raw), &graph); err != nil {
			return out, fmt.Errorf("invalid vite dependency graph: %w", err)
		}
		loose := strings.ToLower(strings.TrimSpace(graph.Framework)) + "\x00" + cleanCachePath(graph.EntryFile) + "\x00" + strings.TrimSpace(graph.Component)
		asset, ok := assetByLoose[loose]
		if !ok {
			continue
		}
		out[clientGraphKey(asset)] = normalizeClientDependencyGraph(asset, graph)
	}
	for _, asset := range assets {
		key := clientGraphKey(asset)
		if _, ok := out[key]; !ok {
			out[key] = defaultClientDependencyGraph(asset)
		}
	}
	return out, nil
}

func postProcessGoxCssModuleFiles(asset Asset, outputAbsDir string) error {
	componentName := strings.TrimSpace(asset.Name)
	if componentName == "" {
		return nil
	}
	root := strings.TrimSpace(outputAbsDir)
	if root == "" {
		return nil
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	pattern, err := regexp.Compile(`\.` + regexp.QuoteMeta(componentName) + `\.[A-Za-z_][A-Za-z0-9_-]*\.[A-Za-z_][A-Za-z0-9_-]*`)
	if err != nil {
		return err
	}
	return filepath.WalkDir(rootAbs, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".css" || isClientPrecompressedArtifact(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		next := pattern.ReplaceAllStringFunc(text, func(match string) string {
			if len(match) <= 1 {
				return match
			}
			return "." + strings.ReplaceAll(match[1:], ".", `\.`)
		})
		if next == text {
			return nil
		}
		return os.WriteFile(path, []byte(next), 0644)
	})
}

func ScanBuiltClientGroupFiles(asset Asset, groupOutputAbsDir string, entryPath string, outputFiles []string) ([]BuiltClientFile, error) {
	root := strings.TrimSpace(groupOutputAbsDir)
	if root == "" {
		return nil, fmt.Errorf("missing output dir for client %q", asset.Name)
	}
	entryAbs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	componentDir := asset.PublicComponentDirName()
	seen := map[string]struct{}{}
	files := make([]BuiltClientFile, 0, len(outputFiles)+8)
	addFile := func(path string) error {
		if strings.TrimSpace(path) == "" {
			return nil
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(rootAbs, filepath.FromSlash(path))
		}
		abs, err := filepath.Abs(abs)
		if err != nil {
			return err
		}
		clean := filepath.Clean(abs)
		if _, ok := seen[clean]; ok {
			return nil
		}
		if !fileExists(abs) || isClientPrecompressedArtifact(abs) {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if strings.HasPrefix(relSlash, "vite/") || strings.Contains(relSlash, "/vite/") || filepath.Base(relSlash) == "vite.config.mjs" {
			return nil
		}
		kind := builtFileKind(abs, entryAbs)
		url := builtClientGroupPublicURL(asset, componentDir, relSlash, kind, abs, entryAbs)
		if url == "" {
			return nil
		}
		seen[clean] = struct{}{}
		files = append(files, BuiltClientFile{Kind: kind, URL: url, File: abs})
		return nil
	}

	for _, file := range outputFiles {
		if err := addFile(file); err != nil {
			return nil, err
		}
	}
	componentRoot := filepath.Join(rootAbs, componentDir)
	_ = filepath.WalkDir(componentRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || filepath.Base(path) == "vite.config.mjs" {
			return nil
		}
		return addFile(path)
	})

	if !asset.Dev && !strings.EqualFold(strings.TrimSpace(asset.Mode), ModeDev) {
		vendorRoot := filepath.Join(rootAbs, "vendor")
		_ = filepath.WalkDir(vendorRoot, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() || filepath.Base(path) == "vite.config.mjs" {
				return nil
			}
			return addFile(path)
		})

		// Vendor builds may emit shared chunks next to the vendor directory as
		// <framework>/<mode>/chunks/*.js. Component builds reference those chunks
		// through public URLs such as /_gox/client/chunks/<file>.js, so the client
		// cache must register them even though they are not inside the component
		// folder nor the vendor folder.
		chunksRoot := filepath.Join(rootAbs, "chunks")
		_ = filepath.WalkDir(chunksRoot, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() || filepath.Base(path) == "vite.config.mjs" {
				return nil
			}
			return addFile(path)
		})
	}
	files = ensureBuiltEntryFiles(asset, files, entryAbs)
	sort.Slice(files, func(i, j int) bool {
		if files[i].Kind == files[j].Kind {
			return files[i].URL < files[j].URL
		}
		return assetKindRank(files[i].Kind) < assetKindRank(files[j].Kind)
	})
	return files, nil
}

func builtClientGroupPublicURL(asset Asset, componentDir string, relSlash string, kind string, abs string, entryAbs string) string {
	if relSlash == "" {
		return ""
	}
	if kind == string(BuiltAssetEntry) {
		return asset.PublicEntryURL()
	}
	if kind == string(BuiltAssetSourceMap) && filepath.Base(abs) == filepath.Base(entryAbs)+".map" {
		return asset.PublicMapURL()
	}
	if componentDir != "" {
		prefix := strings.Trim(componentDir, "/") + "/"
		if strings.HasPrefix(relSlash, prefix) {
			inner := strings.TrimPrefix(relSlash, prefix)
			if strings.HasPrefix(inner, "chunks/") {
				return PublicClientPrefix + "/" + inner
			}
			if strings.HasPrefix(inner, "assets/") {
				return PublicClientPrefix + "/" + inner
			}
		}
	}
	if strings.HasPrefix(relSlash, "vendor/") || strings.HasPrefix(relSlash, "chunks/") || strings.HasPrefix(relSlash, "assets/") {
		return PublicClientPrefix + "/" + relSlash
	}
	return PublicClientPrefix + "/" + relSlash
}

func isClientPrecompressedArtifact(path string) bool {
	name := filepath.Base(path)
	return strings.Contains(name, ".l5.zstd") || strings.Contains(name, ".l6.deflate") || strings.Contains(name, ".l6.gzip") || strings.Contains(name, ".l7.br")
}
