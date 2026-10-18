package serverruntime

import (
	"encoding/json"
	"hash/fnv"
	"html"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gox-lang/server/internal/clientbuilder"
	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"

	"github.com/gox-lang/gox"
)

const internalClientAssetPrefix = "/_gox/client/"

type ClientAssetFileMeta struct {
	URL       string
	File      string
	Kind      string
	Name      string
	Framework string
	Entry     string
	Mode      string
}

func (m ClientAssetFileMeta) IsDev() bool {
	return strings.EqualFold(strings.TrimSpace(m.Mode), "dev")
}

func (m ClientAssetFileMeta) CacheControl() string {
	kind := strings.ToLower(strings.TrimSpace(m.Kind))
	if kind == "vendor" || kind == "runtime" {
		return "public, max-age=31536000, immutable"
	}
	if m.IsDev() {
		return "no-cache"
	}
	if kind == "sourcemap" {
		return "no-cache"
	}
	return "public, max-age=31536000, immutable"
}

type ClientAssetPolicy struct {
	files             map[string]string
	metas             map[string]ClientAssetFileMeta
	stylesByEntry     map[string][]string
	filesByEntry      map[string][]string
	bundlesByEntry    map[string]goxcompiler.CompileClientBundle
	entryAliases      map[string]string
	clientEntryByKey  map[string]string
	clientSnapshots   map[string]gox.ClientIslandSnapshot
	runtimeHTML       []byte
	runtimeURL        string
	baseStyleURL      string
	actionsRuntimeURL string
}

func NewClientAssetPolicy(result *goxcompiler.CompileResult) *ClientAssetPolicy {
	policy := &ClientAssetPolicy{files: map[string]string{}, metas: map[string]ClientAssetFileMeta{}, stylesByEntry: map[string][]string{}, filesByEntry: map[string][]string{}, bundlesByEntry: map[string]goxcompiler.CompileClientBundle{}, entryAliases: map[string]string{}, clientEntryByKey: map[string]string{}, clientSnapshots: map[string]gox.ClientIslandSnapshot{}}
	policy.Merge(result)
	return policy
}

func (p *ClientAssetPolicy) Clone() *ClientAssetPolicy {
	if p == nil {
		return NewClientAssetPolicy(nil)
	}
	out := &ClientAssetPolicy{
		files:             cloneStringMap(p.files),
		metas:             cloneClientAssetMetaMap(p.metas),
		stylesByEntry:     cloneStringSliceMap(p.stylesByEntry),
		filesByEntry:      cloneStringSliceMap(p.filesByEntry),
		bundlesByEntry:    cloneClientBundleMap(p.bundlesByEntry),
		entryAliases:      cloneStringMap(p.entryAliases),
		clientEntryByKey:  cloneStringMap(p.clientEntryByKey),
		clientSnapshots:   cloneClientSnapshotMap(p.clientSnapshots),
		runtimeHTML:       append([]byte(nil), p.runtimeHTML...),
		runtimeURL:        p.runtimeURL,
		baseStyleURL:      p.baseStyleURL,
		actionsRuntimeURL: p.actionsRuntimeURL,
	}
	return out
}

