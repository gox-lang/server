package gox

import (
	"bytes"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/goxerrors"
)

func TestClientDeclarationAndTemplateRender(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

client Menu react {
    src: "client/menu.jsx"
    dev: true
}

func GET(ctx *gox.Context) gox.Page {
	count := 10
	return <@Menu hydrate="visible" isolate count={{count}} class="top-menu">
		<span>Loading...</span>
	</@Menu>
}
`

	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	if len(mod.Clients) != 1 {
		t.Fatalf("expected 1 client declaration, got %d", len(mod.Clients))
	}
	client := mod.Clients[0]
	if client.Name != "Menu" || client.Framework != ClientReact || client.Entry != "client/menu.jsx" || !client.Dev {
		t.Fatalf("unexpected client declaration: %#v", client)
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), mod.File); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		`gox.WriteClientIsland(ctx, "pages/index.gox#Menu#react#client/menu.jsx#dev", func(ctx *gox.Context)`,
		`gox.WriteClientPropsAttribute(ctx.Writer, map[string]any{"count": count, "class": "top-menu"})`,
		`ctx.Writer.Write(goxStaticMainGET[1])`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("compiled template missing %q:\n%s", want, out)
		}
	}
}

func TestClientComponentSelfCloseUsesExplicitClosingTag(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

client dashboard react {
    src: "client/dashboard.jsx"
}

func GET(ctx *gox.Context) gox.Page {
	return <@dashboard hydrate="load" />
}
`

	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), mod.File); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		`gox.WriteClientIsland(ctx, "pages/index.gox#dashboard#react#client/dashboard.jsx#prod", func(ctx *gox.Context)`,
		`nil)`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("self-closed client component missing %q:\n%s", want, out)
		}
	}
}

func TestClientDeclarationNameDoesNotAffectPublicClientURL(t *testing.T) {
	menuSrc := `module main

import "github.com/gox-lang/gox"

client menu react {
    src: "menu/menu.jsx"
    dev: true
}

func GET(ctx *gox.Context) gox.Page {
	return <@menu hydrate="load" />
}
`
	reactMenuSrc := `module main

import "github.com/gox-lang/gox"

client reactmenu react {
    src: "menu/menu.jsx"
    dev: true
}

func GET(ctx *gox.Context) gox.Page {
	return <@reactmenu hydrate="load" />
}
`

	render := func(src string) string {
		mod, err := ParseModule("pages/index.gox", src)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := format.Node(&buf, token.NewFileSet(), mod.File); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	menuOut := render(menuSrc)
	reactMenuOut := render(reactMenuSrc)
	if !strings.Contains(menuOut, `"pages/index.gox#menu#react#menu/menu.jsx#dev"`) {
		t.Fatalf("menu declaration did not use its stable client key:\n%s", menuOut)
	}
	if !strings.Contains(reactMenuOut, `"pages/index.gox#reactmenu#react#menu/menu.jsx#dev"`) {
		t.Fatalf("reactmenu declaration did not use its stable client key:\n%s", reactMenuOut)
	}
	if strings.Contains(menuOut, `data-script=`) || strings.Contains(reactMenuOut, `data-script=`) {
		t.Fatalf("generated template should resolve physical client URLs through the SDK runtime")
	}
}

func TestClientComponentRejectsUnknownDeclaration(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <@Menu />
}
`

	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected unknown client component to fail")
	}
	if !strings.Contains(err.Error(), "unknown client component") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientComponentRejectsInvalidHydrate(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

client Menu react {
    src: "client/menu.jsx"
}

func GET(ctx *gox.Context) gox.Page {
	return <@Menu hydrate="later" />
}
`

	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected invalid hydrate to fail")
	}
	if !strings.Contains(err.Error(), "invalid hydrate mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientDeclarationRejectsFunctionScope(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	client menu react {
		src: "client/menu.jsx"
	}

	return <h1>Hello Gox</h1>
}
`

	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected client declaration inside function to fail")
	}
	if !strings.Contains(err.Error(), "client declarations must be declared at module top-level") {
		t.Fatalf("unexpected error: %v", err)
	}
	if goxErr, ok := err.(*goxerrors.GoxError); ok {
		if goxErr.Line != 6 {
			t.Fatalf("expected error on client declaration line 6, got %d", goxErr.Line)
		}
	}
}

func TestClientDeclarationSyntaxErrorsIncludeLine(t *testing.T) {
	cases := []struct {
		name string
		src  string
		kind string
		line int
	}{
		{
			name: "invalid src semicolon",
			src: `module main

