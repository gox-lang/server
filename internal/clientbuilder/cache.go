package clientbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gox-lang/server/internal/logs"
)

const clientCacheFileName = "clientCache.json"
const clientCacheVersion = 2

type clientCacheStore struct {
	Version int                         `json:"version"`
	Entries map[string]clientCacheEntry `json:"entries"`
}

type clientCacheEntry struct {
	Key        string                `json:"key"`
	Asset      clientCacheAsset      `json:"asset"`
	ConfigPath string                `json:"configPath"`
	EntryURL   string                `json:"entryURL"`
	EntryFile  string                `json:"entryFile"`
	Files      []BuiltClientFile     `json:"files"`
	Graph      ClientDependencyGraph `json:"graph"`
	Tracked    []clientCachedFile    `json:"tracked"`
}

type clientCacheAsset struct {
	Name          string `json:"name"`
	Framework     string `json:"framework"`
	Entry         string `json:"entry"`
	Path          string `json:"path"`
	AbsPath       string `json:"absPath"`
	Dev           bool   `json:"dev"`
	Mode          string `json:"mode"`
	OutputDir     string `json:"outputDir"`
	OutputAbsDir  string `json:"outputAbsDir"`
	ProjectRoot   string `json:"projectRoot"`
	FrameworkRoot string `json:"frameworkRoot"`
}

type clientCachedFile struct {
	Path    string `json:"path"`
	ModTime int64  `json:"modTime"`
	Size    int64  `json:"size"`
}

var clientCacheMu sync.Mutex
var clientCaches = map[string]*clientCacheStore{}
var clientCachesLoaded = map[string]bool{}

// LoadClientCache loads and reconciles the persisted client bundle cache for a project root.
// It is intentionally called at server startup, before the server starts accepting requests.
func LoadClientCache(root string) {
	root = cleanCacheRoot(root)
	if root == "" {
		logs.Info("client cache loaded (0 entries, 0 reconcile)")
		return
	}

	clientCacheMu.Lock()
	defer clientCacheMu.Unlock()

	store := loadClientCacheLocked(root)
	if store == nil {
		logs.Info("client cache loaded (0 entries, 0 reconcile)")
		return
	}
	reconciled := 0
	for key, entry := range store.Entries {
		if clientCacheEntryValid(entry) {
			continue
		}
		delete(store.Entries, key)
		reconciled++
	}
	if reconciled > 0 {
		saveClientCacheLocked(root, store)
	}
	logs.Info("client cache loaded (%d entries, %d reconcile)", len(store.Entries), reconciled)
}

func TryLoadCachedViteAsset(asset Asset) (BuiltAsset, bool) {
	if !isClientCacheableAsset(asset) {
		return BuiltAsset{}, false
	}
	root := cleanCacheRoot(asset.ProjectRoot)
	if root == "" {
		return BuiltAsset{}, false
	}

	clientCacheMu.Lock()
	defer clientCacheMu.Unlock()

	store := loadClientCacheLocked(root)
	if store == nil || len(store.Entries) == 0 {
		return BuiltAsset{}, false
	}
	key := clientCacheKey(asset)
	entry, ok := store.Entries[key]
	if ok && strings.TrimSpace(entry.EntryURL) != asset.PublicEntryURL() {
		delete(store.Entries, key)
		saveClientCacheLocked(root, store)
		return BuiltAsset{}, false
	}
	if !ok || !clientCacheEntryValid(entry) {
		if ok {
			delete(store.Entries, key)
			saveClientCacheLocked(root, store)
		}
		return BuiltAsset{}, false
	}
	built := BuiltAsset{
		Asset:      asset,
		ConfigPath: entry.ConfigPath,
		EntryURL:   entry.EntryURL,
		EntryFile:  entry.EntryFile,
		Files:      append([]BuiltClientFile(nil), entry.Files...),
		Graph:      entry.Graph,
	}
	if repaired, changed := repairCachedBuiltAssetFiles(built); changed {
		built = repaired
		if repairedEntry, ok := newClientCacheEntry(built); ok {
			store.Entries[key] = repairedEntry
			saveClientCacheLocked(root, store)
		}
	}
	return built, true
}

