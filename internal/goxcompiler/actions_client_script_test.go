package gox

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildActionsClientScriptUsesMetadataAndImportMap(t *testing.T) {
	metadata := buildActionsMetadataScript(map[string][]string{
		"MyAction":   {"name", "age"},
		"SubmitForm": {"name", "files"},
	})
	imports := buildActionsImportMapEntries("/_gox/client/gox-actions-test.js")

	for _, want := range []string{
		`<script id="_GOX-ACTIONS" type="application/json">`,
		`"MyAction":["name","age"]`,
		`"SubmitForm":["name","files"]`,
	} {
		if !strings.Contains(metadata, want) {
			t.Fatalf("actions metadata script missing %q:\n%s", want, metadata)
		}
	}
	if strings.Contains(metadata, "data-gox-actions") {
		t.Fatalf("actions metadata script must not use data-gox-actions:\n%s", metadata)
	}
	if imports != `"@gox/actions":"/_gox/client/gox-actions-test.js"` {
		t.Fatalf("unexpected actions import map entries: %s", imports)
	}
}

func TestBuildStaticImportMapProducesScriptAndCSPHash(t *testing.T) {
	entries, script, hash := buildStaticImportMap("/_gox/client/gox-actions.js", map[string]string{"react": "/_gox/client/vendor/react.js"})
	if len(entries) != 2 || entries[0].Specifier != "@gox/actions" || entries[0].URL != "/_gox/client/gox-actions.js" || entries[1].Specifier != "react" || entries[1].URL != "/_gox/client/vendor/react.js" {
		t.Fatalf("unexpected static entries: %#v", entries)
	}
	if !strings.HasPrefix(script, `<script type="importmap">{"imports":`) || !strings.HasSuffix(script, `</script>`) {
		t.Fatalf("unexpected static script: %s", script)
	}
	if !strings.HasPrefix(hash, "'sha256-") || !strings.HasSuffix(hash, "'") {
		t.Fatalf("unexpected CSP hash: %s", hash)
	}
}

func TestClientImportMapIncludesWebSocketModule(t *testing.T) {
	entries := clientFrameworkImportMapEntries(t.TempDir(), nil)
	if got := entries["@gox/websocket"]; got != "/_gox/websocket/client.js" {
		t.Fatalf("@gox/websocket URL = %q", got)
	}
}

func TestImportMapEntriesVarDeclDoesNotRequireGoxImport(t *testing.T) {
	decl := importMapEntriesVarDecl("goxStaticImportMapEntries", nil)
	file := &ast.File{
		Name:  ast.NewIdent("main"),
		Decls: []ast.Decl{decl},
	}
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), file); err != nil {
		t.Fatalf("format generated declaration: %v", err)
	}
	generated := out.String()
	if strings.Contains(generated, "gox.ImportMapEntry") {
		t.Fatalf("empty import map declaration must not depend on gox import:\n%s", generated)
	}
	if !strings.Contains(generated, "[][2]string") {
		t.Fatalf("generated declaration must use import-free pair representation:\n%s", generated)
	}
}

func TestClientFrameworkImportMapEntriesIncludesInstalledFrameworks(t *testing.T) {
	root := t.TempDir()
	writeInstalledFrameworkPackageVersion(t, root, "vue", "vue", "3.5.21")
	writeInstalledFrameworkPackageVersion(t, root, "react", "react", "19.1.1")
	entries := clientFrameworkImportMapEntries(root, []string{"vue", "react", "unknown", "react"})
	for specifier, want := range map[string]string{
		"@gox/websocket":        "/_gox/websocket/client.js",
		"react":                 "/_gox/client/vendor/react.19.1.1.js",
		"react-dom/client":      "/_gox/client/vendor/react-dom-client.19.1.1.js",
		"react/jsx-runtime":     "/_gox/client/vendor/react-jsx-runtime.19.1.1.js",
		"react/jsx-dev-runtime": "/_gox/client/vendor/react-jsx-dev-runtime.19.1.1.js",
		"vue":                   "/_gox/client/vendor/vue.3.5.21.js",
	} {
		if got := entries[specifier]; got != want {
			t.Errorf("%s URL = %q, want %q", specifier, got, want)
		}
	}
	if _, ok := entries["unknown"]; ok {
		t.Fatal("unknown framework unexpectedly produced an import map entry")
	}
}

func TestCompileIncludesInstalledFrameworkAliasesWithoutClientDeclarations(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeInstalledFrameworkPackageVersion(t, dir, "react", "react", "19.1.1")
	writeInstalledFrameworkPackageVersion(t, dir, "vue", "vue", "3.5.21")
	entry := filepath.Join(dir, "index.gox")
	if err := os.WriteFile(entry, []byte(`module main

func GET() string { return "ok" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, ".build", "generated", "go")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(outDir, "index.go")
	result, err := Compile(entry, outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ClientComponents) != 0 {
		t.Fatalf("expected no declared client components, got %#v", result.ClientComponents)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	generated := string(data)
	for _, want := range []string{
		`[2]string{"react", "/_gox/client/vendor/react.19.1.1.js"}`,
		`[2]string{"react-dom/client", "/_gox/client/vendor/react-dom-client.19.1.1.js"}`,
		`[2]string{"react/jsx-runtime", "/_gox/client/vendor/react-jsx-runtime.19.1.1.js"}`,
		`[2]string{"vue", "/_gox/client/vendor/vue.3.5.21.js"}`,
		`[2]string{"@gox/websocket", "/_gox/websocket/client.js"}`,
	} {
		if !strings.Contains(generated, want) {
			t.Fatalf("compiled page missing installed framework import %q:\n%s", want, generated)
		}
	}
}

func TestCompileCreatesRequiredFrameworkWorkspaceBeforeImportMap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProjectManifest(t, dir)
	clientDir := filepath.Join(dir, "client", "vue")
	if err := os.MkdirAll(clientDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clientDir, "menu.vue"), []byte("<template><div>menu</div></template>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "index.gox")
	if err := os.WriteFile(entry, []byte(`module main

client menu vue {
	src: "menu.vue"
	dev: true
}

func GET() string { return "ok" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, ".build", "generated", "go")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(outDir, "index.go")
	writeInstalledFrameworkPackageVersion(t, dir, "vue", "vue", "3.5.21")

	if _, err := Compile(entry, outPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clientDir, "package.json")); err != nil {
		t.Fatalf("required framework workspace was not materialized before bundle generation: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[2]string{"vue", "/_gox/client/vendor/vue.3.5.21.js"}`; !strings.Contains(string(data), want) {
		t.Fatalf("first compile missing required framework import %q:\n%s", want, data)
	}
}

func writeInstalledFrameworkPackageVersion(t *testing.T, root, framework, pkg, version string) {
	t.Helper()
	dir := filepath.Join(root, "client", framework, "node_modules", pkg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"version":"`+version+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}