import "github.com/gox-lang/gox"

client menu react {
	src: "client/menu.jsx";
}

func GET(ctx *gox.Context) gox.Page {
	return <h1>Hello Gox</h1>
}
`,
			kind: "unexpected-token",
			line: 6,
		},
		{
			name: "missing open brace",
			src: `module main

import "github.com/gox-lang/gox"

client menu react 
	src: "client/menu.jsx"
}

func GET(ctx *gox.Context) gox.Page {
	return <h1>Hello Gox</h1>
}
`,
			kind: "invalid-client-declaration",
			line: 6,
		},
		{
			name: "invalid framework",
			src: `module main

import "github.com/gox-lang/gox"

client menu rseact {
	src: "client/menu.jsx"
}

func GET(ctx *gox.Context) gox.Page {
	return <h1>Hello Gox</h1>
}
`,
			kind: "invalid-client-framework",
			line: 5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseModule("pages/index.gox", tc.src)
			if err == nil {
				t.Fatal("expected client declaration syntax error")
			}
			goxErr, ok := err.(*goxerrors.GoxError)
			if !ok {
				t.Fatalf("expected *goxerrors.GoxError, got %T: %v", err, err)
			}
			if goxErr.Kind != tc.kind {
				t.Fatalf("expected kind %q, got %q", tc.kind, goxErr.Kind)
			}
			if goxErr.Line != tc.line {
				t.Fatalf("expected line %d, got %d", tc.line, goxErr.Line)
			}
			if goxErr.File != "pages/index.gox" {
				t.Fatalf("expected normalized file path, got %q", goxErr.File)
			}
			if goxErr.Column == 0 {
				t.Fatalf("expected non-zero column")
			}
		})
	}
}

func TestAnalyzeResultIncludesClientComponents(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "client", "react"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "client", "react"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "client", "react", "menu.jsx"), []byte("export default function Menu() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "index.gox")
	if err := os.WriteFile(entry, []byte(`module main

client menu react {
	src: "client/menu.jsx"
	dev: true
}

func GET() string { return "ok" }
`), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := Analyze(entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ClientComponents) != 1 {
		t.Fatalf("expected 1 client component, got %d", len(result.ClientComponents))
	}
	client := result.ClientComponents[0]
	if client.Name != "menu" || client.Framework != "react" || client.Entry != "client/menu.jsx" || client.Path != "client/react/menu.jsx" || !client.Dev {
		t.Fatalf("unexpected client component: %+v", client)
	}
	if client.Mode != "dev" || client.OutputDir != filepath.ToSlash(filepath.Join(".build", "client", "react", "dev")) {
		t.Fatalf("unexpected client output metadata: %+v", client)
	}
	if client.AbsPath == "" || !strings.HasSuffix(filepath.ToSlash(client.AbsPath), "/client/react/menu.jsx") {
		t.Fatalf("expected resolved client abs path, got %+v", client)
	}
	if len(result.Modules) != 1 || len(result.Modules[0].Clients) != 1 {
		t.Fatalf("expected module clients in result, got modules=%+v", result.Modules)
	}
}

func TestAnalyzeRejectsMissingClientEntry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "index.gox")
	if err := os.WriteFile(entry, []byte(`module main

client menu react {
	src: "client/missing.jsx"
}

func GET() string { return "ok" }
`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Analyze(entry)
	if err == nil {
		t.Fatal("expected missing client entry error")
	}
	goxErr, ok := err.(*goxerrors.GoxError)
	if !ok {
		t.Fatalf("expected *goxerrors.GoxError, got %T: %v", err, err)
	}
	if goxErr.Kind != "missing-client-entry" {
		t.Fatalf("expected missing-client-entry, got %q: %v", goxErr.Kind, err)
	}
	if goxErr.Line != 4 {
		t.Fatalf("expected error on src line 4, got %d", goxErr.Line)
	}
}

func TestCompileDoesNotWriteClientManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeProjectManifest(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".build", "generated", "go"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "client", "react"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "client", "react"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "client", "react", "menu.jsx"), []byte("export default function Menu() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "index.gox")
	if err := os.WriteFile(entry, []byte(`module main

client menu react {
	src: "menu.jsx"
	dev: true
}