func cloneStringMap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneClientAssetMetaMap(src map[string]ClientAssetFileMeta) map[string]ClientAssetFileMeta {
	out := make(map[string]ClientAssetFileMeta, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneStringSliceMap(src map[string][]string) map[string][]string {
	out := make(map[string][]string, len(src))
	for key, value := range src {
		out[key] = append([]string(nil), value...)
	}
	return out
}

func cloneClientBundleMap(src map[string]goxcompiler.CompileClientBundle) map[string]goxcompiler.CompileClientBundle {
	out := make(map[string]goxcompiler.CompileClientBundle, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func cloneClientSnapshotMap(src map[string]gox.ClientIslandSnapshot) map[string]gox.ClientIslandSnapshot {
	out := make(map[string]gox.ClientIslandSnapshot, len(src))
	for key, value := range src {
		copyValue := value
		copyValue.DataAssets = append([]byte(nil), value.DataAssets...)
		copyValue.StyleTags = append([]gox.ClientAssetTag(nil), value.StyleTags...)
		out[key] = copyValue
	}
	return out
}

func (p *ClientAssetPolicy) Merge(result *goxcompiler.CompileResult) {
	if p == nil || result == nil {
		return
	}
	if p.files == nil {
		p.files = make(map[string]string, len(result.ClientAssetMap))
	}
	if p.metas == nil {
		p.metas = make(map[string]ClientAssetFileMeta)
	}
	if p.stylesByEntry == nil {
		p.stylesByEntry = make(map[string][]string)
	}
	if p.filesByEntry == nil {
		p.filesByEntry = make(map[string][]string)
	}
	if p.bundlesByEntry == nil {
		p.bundlesByEntry = make(map[string]goxcompiler.CompileClientBundle)
	}
	if p.entryAliases == nil {
		p.entryAliases = make(map[string]string)
	}
	if p.clientEntryByKey == nil {
		p.clientEntryByKey = make(map[string]string)
	}
	for _, component := range result.ClientComponents {
		clientKey := strings.TrimSpace(component.Key)
		if clientKey == "" {
			clientKey = goxcompiler.ClientComponentKey(component.Framework, component.Entry, component.Dev)
		}
		if clientKey == "" {
			continue
		}
		mode := strings.TrimSpace(component.Mode)
		if mode == "" {
			if component.Dev {
				mode = "dev"
			} else {
				mode = "prod"
			}
		}
		entryPath := strings.TrimSpace(component.Path)
		if entryPath == "" {
			entryPath = strings.TrimSpace(component.Entry)
		}
		stable := normalizeClientAssetURL(clientbuilder.PublicEntryURL(component.Name, strings.ToLower(component.Framework), entryPath, mode))
		if stable != "" {
			p.clientEntryByKey[clientKey] = stable
		}
	}
	for url, file := range result.ClientAssetMap {
		url = normalizeClientAssetURL(url)
		file = strings.TrimSpace(file)
		if url == "" || file == "" {
			continue
		}
		p.files[url] = file
		if _, ok := p.metas[url]; !ok {
			p.metas[url] = ClientAssetFileMeta{URL: url, File: file, Kind: inferClientAssetKind(url), Mode: "prod"}
		}
	}
	if strings.TrimSpace(result.ClientRuntimeURL) != "" {
		p.runtimeURL = normalizeClientAssetURL(result.ClientRuntimeURL)
		if p.runtimeURL != "" {
			p.metas[p.runtimeURL] = ClientAssetFileMeta{URL: p.runtimeURL, File: p.files[p.runtimeURL], Kind: "runtime", Mode: "prod"}
		}
	}
	if strings.TrimSpace(result.ClientBaseStyleURL) != "" {
		p.baseStyleURL = normalizeClientAssetURL(result.ClientBaseStyleURL)
		if p.baseStyleURL != "" {
			p.metas[p.baseStyleURL] = ClientAssetFileMeta{URL: p.baseStyleURL, File: p.files[p.baseStyleURL], Kind: "runtime", Mode: "prod"}
		}
	}
	if strings.TrimSpace(result.ActionsRuntimeURL) != "" {
		p.actionsRuntimeURL = normalizeClientAssetURL(result.ActionsRuntimeURL)
		if p.actionsRuntimeURL != "" {
			p.metas[p.actionsRuntimeURL] = ClientAssetFileMeta{URL: p.actionsRuntimeURL, File: p.files[p.actionsRuntimeURL], Kind: "runtime", Mode: "prod"}
		}
	}
	for _, bundle := range result.ClientBundles {
		p.ReplaceBundle(bundle)
	}
	p.rebuildClientSnapshots()
}

func (p *ClientAssetPolicy) IsEmpty() bool {
	if p == nil {
		return true
	}
	return len(p.files) == 0 &&
		len(p.stylesByEntry) == 0 &&
		len(p.filesByEntry) == 0 &&
		len(p.bundlesByEntry) == 0 &&
		strings.TrimSpace(p.runtimeURL) == "" &&
		strings.TrimSpace(p.baseStyleURL) == "" &&
		strings.TrimSpace(p.actionsRuntimeURL) == ""
}

func (p *ClientAssetPolicy) RuntimeURL() string {
	if p == nil {
		return ""
	}
	return p.runtimeURL
}

func (p *ClientAssetPolicy) BaseStyleURL() string {
	if p == nil {
		return ""
	}
	return p.baseStyleURL
}

func (p *ClientAssetPolicy) ActionsRuntimeURL() string {
	if p == nil {
		return ""
	}
	return p.actionsRuntimeURL
}

func (p *ClientAssetPolicy) ReplaceBundle(bundle goxcompiler.CompileClientBundle) {
	if p == nil {
		return
	}
	if p.files == nil {
		p.files = make(map[string]string)
	}
	if p.metas == nil {
		p.metas = make(map[string]ClientAssetFileMeta)
	}
	if p.stylesByEntry == nil {
		p.stylesByEntry = make(map[string][]string)
	}
	if p.filesByEntry == nil {
		p.filesByEntry = make(map[string][]string)
	}
	if p.bundlesByEntry == nil {
		p.bundlesByEntry = make(map[string]goxcompiler.CompileClientBundle)
	}

	entryKey := cleanClientEntryKey(bundle.Graph.EntryFile)
	if entryKey == "" {
		entryKey = cleanClientEntryKey(bundle.EntryFile)
	}
	if entryKey == "" {
		return
	}

	if oldBundle, ok := p.bundlesByEntry[entryKey]; ok {
		delete(p.stylesByEntry, normalizeClientAssetURL(oldBundle.EntryURL))
		if oldStable := stableClientBundleEntryURL(oldBundle); oldStable != "" {
			delete(p.entryAliases, oldStable)
		}
	}
	for _, oldURL := range p.filesByEntry[entryKey] {
		delete(p.files, oldURL)
		delete(p.metas, oldURL)
	}
	delete(p.filesByEntry, entryKey)
	delete(p.bundlesByEntry, entryKey)
	delete(p.stylesByEntry, normalizeClientAssetURL(bundle.EntryURL))

	seenFiles := make(map[string]struct{})
	seenStyles := make(map[string]struct{})
	styles := make([]string, 0)
	urls := make([]string, 0, len(bundle.Assets))

	for _, asset := range bundle.Assets {
		url := normalizeClientAssetURL(asset.URL)
		file := strings.TrimSpace(asset.File)
		if url == "" || file == "" {
			continue
		}
		if _, ok := seenFiles[url]; !ok {
			seenFiles[url] = struct{}{}
			urls = append(urls, url)
		}
		p.files[url] = file
		p.metas[url] = ClientAssetFileMeta{
			URL:       url,
			File:      file,
			Kind:      strings.ToLower(strings.TrimSpace(asset.Kind)),
			Name:      strings.TrimSpace(bundle.Name),
			Framework: strings.ToLower(strings.TrimSpace(bundle.Framework)),
			Entry:     strings.TrimSpace(bundle.Entry),
			Mode:      normalizeClientAssetMode(bundle.Mode),
		}
		if strings.EqualFold(asset.Kind, "css") {
			if _, ok := seenStyles[url]; !ok {
				seenStyles[url] = struct{}{}
				styles = append(styles, url)
			}
		}
	}

	sort.Strings(urls)
	sort.Strings(styles)
	p.filesByEntry[entryKey] = urls
	p.bundlesByEntry[entryKey] = bundle
	entryURL := normalizeClientAssetURL(bundle.EntryURL)
	if len(styles) > 0 && entryURL != "" {
		p.stylesByEntry[entryURL] = styles
	}
	if stableURL := stableClientBundleEntryURL(bundle); stableURL != "" && entryURL != "" && stableURL != entryURL {
		p.entryAliases[stableURL] = entryURL
	}
	p.rebuildClientSnapshots()
}

func stableClientBundleEntryURL(bundle goxcompiler.CompileClientBundle) string {
	framework := strings.ToLower(strings.TrimSpace(bundle.Framework))
	mode := strings.ToLower(strings.TrimSpace(bundle.Mode))
	if mode == "" {
		mode = "prod"
	}
	path := strings.TrimSpace(bundle.Path)
	if path == "" {
		path = strings.TrimSpace(bundle.Entry)
	}
	if framework == "" || path == "" {
		return ""
	}
	return normalizeClientAssetURL(clientbuilder.PublicEntryURL(bundle.Name, framework, path, mode))
}

func (p *ClientAssetPolicy) FilesForEntry(entryFile string) []string {
	if p == nil || len(p.filesByEntry) == 0 {
		return nil
	}
	entryKey := cleanClientEntryKey(entryFile)
	if entryKey == "" {
		return nil
	}
	urls := p.filesByEntry[entryKey]
	if len(urls) == 0 {
		return nil
	}
	out := make([]string, 0, len(urls))
	seen := make(map[string]struct{}, len(urls))
	for _, url := range urls {
		file := strings.TrimSpace(p.files[normalizeClientAssetURL(url)])
		if file == "" {
			continue
		}
		clean := filepath.Clean(file)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	sort.Strings(out)
	return out
}

func (p *ClientAssetPolicy) ReferencedFiles() map[string]struct{} {
	out := make(map[string]struct{})
	if p == nil || len(p.files) == 0 {
		return out
	}
	for _, file := range p.files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		out[filepath.Clean(file)] = struct{}{}
	}
	return out
}

func (p *ClientAssetPolicy) BundleByEntryFile(entryFile string) (goxcompiler.CompileClientBundle, bool) {
	if p == nil || len(p.bundlesByEntry) == 0 {
		return goxcompiler.CompileClientBundle{}, false
	}
	entryKey := cleanClientEntryKey(entryFile)
	if entryKey == "" {
		return goxcompiler.CompileClientBundle{}, false
	}
	bundle, ok := p.bundlesByEntry[entryKey]
	return bundle, ok
}

func (p *ClientAssetPolicy) BundlesForFrameworkMode(framework string, mode string) []goxcompiler.CompileClientBundle {
	if p == nil || len(p.bundlesByEntry) == 0 {
		return nil
	}
	framework = strings.ToLower(strings.TrimSpace(framework))
	mode = strings.ToLower(strings.TrimSpace(mode))
	out := make([]goxcompiler.CompileClientBundle, 0)
	seen := make(map[string]struct{})
	for _, bundle := range p.bundlesByEntry {
		if strings.ToLower(strings.TrimSpace(bundle.Framework)) != framework {
			continue
		}
		bundleMode := strings.ToLower(strings.TrimSpace(bundle.Mode))
		if bundleMode == "" {
			bundleMode = "prod"
		}
		if mode != "" && bundleMode != mode {
			continue
		}
		key := cleanClientEntryKey(bundle.Graph.EntryFile)
		if key == "" {
			key = cleanClientEntryKey(bundle.EntryFile)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, bundle)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Framework == out[j].Framework {
			if out[i].Mode == out[j].Mode {
				return out[i].EntryURL < out[j].EntryURL
			}
			return out[i].Mode < out[j].Mode
		}
		return out[i].Framework < out[j].Framework
	})
	return out
}

func (p *ClientAssetPolicy) Meta(url string) (ClientAssetFileMeta, bool) {
	if p == nil || len(p.metas) == 0 {
		return ClientAssetFileMeta{}, false
	}
	meta, ok := p.metas[normalizeClientAssetURL(url)]
	return meta, ok
}

func normalizeClientAssetMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "prod"
	}
	return mode
}

func inferClientAssetKind(url string) string {
	path := strings.ToLower(strings.TrimSpace(url))
	if strings.Contains(path, "/vendor/") || strings.Contains(path, "vendor-") {
		return "vendor"
	}
	if strings.Contains(path, "/chunks/") || strings.Contains(path, "chunk-") {
		return "chunk"
	}
	switch filepath.Ext(path) {
	case ".css":
		return "css"
	case ".map":
		return "sourcemap"
	case ".js", ".mjs":
		return "entry"
	default:
		return "asset"
	}
}

func cleanClientEntryKey(path string) string {
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

func (p *ClientAssetPolicy) allMetas() []ClientAssetFileMeta {
	if p == nil || len(p.metas) == 0 {
		return nil
	}
	out := make([]ClientAssetFileMeta, 0, len(p.metas))
	for _, meta := range p.metas {
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

func (p *ClientAssetPolicy) Resolve(url string) (string, bool) {
	if p == nil || len(p.files) == 0 {
		return "", false
	}
	url = normalizeClientAssetURL(url)
	if url == "" {
		return "", false
	}
	file, ok := p.files[url]
	return file, ok
}

func (p *ClientAssetPolicy) StylesForEntry(entryURL string) []string {
	if p == nil || len(p.stylesByEntry) == 0 {
		return nil
	}
	entryURL = normalizeClientAssetURL(entryURL)
	if entryURL == "" {
		return nil
	}
	styles := p.stylesByEntry[entryURL]
	if len(styles) == 0 {
		return nil
	}
	out := make([]string, len(styles))
	copy(out, styles)
	return out
}

func normalizeClientAssetURL(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	if !strings.HasPrefix(url, "/") {
		url = "/" + url
	}
	url = filepath.ToSlash(filepath.Clean(filepath.FromSlash(url)))
	if !strings.HasPrefix(url, internalClientAssetPrefix) {
		return ""
	}
	return url
}

func (r *Runtime) registerClientAssets(result *goxcompiler.CompileResult) {
	if r == nil || result == nil || len(result.ClientAssetMap) == 0 {
		return
	}
	r.requestMu.Lock()
	defer r.requestMu.Unlock()
	old := r.state()
	state := *old
	if state.ClientAssets == nil {
		state.ClientAssets = NewClientAssetPolicy(result)
	} else {
		state.ClientAssets = state.ClientAssets.Clone()
		state.ClientAssets.Merge(result)
	}
	precompressClientAssets(r.ProjectRoot, state.Config.Static.Compression, state.ClientAssets)
	r.runtimeState.Store(&state)
}

func (p *ClientAssetPolicy) rebuildClientSnapshots() {
	if p == nil {
		return
	}
	var runtime strings.Builder
	if u := p.RuntimeURL(); u != "" {
		runtime.WriteString(`<script type="module" src="`)
		runtime.WriteString(u)
		runtime.WriteString(`"></script>`)
	}
	if u := p.BaseStyleURL(); u != "" {
		runtime.WriteString(`<link rel="stylesheet" href="`)
		runtime.WriteString(u)
		runtime.WriteString(`">`)
	}
	p.runtimeHTML = []byte(runtime.String())
	if p.clientSnapshots == nil {
		p.clientSnapshots = make(map[string]gox.ClientIslandSnapshot, len(p.clientEntryByKey))
	}
	for key := range p.clientSnapshots {
		delete(p.clientSnapshots, key)
	}
	for clientKey, stableRaw := range p.clientEntryByKey {
		stable := normalizeClientAssetURL(stableRaw)
		if stable == "" {
			continue
		}
		entryURL := stable
		if actual := normalizeClientAssetURL(p.entryAliases[stable]); actual != "" {
			entryURL = actual
		}
		styles := p.StylesForEntry(entryURL)
		styleTags := make([]gox.ClientAssetTag, 0, len(styles))
		for _, style := range styles {
			styleTags = append(styleTags, gox.ClientAssetTag{
				ID:   clientAssetTagID(style),
				HTML: []byte(`<link rel="stylesheet" href="` + style + `">`),
			})
		}
		assets := make([]string, 0, len(styles)+1)
		if base := p.BaseStyleURL(); base != "" {
			assets = append(assets, base)
		}
		assets = append(assets, styles...)
		var dataAssets []byte
		if len(assets) > 0 {
			encoded, _ := json.Marshal(assets)
			dataAssets = []byte(` data-assets="` + html.EscapeString(string(encoded)) + `"`)
		}
		p.clientSnapshots[filepath.ToSlash(strings.TrimSpace(clientKey))] = gox.ClientIslandSnapshot{ScriptURL: entryURL, StyleTags: styleTags, DataAssets: dataAssets}
	}
}

func clientAssetTagID(value string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	id := h.Sum64()
	if id == 0 {
		return 1
	}
	return id
}

func (p *ClientAssetPolicy) ClientRuntimeHTML() []byte {
	if p == nil || len(p.runtimeHTML) == 0 {
		return nil
	}
	return p.runtimeHTML
}

func (p *ClientAssetPolicy) ResolveClientIsland(clientKey string) (gox.ClientIslandSnapshot, bool) {
	if p == nil {
		return gox.ClientIslandSnapshot{}, false
	}
	value, ok := p.clientSnapshots[filepath.ToSlash(strings.TrimSpace(clientKey))]
	return value, ok
}
