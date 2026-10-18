package clientbuilder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateViteConfigReactDev(t *testing.T) {
	cfg, err := GenerateViteConfig(Asset{
		Name:         "menu",
		Framework:    "react",
		Path:         "menu.jsx",
		AbsPath:      filepath.Join("tmp", "project", "client", "react", "menu.jsx"),
		ProjectRoot:  filepath.Join("tmp", "project"),
		OutputAbsDir: filepath.Join("tmp", "project", ".build", "client", "react", "menu.jsx", "dev"),
		Dev:          true,
		Mode:         "dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(cfg)
	for _, want := range []string{
		`require.resolve("@vitejs/plugin-react")`,
		`base: "/_gox/client/"`,
		`plugins: [react(), goxClientGraphPlugin()]`,
		`gox-client-graph`,
		`__GOX_CLIENT_GRAPH_BEGIN__`,
		`__GOX_CLIENT_GRAPH_END__`,
		`sourcemap: true`,
		`minify: false`,
		`"process.env.NODE_ENV": "\"development\""`,
		`fileName: () => "menu-`,
		`manualChunks: goxManualChunks`,
		`chunkFileNames: goxChunkFileNames`,
		`const goxVendorChunkName = "vendor-react"`,
		`"react-dom"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateViteConfigUsesDeterministicCSSModuleNames(t *testing.T) {
	cfg, err := GenerateViteConfig(Asset{
		Name:         "Menu",
		Framework:    "react",
		Path:         "menu/menu.jsx",
		AbsPath:      filepath.Join("tmp", "project", "client", "react", "menu", "menu.jsx"),
		ProjectRoot:  filepath.Join("tmp", "project"),
		OutputAbsDir: filepath.Join("tmp", "project", ".build", "client", "react", "dev"),
		Dev:          true,
		Mode:         "dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(cfg)
	for _, want := range []string{
		`const goxCssModuleComponentName = "Menu"`,
		`generateScopedName: goxCssModuleScopedName`,
		`return goxCssModuleComponentName + "." + goxCssModuleFileName(filename) + "." + local`,
		`replace(/\.module\.[^.]+$/i, "")`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateViteConfigPreactUsesPresetFunction(t *testing.T) {
	cfg, err := GenerateViteConfig(Asset{
		Name:         "menu",
		Framework:    "preact",
		Path:         "menu.jsx",
		AbsPath:      filepath.Join("tmp", "project", "client", "preact", "menu.jsx"),
		ProjectRoot:  filepath.Join("tmp", "project"),
		OutputAbsDir: filepath.Join("tmp", "project", ".build", "client", "preact", "dev"),
		Dev:          true,
		Mode:         "dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(cfg)
	for _, want := range []string{
		`require.resolve("@preact/preset-vite")`,
		`const preact = goxPreactPreset.default?.default || goxPreactPreset.default || goxPreactPreset.preact || goxPreactPreset`,
		`plugins: [preact(), goxClientGraphPlugin()]`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateViteConfigPreactExternalizesSharedRuntime(t *testing.T) {
	cfg, err := GenerateViteConfig(Asset{
		Name:         "menu",
		Framework:    "preact",
		Path:         "menu.jsx",
		AbsPath:      filepath.Join("tmp", "project", "client", "preact", "menu.jsx"),
		ProjectRoot:  filepath.Join("tmp", "project"),
		OutputAbsDir: filepath.Join("tmp", "project", ".build", "client", "preact", "prod"),
		Mode:         "prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(cfg)
	for _, want := range []string{
		`"preact"`,
		`"preact/jsx-runtime"`,
		`"preact/jsx-dev-runtime"`,
		`"preact/hooks"`,
		`"preact/compat"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Preact config missing shared runtime %q:\n%s", want, out)
		}
	}
}

func TestGenerateViteConfigVueProd(t *testing.T) {
	cfg, err := GenerateViteConfig(Asset{
		Framework:    "vue",
		AbsPath:      filepath.Join("tmp", "project", "client", "chart.vue"),
		ProjectRoot:  filepath.Join("tmp", "project"),
		OutputAbsDir: filepath.Join("tmp", "project", ".build", "client", "vue", "client", "chart.vue", "prod"),
		Mode:         "prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(cfg)
	for _, want := range []string{
		`require.resolve("@vitejs/plugin-vue")`,
		`base: "/_gox/client/"`,
		`plugins: [vue(), goxClientGraphPlugin()]`,
		`sourcemap: false`,
		`minify: true`,
		`"process.env.NODE_ENV": "\"production\""`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateViteConfigExternalizesGoxPublicModules(t *testing.T) {
	cfg, err := GenerateViteConfig(Asset{
		Framework:    "vue",
		AbsPath:      filepath.Join("tmp", "project", "client", "vue", "menu.vue"),
		ProjectRoot:  filepath.Join("tmp", "project"),
		OutputAbsDir: filepath.Join("tmp", "project", ".build", "client", "vue", "prod"),
		Mode:         "prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(cfg)
	for _, want := range []string{
		`const goxExternalPublicModules = new Set(["@gox/actions", "@gox/websocket"])`,
		`if (goxExternalPublicModules.has(id))`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateViteConfigSvelteProdUsesClientRuntimeAliases(t *testing.T) {
	projectRoot := filepath.Join("tmp", "project")
	cfg, err := GenerateViteConfig(Asset{
		Framework:    "svelte",
		AbsPath:      filepath.Join(projectRoot, "client", "svelte", "menu.svelte"),
		ProjectRoot:  projectRoot,
		OutputAbsDir: filepath.Join(projectRoot, ".build", "client", "svelte", "prod"),
		Mode:         "prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := string(cfg)
	for _, want := range []string{
		filepath.Join(projectRoot, "client", "svelte", "node_modules", "svelte", "src", "index-client.js"),
		filepath.Join(projectRoot, "client", "svelte", "node_modules", "svelte", "src", "internal", "client", "index.js"),
		filepath.Join(projectRoot, "client", "svelte", "node_modules", "svelte", "src", "internal", "flags", "legacy.js"),
		filepath.Join(projectRoot, "client", "svelte", "node_modules", "svelte", "src", "internal", "flags", "async.js"),
		filepath.Join(projectRoot, "client", "svelte", "node_modules", "svelte", "src", "internal", "flags", "tracing.js"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("svelte prod config missing client runtime alias %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `replacement: require.resolve("svelte")`) {
		t.Fatalf("svelte prod config must not resolve the package root through Node:\n%s", out)
	}
}

func TestWriteViteConfigs(t *testing.T) {
	dir := t.TempDir()
	asset := Asset{
		Framework:    "svelte",
		AbsPath:      filepath.Join(dir, "client", "menu.svelte"),
		ProjectRoot:  dir,
		OutputAbsDir: filepath.Join(dir, ".build", "client", "svelte", "client", "menu.svelte", "dev"),
		Dev:          true,
		Mode:         "dev",
	}
	written, err := WriteViteConfigs([]Asset{asset})
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 written config, got %+v", written)
	}
	content, err := os.ReadFile(asset.ViteConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `@sveltejs/vite-plugin-svelte`) {
		t.Fatalf("expected svelte config, got:\n%s", string(content))
	}
}

func TestEnsureFrameworkWorkspaceWritesManagedDependencies(t *testing.T) {
	dir := t.TempDir()
	writeDefaultProjectManifest(t, dir)
	ws, err := EnsureFrameworkWorkspace(dir, "react")
	if err != nil {
		t.Fatal(err)
	}
	if ws.RootDir != filepath.Join(dir, "client", "react") {
		t.Fatalf("workspace root=%q", ws.RootDir)
	}
	content, err := os.ReadFile(filepath.Join(dir, "client", "react", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{`"react"`, `"react-dom"`, `"vite"`, `"@vitejs/plugin-react"`, `"typescript"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("package.json missing %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"pnpm"`) {
		t.Fatalf("package.json should not contain pnpm settings:\n%s", text)
	}
	workspaceSettings, err := os.ReadFile(filepath.Join(dir, "client", "react", "pnpm-workspace.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workspaceSettings), "onlyBuiltDependencies:") || !strings.Contains(string(workspaceSettings), "esbuild") {
		t.Fatalf("pnpm-workspace.yaml missing esbuild approval:\n%s", string(workspaceSettings))
	}
}

func TestEnsureFrameworkWorkspacePreservesUserDependencies(t *testing.T) {
	dir := t.TempDir()
	writeDefaultProjectManifest(t, dir)
	root := filepath.Join(dir, "client", "react")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "package.json")
	if err := os.WriteFile(pkg, []byte(`{"private":true,"type":"module","dependencies":{"web3":"^4.0.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureFrameworkWorkspace(dir, "react"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{`"web3"`, `"react"`, `"vite"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("package.json missing %s:\n%s", want, text)
		}
	}
}

func TestGenerateViteConfigSolidDevKeepsWebResolvableAndLeavesCoreToPlugin(t *testing.T) {
	root := t.TempDir()
	asset := Asset{
		Framework:   "solid",
		Mode:        ModeDev,
		Dev:         true,
		ProjectRoot: root,
	}

	config := goxFrameworkResolveAliasJS(asset, "solid", ModeDev, true)
	if strings.Contains(config, "solid-js/dist/solid.js") {
		t.Fatalf("solid dev config must not force the production solid-js runtime: %s", config)
	}
	wantWeb := filepath.Join(root, "client", "solid", "node_modules", "solid-js", "web", "dist", "web.js")
	if !strings.Contains(config, wantWeb) {
		t.Fatalf("solid dev config must resolve solid-js/web for the GOX wrapper; missing %q in: %s", wantWeb, config)
	}
	if strings.Contains(config, `require.resolve("solid-js")`) {
		t.Fatalf("solid dev config must leave solid-js resolution to vite-plugin-solid: %s", config)
	}
	if strings.Contains(config, `require.resolve("solid-js/web")`) {
		t.Fatalf("solid dev config must use the explicit client web runtime path instead of require.resolve: %s", config)
	}
}

func TestGenerateViteConfigSolidProdKeepsSharedRuntimeResolution(t *testing.T) {
	root := t.TempDir()
	asset := Asset{
		Framework:   "solid",
		Mode:        ModeProd,
		ProjectRoot: root,
	}

	config := goxFrameworkResolveAliasJS(asset, "solid", ModeProd, false)
	if !strings.Contains(config, `require.resolve("solid-js")`) {
		t.Fatalf("solid prod config must keep solid-js resolution for the shared runtime: %s", config)
	}
	if !strings.Contains(config, `require.resolve("solid-js/web")`) {
		t.Fatalf("solid prod config must keep solid-js/web resolution for the shared runtime: %s", config)
	}
}

func TestGenerateViteFrameworkVendorConfigEmitsIntegrityManifest(t *testing.T) {
	root := t.TempDir()
	asset := Asset{
		Framework:   "react",
		Mode:        ModeProd,
		ProjectRoot: root,
	}
	config, err := GenerateViteFrameworkVendorConfig(
		asset,
		filepath.Join(root, ".build", "client", "react", ModeProd),
		frameworkModuleFacades("react"),
		"19.1.1",
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, want := range []string{
		`name: "gox-vendor-manifest"`,
		`fileName: ".gox-vendor-manifest.json"`,
		`plugins: [goxVendorManifestPlugin()]`,
		`entryFileNames: "[name].19.1.1.js"`,
		`chunkFileNames: "chunks/19.1.1/[hash].js"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("vendor vite config missing %q:\n%s", want, text)
		}
	}
}