func GET() string { return "ok" }
`), 0644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, ".build", "generated", "go", "index.go")

	result, err := Compile(entry, outPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, ".build", "client", "manifest.json")
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("manifest should not be written, stat err=%v", err)
	}
	legacyViteConfigPath := filepath.Join(dir, ".build", "client", "react", "menu.jsx", "vite", "vite.config.mjs")
	if _, err := os.Stat(legacyViteConfigPath); !os.IsNotExist(err) {
		t.Fatalf("compile should not write legacy per-entry vite config, stat err=%v", err)
	}
	if len(result.ClientComponents) != 1 {
		t.Fatalf("expected 1 client component, got %+v", result.ClientComponents)
	}
	client := result.ClientComponents[0]
	if client.Entry != "menu.jsx" || client.Path != "client/react/menu.jsx" || client.Mode != "dev" {
		t.Fatalf("unexpected compile client metadata: %+v", client)
	}
	wantOutput := filepath.ToSlash(filepath.Join(".build", "client", "react", "dev"))
	if client.OutputDir != wantOutput {
		t.Fatalf("output dir=%q, want %q", client.OutputDir, wantOutput)
	}
}

func TestAnalyzeClientEntryResolutionUnderClientRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"client/menu.jsx", "client/react/menu.jsx"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte("export default function Menu() {}\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "without client prefix", src: "menu.jsx", want: "client/react/menu.jsx"},
		{name: "with framework path", src: "react/menu.jsx", want: "client/react/menu.jsx"},
		{name: "with client prefix", src: "client/menu.jsx", want: "client/react/menu.jsx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "_")+".gox")
			if err := os.WriteFile(entry, []byte(`module main

client menu react {
	src: "`+tt.src+`"
}

func GET() string { return "ok" }
`), 0644); err != nil {
				t.Fatal(err)
			}
			result, err := Analyze(entry)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.ClientComponents) != 1 {
				t.Fatalf("expected 1 client component, got %+v", result.ClientComponents)
			}
			client := result.ClientComponents[0]
			if client.Path != tt.want {
				t.Fatalf("path=%q, want %q; client=%+v", client.Path, tt.want, client)
			}
		})
	}
}

func TestAnalyzeRejectsClientEntryOutsideClientRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "index.gox")
	if err := os.WriteFile(entry, []byte(`module main

client menu react {
	src: "../menu.jsx"
}

func GET() string { return "ok" }
`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Analyze(entry)
	if err == nil {
		t.Fatal("expected invalid client entry path error")
	}
	goxErr, ok := err.(*goxerrors.GoxError)
	if !ok {
		t.Fatalf("expected *goxerrors.GoxError, got %T: %v", err, err)
	}
	if goxErr.Kind != "invalid-client-entry-path" {
		t.Fatalf("expected invalid-client-entry-path, got %q: %v", goxErr.Kind, err)
	}
}

func TestClientComponentMapsRuntimeAttrsToDataAttrs(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

client menu react {
    src: "client/menu.jsx"
}

func GET(ctx *gox.Context) gox.Page {
	return <@menu hydrate="interaction" transition="fade" isolate title="Menu" count={{10}} />
}
`

	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), mod.File); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`[]byte(" data-hydrate=\"interaction\" data-transition=\"fade\" data-isolate=\"true\"")`,
		`gox.WriteClientPropsAttribute(ctx.Writer, map[string]any{"title": "Menu", "count": 10})`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("compiled template missing %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{` hydrate=`, ` transition=`, ` isolate=`} {
		if strings.Contains(out, bad) {
			t.Fatalf("compiled template leaked non-data runtime attr %q:\n%s", bad, out)
		}
	}
}

func TestClientStaticPropsArePrecompiledWithoutMap(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

client menu react { src: "menu/menu.jsx" }

func GET(ctx *gox.Context) gox.Page {
	return <><head></head><@menu hydrate="hover" myattribute="myvalor" isolate /></>
}
`
	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), mod.File); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `gox.WriteClientIsland(ctx, "pages/index.gox#menu#react#menu/menu.jsx#prod"`) {
		t.Fatalf("missing stable client call:\n%s", out)
	}
	if !strings.Contains(out, `data-props=\"{&#34;myattribute&#34;:&#34;myvalor&#34;}\"`) {
		t.Fatalf("static data-props was not precompiled:\n%s", out)
	}
	if strings.Contains(out, `map[string]any`) {
		t.Fatalf("static props must not allocate a map:\n%s", out)
	}
	if !strings.Contains(out, `gox.WriteClientRuntime(ctx)`) {
		t.Fatalf("client runtime slot missing before head close:\n%s", out)
	}
}

