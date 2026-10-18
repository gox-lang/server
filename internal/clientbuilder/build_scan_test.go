package clientbuilder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanBuiltClientFilesRegistersEntryChunksAssetsCSSAndSourceMaps(t *testing.T) {
	dir := t.TempDir()
	asset := Asset{
		Name:         "menu",
		Framework:    "react",
		Path:         "menu/menu.jsx",
		Mode:         ModeDev,
		OutputAbsDir: dir,
	}

	files := map[string]string{
		asset.PublicBaseName() + ".js":     "console.log('entry')\n//# sourceMappingURL=" + asset.PublicBaseName() + ".js.map\n",
		asset.PublicBaseName() + ".js.map": "{}",
		"chunks/abc123.js":                 "export default 1\n//# sourceMappingURL=abc123.js.map\n",
		"chunks/abc123.js.map":             "{}",
		"vendor/vendor-react-abc123.js":    "export default 1\n",
		"assets/style-111.css":             "body{}",
		"assets/logo-222.svg":              "<svg></svg>",
		"assets/font-333.woff2":            "font",
	}
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entry := filepath.Join(dir, asset.PublicBaseName()+".js")
	got, err := ScanBuiltClientFiles(asset, entry)
	if err != nil {
		t.Fatal(err)
	}

	byURL := map[string]BuiltClientFile{}
	for _, file := range got {
		byURL[file.URL] = file
	}

	assertAsset := func(url string, kind BuiltAssetKind) {
		t.Helper()
		file, ok := byURL[url]
		if !ok {
			t.Fatalf("missing asset URL %s in %#v", url, got)
		}
		if file.Kind != string(kind) {
			t.Fatalf("asset %s kind=%s, want %s", url, file.Kind, kind)
		}
		if _, err := os.Stat(file.File); err != nil {
			t.Fatalf("asset %s file does not exist: %v", url, err)
		}
	}

	assertAsset(asset.PublicEntryURL(), BuiltAssetEntry)
	assertAsset(asset.PublicMapURL(), BuiltAssetSourceMap)
	assertAsset(PublicClientPrefix+"/chunks/abc123.js", BuiltAssetChunk)
	assertAsset(PublicClientPrefix+"/chunks/abc123.js.map", BuiltAssetSourceMap)
	assertAsset(PublicClientPrefix+"/vendor/vendor-react-abc123.js", BuiltAssetVendor)
	assertAsset(PublicClientPrefix+"/assets/style-111.css", BuiltAssetCSS)
	assertAsset(PublicClientPrefix+"/assets/logo-222.svg", BuiltAssetAsset)
	assertAsset(PublicClientPrefix+"/assets/font-333.woff2", BuiltAssetAsset)
}