func StoreCachedViteAsset(built BuiltAsset) {
	asset := built.Asset
	if !isClientCacheableAsset(asset) {
		return
	}
	root := cleanCacheRoot(asset.ProjectRoot)
	if root == "" {
		return
	}
	entry, ok := newClientCacheEntry(built)
	if !ok {
		return
	}

	clientCacheMu.Lock()
	defer clientCacheMu.Unlock()

	store := loadClientCacheLocked(root)
	if store == nil {
		store = &clientCacheStore{Version: clientCacheVersion, Entries: map[string]clientCacheEntry{}}
		clientCaches[root] = store
		clientCachesLoaded[root] = true
	}
	if store.Entries == nil {
		store.Entries = map[string]clientCacheEntry{}
	}
	store.Version = clientCacheVersion
	store.Entries[entry.Key] = entry
	saveClientCacheLocked(root, store)
}

func repairCachedBuiltAssetFiles(built BuiltAsset) (BuiltAsset, bool) {
	asset := built.Asset
	if strings.TrimSpace(asset.OutputAbsDir) == "" || strings.TrimSpace(built.EntryFile) == "" {
		return built, false
	}
	entryPath := built.EntryFile
	if !fileExists(entryPath) {
		return built, false
	}
	files, err := ScanBuiltClientGroupFiles(asset, asset.OutputAbsDir, entryPath, built.Graph.OutputFiles)
	if err != nil || len(files) == 0 {
		return built, false
	}
	_, files, err = FingerprintBuiltClientEntry(asset, entryPath, files)
	if err != nil {
		return built, false
	}
	oldKey := normalizedBuiltFileSet(built.Files)
	newKey := normalizedBuiltFileSet(files)
	if oldKey == newKey {
		return built, false
	}
	built.Files = files
	return built, true
}