func TestClientBuildKeyNormalizesSourceAndMode(t *testing.T) {
	key := ClientComponentKey("React", "./menu/menu.jsx", true)
	if key != "react#menu/menu.jsx#dev" {
		t.Fatalf("unexpected client component key: %q", key)
	}

	prod := ClientComponentKey("react", "menu/menu.jsx", false)
	if prod != "react#menu/menu.jsx#prod" {
		t.Fatalf("unexpected prod client component key: %q", prod)
	}
}

func TestClientDeclarationKeyIncludesModuleNameFrameworkSourceAndMode(t *testing.T) {
	home := ClientDeclarationKey("pages/home.gox", "Menu", "React", "./menu/menu.jsx", true)
	dashboard := ClientDeclarationKey("pages/dashboard.gox", "Menu", "React", "./menu/menu.jsx", true)
	if home == dashboard {
		t.Fatalf("declarations in different modules must not share an identity: %q", home)
	}
	if want := "pages/home.gox#Menu#react#menu/menu.jsx#dev"; home != want {
		t.Fatalf("ClientDeclarationKey() = %q, want %q", home, want)
	}
	for _, changed := range []string{
		ClientDeclarationKey("pages/home.gox", "Header", "React", "./menu/menu.jsx", true),
		ClientDeclarationKey("pages/home.gox", "Menu", "Vue", "./menu/menu.jsx", true),
		ClientDeclarationKey("pages/home.gox", "Menu", "React", "./other/menu.jsx", true),
		ClientDeclarationKey("pages/home.gox", "Menu", "React", "./menu/menu.jsx", false),
	} {
		if changed == home {
			t.Fatalf("changed declaration unexpectedly reused identity %q", home)
		}
	}
}

func TestClientHydrateAcceptsDynamicExpressionAndValidatesStaticValues(t *testing.T) {
	dynamic := `module main

import "github.com/gox-lang/gox"

client menu react { src: "menu/menu.jsx" }

func GET(ctx *gox.Context) gox.Page {
	mode := "hover"
	return <@menu hydrate={{mode}} />
}
`
	mod, err := ParseModule("pages/index.gox", dynamic)
	if err != nil {
		t.Fatalf("dynamic hydrate should compile: %v", err)
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), mod.File); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `WriteTextString(mode)`) {
		t.Fatalf("dynamic hydrate was not emitted as a runtime attribute:\n%s", buf.String())
	}

	invalid := strings.Replace(dynamic, `hydrate={{mode}}`, `hydrate="invalid"`, 1)
	_, err = ParseModule("pages/index.gox", invalid)
	if err == nil {
		t.Fatal("expected invalid static hydrate to fail")
	}
	goxErr, ok := err.(*goxerrors.GoxError)
	if !ok || goxErr.Kind != "invalid-client-hydrate" {
		t.Fatalf("expected invalid-client-hydrate, got %T: %v", err, err)
	}
}

func TestClientIsolateAcceptsDynamicExpressionAndValidatesStaticValues(t *testing.T) {
	dynamic := `module main

import "github.com/gox-lang/gox"

client menu react { src: "menu/menu.jsx" }

func GET(ctx *gox.Context) gox.Page {
	isolated := true
	return <@menu isolate={{isolated}} />
}
`
	mod, err := ParseModule("pages/index.gox", dynamic)
	if err != nil {
		t.Fatalf("dynamic isolate should compile: %v", err)
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), mod.File); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `if isolated`) || !strings.Contains(out, `gox.WriteClientIslandDataAssets(ctx)`) {
		t.Fatalf("dynamic isolate did not emit conditional isolate + data-assets:\n%s", out)
	}

	for _, value := range []string{"yes", "1", "invalid"} {
		invalid := strings.Replace(dynamic, `isolate={{isolated}}`, `isolate="`+value+`"`, 1)
		_, err = ParseModule("pages/index.gox", invalid)
		if err == nil {
			t.Fatalf("expected isolate=%q to fail", value)
		}
		goxErr, ok := err.(*goxerrors.GoxError)
		if !ok || goxErr.Kind != "invalid-client-isolate" {
			t.Fatalf("expected invalid-client-isolate for %q, got %T: %v", value, err, err)
		}
	}
}