func TestScanBuiltClientFilesDoesNotPublishSourcemapWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	asset := Asset{Name: "menu", Framework: "react", Path: "menu/menu.jsx", Mode: ModeProd, OutputAbsDir: dir}
	entry := filepath.Join(dir, asset.PublicBaseName()+".js")
	if err := os.WriteFile(entry, []byte("console.log('prod')"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ScanBuiltClientFiles(asset, entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range got {
		if file.Kind == string(BuiltAssetSourceMap) {
			t.Fatalf("unexpected sourcemap asset in prod scan: %#v", file)
		}
	}
}

func TestScanBuiltClientGroupFilesRegistersProdVendorChunks(t *testing.T) {
	dir := t.TempDir()
	asset := Asset{
		Name:         "menu",
		Framework:    "preact",
		Path:         "menu/menu.jsx",
		Mode:         ModeProd,
		OutputAbsDir: filepath.Join(dir, "menu-af15d28a"),
	}
	root := dir
	entry := filepath.Join(root, "menu-af15d28a", "menu-af15d28a.js")
	files := map[string]string{
		"menu-af15d28a/menu-af15d28a.js": "import './chunks/local.js';\n",
		"menu-af15d28a/chunks/local.js":  "export default 1\n",
		"vendor/preact.js":               "import '../chunks/vendor.js';\n",
		"chunks/vendor.js":               "export default 2\n",
	}
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ScanBuiltClientGroupFiles(asset, root, entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	byURL := map[string]BuiltClientFile{}
	for _, file := range got {
		byURL[file.URL] = file
	}
	for _, url := range []string{
		PublicClientPrefix + "/chunks/local.js",
		PublicClientPrefix + "/chunks/vendor.js",
		PublicClientPrefix + "/vendor/preact.js",
	} {
		if _, ok := byURL[url]; !ok {
			t.Fatalf("missing URL %s in %#v", url, got)
		}
	}
}

func TestPostProcessGoxCssModuleFilesEscapesDottedClassNames(t *testing.T) {
	dir := t.TempDir()
	asset := Asset{Name: "menuReact", Framework: "react", Path: "menu/menu.jsx", Mode: ModeDev, OutputAbsDir: dir}
	cssPath := filepath.Join(dir, "assets", "style.css")
	if err := os.MkdirAll(filepath.Dir(cssPath), 0o755); err != nil {
		t.Fatal(err)
	}
	input := `.menuReact.style.green{background:green}.other{color:red}.menuReact.theme.primary:hover{color:blue}`
	if err := os.WriteFile(cssPath, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := postProcessGoxCssModuleFiles(asset, dir); err != nil {
		t.Fatal(err)
	}
	gotBytes, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(gotBytes)
	wantContains := []string{`.menuReact\.style\.green{`, `.menuReact\.theme\.primary:hover{`, `.other{`}
	for _, want := range wantContains {
		if !strings.Contains(got, want) {
			t.Fatalf("processed css missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, `.menuReact.style.green`) || strings.Contains(got, `.menuReact.theme.primary`) {
		t.Fatalf("processed css still contains unescaped dotted GoX CSS module selector: %q", got)
	}
}

func TestScanBuiltClientGroupFilesRegistersSharedAssetsForEntry(t *testing.T) {
	dir := t.TempDir()
	asset := Asset{
		Name:         "menuReact",
		Framework:    "react",
		Path:         "menu/menu.jsx",
		Mode:         ModeDev,
		Dev:          true,
		OutputAbsDir: filepath.Join(dir, "menu-f2a90f1d"),
	}
	root := dir
	entry := filepath.Join(root, "menu-f2a90f1d", "menu-f2a90f1d.js")
	files := map[string]string{
		"menu-f2a90f1d/menu-f2a90f1d.js": "import '../assets/DM3GpQX9.css';\n",
		"assets/DM3GpQX9.css":            `.menuReact\.style\.green{background:green}`,
	}
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ScanBuiltClientGroupFiles(asset, root, entry, []string{"assets/DM3GpQX9.css"})
	if err != nil {
		t.Fatal(err)
	}
	byURL := map[string]BuiltClientFile{}
	for _, file := range got {
		byURL[file.URL] = file
	}
	file, ok := byURL[PublicClientPrefix+"/assets/DM3GpQX9.css"]
	if !ok {
		t.Fatalf("missing shared CSS asset in %#v", got)
	}
	if file.Kind != string(BuiltAssetCSS) {
		t.Fatalf("shared asset kind=%s, want css", file.Kind)
	}
}

func TestScanBuiltClientGroupFilesDoesNotRegisterStaleSharedAssets(t *testing.T) {
	dir := t.TempDir()
	asset := Asset{
		Name:         "menuReact",
		Framework:    "react",
		Path:         "menu/menu.jsx",
		Mode:         ModeDev,
		Dev:          true,
		OutputAbsDir: filepath.Join(dir, "menu-f2a90f1d"),
	}
	root := dir
	entry := filepath.Join(root, "menu-f2a90f1d", "menu-f2a90f1d.js")
	files := map[string]string{
		"menu-f2a90f1d/menu-f2a90f1d.js": "import '../assets/new.css';\n",
		"assets/old.css":                 `.old{color:red}`,
		"assets/new.css":                 `.new{color:green}`,
	}
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ScanBuiltClientGroupFiles(asset, root, entry, []string{"assets/new.css"})
	if err != nil {
		t.Fatal(err)
	}
	byURL := map[string]BuiltClientFile{}
	for _, file := range got {
		byURL[file.URL] = file
	}
	if _, ok := byURL[PublicClientPrefix+"/assets/new.css"]; !ok {
		t.Fatalf("missing current shared CSS asset in %#v", got)
	}
	if _, ok := byURL[PublicClientPrefix+"/assets/old.css"]; ok {
		t.Fatalf("stale shared CSS asset was registered: %#v", got)
	}
}