func normalizedBuiltFileSet(files []BuiltClientFile) string {
	if len(files) == 0 {
		return ""
	}
	parts := make([]string, 0, len(files))
	for _, file := range files {
		parts = append(parts, strings.TrimSpace(file.Kind)+"\x00"+strings.TrimSpace(file.URL)+"\x00"+cleanCachePath(file.File))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

func isClientCacheableAsset(asset Asset) bool {
	root := cleanCacheRoot(asset.ProjectRoot)
	if root == "" || strings.TrimSpace(asset.OutputAbsDir) == "" {
		return false
	}
	outputAbs, err := filepath.Abs(asset.OutputAbsDir)
	if err != nil {
		return false
	}
	expected := filepath.Join(root, ".build", "client")
	rel, err := filepath.Rel(expected, outputAbs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func cleanCacheRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	return filepath.Clean(abs)
}

func clientCachePath(root string) string {
	return filepath.Join(root, ".build", "cache", clientCacheFileName)
}

func loadClientCacheLocked(root string) *clientCacheStore {
	root = cleanCacheRoot(root)
	if root == "" {
		return nil
	}
	if clientCachesLoaded[root] {
		return clientCaches[root]
	}
	clientCachesLoaded[root] = true
	path := clientCachePath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		store := &clientCacheStore{Version: clientCacheVersion, Entries: map[string]clientCacheEntry{}}
		clientCaches[root] = store
		return store
	}
	var store clientCacheStore
	if err := json.Unmarshal(data, &store); err != nil || store.Version != clientCacheVersion {
		store = clientCacheStore{Version: clientCacheVersion, Entries: map[string]clientCacheEntry{}}
	}
	if store.Entries == nil {
		store.Entries = map[string]clientCacheEntry{}
	}
	clientCaches[root] = &store
	return &store
}

func saveClientCacheLocked(root string, store *clientCacheStore) {
	if root == "" || store == nil {
		return
	}
	path := clientCachePath(root)
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

func clientCacheKey(asset Asset) string {
	return asset.BuildIdentity()
}

func cleanCachePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func newClientCacheEntry(built BuiltAsset) (clientCacheEntry, bool) {
	asset := built.Asset
	tracked := make([]clientCachedFile, 0)
	addTracked := func(path string) {
		path = cleanCachePath(path)
		if path == "" {
			return
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return
		}
		tracked = append(tracked, clientCachedFile{Path: path, ModTime: info.ModTime().UnixNano(), Size: info.Size()})
	}

	addTracked(asset.AbsPath)
	addTracked(filepath.Join(asset.FrameworkRoot, "package.json"))
	addTracked(filepath.Join(asset.FrameworkRoot, "pnpm-lock.yaml"))
	addTracked(filepath.Join(asset.FrameworkRoot, "pnpm-workspace.yaml"))
	addTracked(built.ConfigPath)
	for _, file := range built.Graph.ModuleFiles {
		if shouldTrackClientModuleFile(asset, file) {
			addTracked(file)
		}
	}

	seen := map[string]struct{}{}
	compact := tracked[:0]
	for _, item := range tracked {
		if item.Path == "" {
			continue
		}
		if _, ok := seen[item.Path]; ok {
			continue
		}
		seen[item.Path] = struct{}{}
		compact = append(compact, item)
	}
	tracked = compact
	sort.Slice(tracked, func(i, j int) bool { return tracked[i].Path < tracked[j].Path })

	if len(tracked) == 0 || strings.TrimSpace(built.EntryFile) == "" || len(built.Files) == 0 {
		return clientCacheEntry{}, false
	}

	return clientCacheEntry{
		Key: clientCacheKey(asset),
		Asset: clientCacheAsset{
			Name: asset.Name, Framework: asset.Framework, Entry: asset.Entry, Path: asset.Path, AbsPath: cleanCachePath(asset.AbsPath), Dev: asset.Dev, Mode: asset.Mode,
			OutputDir: asset.OutputDir, OutputAbsDir: cleanCachePath(asset.OutputAbsDir), ProjectRoot: cleanCacheRoot(asset.ProjectRoot), FrameworkRoot: cleanCachePath(asset.FrameworkRoot),
		},
		ConfigPath: cleanCachePath(built.ConfigPath),
		EntryURL:   built.EntryURL,
		EntryFile:  cleanCachePath(built.EntryFile),
		Files:      normalizeCachedBuiltFiles(built.Files),
		Graph:      built.Graph,
		Tracked:    tracked,
	}, true
}

func shouldTrackClientModuleFile(asset Asset, path string) bool {
	path = cleanCachePath(path)
	if path == "" {
		return false
	}
	frameworkRoot := cleanCachePath(asset.FrameworkRoot)
	if frameworkRoot == "" {
		return true
	}
	rel, err := filepath.Rel(frameworkRoot, path)
	if err != nil {
		return true
	}
	relSlash := filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(relSlash, "../") || strings.HasPrefix(relSlash, "/") {
		return true
	}
	return !strings.HasPrefix(relSlash, "node_modules/") && !strings.Contains(relSlash, "/node_modules/") && !strings.HasPrefix(relSlash, ".pnpm/") && !strings.Contains(relSlash, "/.pnpm/")
}

func normalizeCachedBuiltFiles(files []BuiltClientFile) []BuiltClientFile {
	out := make([]BuiltClientFile, 0, len(files))
	seen := map[string]struct{}{}
	for _, file := range files {
		url := strings.TrimSpace(file.URL)
		path := cleanCachePath(file.File)
		if url == "" || path == "" {
			continue
		}
		key := url + "\x00" + path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, BuiltClientFile{Kind: file.Kind, URL: url, File: path})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].URL < out[j].URL
		}
		return assetKindRank(out[i].Kind) < assetKindRank(out[j].Kind)
	})
	return out
}

func clientCacheEntryValid(entry clientCacheEntry) bool {
	if strings.TrimSpace(entry.EntryURL) == "" || !cachedFileExists(entry.EntryFile) || len(entry.Files) == 0 || len(entry.Tracked) == 0 {
		return false
	}
	for _, file := range entry.Files {
		if strings.TrimSpace(file.URL) == "" || !cachedFileExists(file.File) {
			return false
		}
	}
	for _, item := range entry.Tracked {
		if item.Path == "" {
			return false
		}
		info, err := os.Stat(item.Path)
		if err != nil || info.IsDir() {
			return false
		}
		if info.ModTime().UnixNano() != item.ModTime || info.Size() != item.Size {
			return false
		}
	}
	return true
}

func cachedFileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