func TestClientPropsAttributeIsCollectedIntoDataProps(t *testing.T) {
	staticSource := `module main

import "github.com/gox-lang/gox"

client menu react { src: "menu/menu.jsx" }

func GET(ctx *gox.Context) gox.Page {
	return <@menu props="menu-value" />
}
`
	mod, err := ParseModule("pages/index.gox", staticSource)
	if err != nil {
		t.Fatalf("static props attribute should compile: %v", err)
	}
	var staticBuf bytes.Buffer
	if err := format.Node(&staticBuf, token.NewFileSet(), mod.File); err != nil {
		t.Fatal(err)
	}
	staticOutput := staticBuf.String()
	if !strings.Contains(staticOutput, `data-props=\"{&#34;props&#34;:&#34;menu-value&#34;}\"`) {
		t.Fatalf("props was not collected into static data-props:\n%s", staticOutput)
	}
	if strings.Contains(staticOutput, `reserved-client-attribute`) {
		t.Fatalf("props must not be treated as reserved:\n%s", staticOutput)
	}

	dynamicSource := `module main

import "github.com/gox-lang/gox"

client menu react { src: "menu/menu.jsx" }

func GET(ctx *gox.Context) gox.Page {
	value := "menu-value"
	return <@menu props={{value}} />
}
`
	mod, err = ParseModule("pages/index.gox", dynamicSource)
	if err != nil {
		t.Fatalf("dynamic props attribute should compile: %v", err)
	}
	var dynamicBuf bytes.Buffer
	if err := format.Node(&dynamicBuf, token.NewFileSet(), mod.File); err != nil {
		t.Fatal(err)
	}
	dynamicOutput := dynamicBuf.String()
	if !strings.Contains(dynamicOutput, `gox.WriteClientPropsAttribute(ctx.Writer, map[string]any{"props": value})`) {
		t.Fatalf("dynamic props was not collected into data-props:\n%s", dynamicOutput)
	}
}

func TestClientDeclarationRejectsFrameworkNotSupportedYet(t *testing.T) {
	src := `module main

client Menu gox {
	src: "menu.gox"
}
`
	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected gox client framework to fail")
	}
	if !strings.Contains(err.Error(), `client framework "gox" is not supported yet`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientDeclarationRejectsRemovedWasmFramework(t *testing.T) {
	src := `module main

client Menu wasm {
	src: "menu.wasm"
}
`
	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected wasm client framework to fail")
	}
	if !strings.Contains(err.Error(), `unsupported client framework "wasm"`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "wasm") && strings.Contains(err.Error(), "Supported frameworks:") && strings.Contains(err.Error(), "gox") {
		t.Fatalf("supported framework hint must not advertise unavailable frameworks: %v", err)
	}
}

func TestClientDeclarationRejectsDuplicateProperties(t *testing.T) {
	for _, property := range []string{"src", "dev"} {
		t.Run(property, func(t *testing.T) {
			body := "\tsrc: \"menu.jsx\"\n\tdev: true\n"
			if property == "src" {
				body += "\tsrc: \"other.jsx\"\n"
			} else {
				body += "\tdev: false\n"
			}
			src := "module main\n\nclient Menu react {\n" + body + "}\n"
			_, err := ParseModule("pages/index.gox", src)
			if err == nil {
				t.Fatalf("expected duplicate %s property to fail", property)
			}
			if !strings.Contains(err.Error(), `duplicate client property "`+property+`"`) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestClientDeclarationRequiresLowercaseFrameworkName(t *testing.T) {
	for _, framework := range []string{"React", "REACT", "Preact", "Solid", "Vue", "Svelte", "Gox"} {
		t.Run(framework, func(t *testing.T) {
			src := "module main\n\nclient Menu " + framework + " {\n\tsrc: \"menu.jsx\"\n}\n"
			_, err := ParseModule("pages/index.gox", src)
			if err == nil {
				t.Fatalf("expected framework %q to fail", framework)
			}
			if !strings.Contains(err.Error(), "must be lowercase") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestClientDeclarationRequiresCanonicalSrcString(t *testing.T) {
	src := "module main\n\nclient Menu react {\n\tsrc: `menu.jsx`\n}\n"
	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected raw string src to fail")
	}
	if !strings.Contains(err.Error(), "client src must be a non-empty string") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientDeclarationRejectsGoKeywordName(t *testing.T) {
	for _, name := range []string{"func", "type", "var", "map", "range"} {
		t.Run(name, func(t *testing.T) {
			src := "module main\n\nclient " + name + " react {\n\tsrc: \"menu.jsx\"\n}\n"
			_, err := ParseModule("pages/index.gox", src)
			if err == nil {
				t.Fatalf("expected keyword name %q to fail", name)
			}
			if !strings.Contains(err.Error(), "invalid client component name") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
