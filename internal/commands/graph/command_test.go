package graph

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/cli"
	gox "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/project"

	"github.com/gox-lang/gox/server"
)

func TestResolveGraphEntryRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()

	invalid := []string{
		"",
		"index",
		"index.go",
		"../pages/index.gox",
		"pages/../outside.gox",
		filepath.Join(root, "pages", "index.gox"),
	}

	for _, input := range invalid {
		if _, _, err := resolveGraphEntry(root, input); err == nil {
			t.Fatalf("resolveGraphEntry(%q) error=nil, want error", input)
		}
	}
}

func TestResolveGraphEntryUsesProjectRelativePath(t *testing.T) {
	root := t.TempDir()
	rel, abs, err := resolveGraphEntry(root, "pages/index.gox")
	if err != nil {
		t.Fatal(err)
	}
	if rel != "pages/index.gox" {
		t.Fatalf("rel=%q", rel)
	}
	wantAbs := filepath.Join(root, "pages", "index.gox")
	if abs != wantAbs {
		t.Fatalf("abs=%q, want %q", abs, wantAbs)
	}
}

func TestCommandRunPrintsGraph(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".build", "app", "pages", "components", "client", "tests", "public"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "config.gox"), []byte("module app\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pages", "index.gox"), []byte(`module main

import (
	"fmt"
	"../components/title.gox"
)

func GET() string { return title.Value() }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "components", "title.gox"), []byte(`module title

func Value() string { return "title" }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "client", "react"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "client", "react", "menu.jsx"), []byte("export default function Menu() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := New()
	err := cmd.Run(cli.Context{WorkDir: root, Stdout: &out, Stderr: &out}, []string{"pages/index.gox"})
	if err != nil {
		t.Fatal(err)
	}

	text := out.String()
	for _, want := range []string{
		"GRAPH pages/index.gox",
		"pages/index.gox",
		`"fmt"`,
		`"components/title.gox"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
	for _, notWant := range []string{"entry\n", "graph\n", "Summary"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("non-verbose output should not include %q:\n%s", notWant, text)
		}
	}
	if strings.Contains(text, "package") || strings.Contains(text, "module   ") {
		t.Fatalf("non-verbose output should show compact paths only:\n%s", text)
	}
}

func TestParseGraphArgsDefaultIncludesClientDependencies(t *testing.T) {
	opts, err := parseGraphArgs([]string{"pages/index.gox"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.EntryPath != "pages/index.gox" || !opts.Filter.Go || !opts.Filter.Gox || !opts.Filter.Client || !opts.Filter.ClientDeps {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestParseGraphArgsRouteCombinesWithVerboseAndFilter(t *testing.T) {
	opts, err := parseGraphArgs([]string{"-v", "--route", "/dashboard", "--filter", "gx"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Route || !opts.Verbose || opts.EntryPath != "/dashboard" || !opts.Filter.Go || !opts.Filter.Gox || opts.Filter.Client {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestRunGraphRouteSilentlyRestoresLogs(t *testing.T) {
	previous := logs.Enabled()
	t.Cleanup(func() { logs.SetEnabled(previous) })

	logs.SetEnabled(true)
	err := runGraphRouteSilently(func() error {
		if logs.Enabled() {
			t.Fatal("logs must be disabled while loading graph route configuration")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !logs.Enabled() {
		t.Fatal("logs were not restored after graph route configuration loaded")
	}
}

func TestRunGraphRouteSilentlyRestoresDisabledStateAfterError(t *testing.T) {
	previous := logs.Enabled()
	t.Cleanup(func() { logs.SetEnabled(previous) })

	logs.SetEnabled(false)
	wantErr := fmt.Errorf("load failed")
	err := runGraphRouteSilently(func() error {
		if logs.Enabled() {
			t.Fatal("logs must remain disabled while loading graph route configuration")
		}
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if logs.Enabled() {
		t.Fatal("logs were enabled after graph route configuration failed")
	}
}

func TestValidateGraphRoute(t *testing.T) {
	valid := map[string]string{"/": "/", "/dashboard": "/dashboard", "/dashboard/": "/dashboard", "/users/George": "/users/George"}
	for input, want := range valid {
		got, err := validateGraphRoute(input)
		if err != nil || got != want {
			t.Errorf("validateGraphRoute(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "dashboard", " /dashboard", "/dashboard settings", "/dashboard?tab=users", "/dashboard#main", `/dashboard\\settings`, "/dashboard//settings", "/dashboard/../admin", "/dashboard/./settings", "/.private", "/users%2Fadmin", "https://example.com/dashboard"} {
		if _, err := validateGraphRoute(input); err == nil {
			t.Errorf("validateGraphRoute(%q) error=nil", input)
		}
	}
}

func TestResolveGraphRouteAppliesPreRoute(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"pages/index.gox", "pages/dashboard/profile.gox"} {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("module page\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := &project.Project{RootDir: root}
	config := server.RoutesConfig{Routes: map[string]string{
		"/account":  "/dashboard/profile",
		"/user/:id": "/dashboard/profile",
		"/assets":   "/public/app.js",
	}}

	for _, route := range []string{"/account", "/user/123"} {
		entry, err := resolveGraphRoute(p, route, config)
		if err != nil {
			t.Fatalf("resolveGraphRoute(%q): %v", route, err)
		}
		if entry.File != "pages/dashboard/profile.gox" {
			t.Errorf("resolveGraphRoute(%q) = %#v", route, entry)
		}
	}
	entry, err := resolveGraphRoute(p, "/", config)
	if err != nil || entry.File != "pages/index.gox" {
		t.Fatalf("resolveGraphRoute(/) = %#v, %v", entry, err)
	}
	if _, err := resolveGraphRoute(p, "/missing", config); err == nil || !strings.Contains(err.Error(), "route not found") {
		t.Fatalf("missing route error = %v", err)
	}
	if _, err := resolveGraphRoute(p, "/assets", config); err == nil || !strings.Contains(err.Error(), "static target") {
		t.Fatalf("static route error = %v", err)
	}
}

func TestParseGraphArgsVerboseAndFilter(t *testing.T) {
	opts, err := parseGraphArgs([]string{"pages/index.gox", "-v", "--filter", "xc"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.EntryPath != "pages/index.gox" || !opts.Verbose || opts.Filter.Go || !opts.Filter.Gox || !opts.Filter.Client || opts.Filter.ClientDeps {
		t.Fatalf("opts=%+v", opts)
	}

	opts, err = parseGraphArgs([]string{"--verbose", "--filter=c", "pages/index.gox"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.EntryPath != "pages/index.gox" || !opts.Verbose || opts.Filter.Go || opts.Filter.Gox || !opts.Filter.Client {
		t.Fatalf("opts=%+v", opts)
	}

	opts, err = parseGraphArgs([]string{"pages/index.gox", "--filter", "d"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Filter.Go || opts.Filter.Gox || !opts.Filter.Client || !opts.Filter.ClientDeps {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestParseGraphArgsRejectsInvalidFilter(t *testing.T) {
	if _, err := parseGraphArgs([]string{"pages/index.gox", "--filter", "z"}); err == nil {
		t.Fatal("expected invalid filter error")
	}
}

func TestParseGraphArgsRejectsReversedDashConvention(t *testing.T) {
	for _, args := range [][]string{{"pages/index.gox", "--v"}, {"-verbose", "pages/index.gox"}, {"pages/index.gox", "-filter", "g"}} {
		if _, err := parseGraphArgs(args); err == nil {
			t.Fatalf("arguments %q were accepted", args)
		}
	}
}

func TestParseGraphArgsSupportsGNUEqualsSyntax(t *testing.T) {
	opts, err := parseGraphArgs([]string{"pages/index.gox", "--filter=gx"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Filter.Go || !opts.Filter.Gox || opts.Filter.Client || opts.Filter.ClientDeps {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestParseGraphArgsRejectsUnknownOptions(t *testing.T) {
	for _, args := range [][]string{{"pages/index.gox", "--helplplp"}, {"pages/index.gox", "--unknown"}, {"pages/index.gox", "-x"}} {
		if _, err := parseGraphArgs(args); err == nil {
			t.Fatalf("arguments %q were accepted", args)
		}
	}
}

func TestCommandRunVerbosePrintsGoPackages(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".build", "app", "pages", "components", "client", "tests", "public"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "config.gox"), []byte("module app\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pages", "index.gox"), []byte(`module main

import (
	"fmt"
	"../components/title.gox"
)

func GET() string { return fmt.Sprint(title.Value()) }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "components", "title.gox"), []byte(`module title

func Value() string { return "title" }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "client", "react"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "client", "react", "menu.jsx"), []byte("export default function Menu() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := New()
	err := cmd.Run(cli.Context{WorkDir: root, Stdout: &out, Stderr: &out}, []string{"pages/index.gox", "-v"})
	if err != nil {
		t.Fatal(err)
	}

	text := out.String()
	if !strings.Contains(text, `package  "fmt"`) {
		t.Fatalf("verbose output missing Go package:\n%s", text)
	}
	if !strings.Contains(text, `module   title`) || !strings.Contains(text, `"components/title.gox"`) {
		t.Fatalf("verbose output missing GoX module:\n%s", text)
	}
}

func TestFormatExpandsTransitiveLocalGoPackages(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "pages", "index.gox")
	result := &gox.CompileResult{
		EntryAbsPath: entry,
		Modules: []gox.CompileModule{
			{
				AbsPath:    entry,
				ModuleName: "index",
				IsEntry:    true,
				GoImports:  []string{"noti-milo/billing"},
			},
		},
		GoPackages: []gox.CompileGoPackage{
			{
				ImportPath: "noti-milo/billing",
				Imports:    []string{"noti-milo/database"},
				Files:      []string{filepath.Join(root, "billing", "billing.go")},
				Direct:     true,
			},
			{
				ImportPath: "noti-milo/database",
				Files:      []string{filepath.Join(root, "database", "database.go")},
			},
		},
	}

	text := Format(result, root, "pages/index.gox", true)
	ordered := []string{
		`package  "noti-milo/billing"`,
		`package  "noti-milo/database"`,
	}
	last := -1
	for _, value := range ordered {
		index := strings.Index(text, value)
		if index < 0 {
			t.Fatalf("graph output missing %q:\n%s", value, text)
		}
		if index <= last {
			t.Fatalf("graph output is not hierarchical at %q:\n%s", value, text)
		}
		last = index
	}
	for _, want := range []string{"Go      : 2", "Total   : 2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("graph summary missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "database/database.go") || strings.Contains(text, "billing/billing.go") {
		t.Fatalf("physical Go files must not appear as import graph nodes:\n%s", text)
	}
}

func TestFormatGoFilterHidesGoPackages(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "pages", "index.gox")
	result := &gox.CompileResult{
		EntryAbsPath: entry,
		Modules: []gox.CompileModule{{
			AbsPath: entry, ModuleName: "index", IsEntry: true,
			GoImports: []string{"noti-milo/billing"},
		}},
		GoPackages: []gox.CompileGoPackage{{
			ImportPath: "noti-milo/billing",
			Files:      []string{filepath.Join(root, "billing", "billing.go")},
		}},
	}

	text := FormatWithFilter(result, root, "pages/index.gox", true, graphFilter{Gox: true})
	if strings.Contains(text, "noti-milo/billing") || strings.Contains(text, "billing/billing.go") {
		t.Fatalf("Go-only graph data escaped the Go filter:\n%s", text)
	}
}

func TestCommandRunPrintsClientComponents(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".build", "app", "pages", "components", "client", "tests", "public"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "config.gox"), []byte("module app\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pages", "index.gox"), []byte(`module main

import (
	"fmt"
	"../components/title.gox"
)

client menu react {
	src: "client/menu.jsx"
	dev: true
}

func GET() string { return fmt.Sprint(title.Value()) }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "components", "title.gox"), []byte(`module title

func Value() string { return "title" }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "client", "react"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "client", "react", "menu.jsx"), []byte("export default function Menu() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := New()
	err := cmd.Run(cli.Context{WorkDir: root, Stdout: &out, Stderr: &out}, []string{"pages/index.gox", "-v"})
	if err != nil {
		t.Fatal(err)
	}

	text := out.String()
	for _, want := range []string{`Summary`, `Go      : 1`, `GoX     : 1`, `Client  : 1`, `Total   : 3`, `package  "fmt"`, `module   title`, `client   react`, `"client/react/menu.jsx"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestCommandRunFilterClientOnly(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".build", "app", "pages", "components", "client", "tests", "public"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "config.gox"), []byte("module app\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pages", "index.gox"), []byte(`module main

import (
	"fmt"
	"../components/title.gox"
)

client menu react {
	src: "client/menu.jsx"
}

func GET() string { return fmt.Sprint(title.Value()) }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "components", "title.gox"), []byte(`module title

func Value() string { return "title" }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "client", "react"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "client", "react", "menu.jsx"), []byte("export default function Menu() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := New()
	err := cmd.Run(cli.Context{WorkDir: root, Stdout: &out, Stderr: &out}, []string{"pages/index.gox", "--filter", "c"})
	if err != nil {
		t.Fatal(err)
	}

	text := out.String()
	if !strings.Contains(text, `"client/react/menu.jsx"`) {
		t.Fatalf("client-only output missing client path:\n%s", text)
	}
	for _, notWant := range []string{`"fmt"`, `"components/title.gox"`, `package`, `module   `} {
		if strings.Contains(text, notWant) {
			t.Fatalf("client-only output should not contain %q:\n%s", notWant, text)
		}
	}
}

func TestFormatWithClientDependencies(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	result := &gox.CompileResult{
		EntryAbsPath: filepath.Join(root, "pages", "index.gox"),
		Modules: []gox.CompileModule{
			{
				Path:               filepath.Join(root, "pages", "index.gox"),
				AbsPath:            filepath.Join(root, "pages", "index.gox"),
				ModuleName:         "main",
				DeclaredModuleName: "main",
				IsEntry:            true,
				Clients: []gox.CompileClientComponent{
					{Name: "menu", Framework: "react", Path: "client/react/menu/menu.jsx"},
				},
			},
		},
		ClientBundles: []gox.CompileClientBundle{
			{
				Name:      "menu",
				Framework: "react",
				Path:      "client/react/menu/menu.jsx",
				Graph: gox.CompileClientDependencyGraph{
					EntryFile: filepath.Join(root, "client", "react", "menu", "menu.jsx"),
					ModuleFiles: []string{
						filepath.Join(root, "client", "react", "menu", "menu.jsx"),
						filepath.Join(root, "client", "react", "menu", "messge.jsx"),
						filepath.Join(root, "client", "react", "menu", "menu.module.css"),
						filepath.Join(root, "client", "react", "menu", "logo.svg"),
						filepath.Join(root, "client", "react", "node_modules", "react", "index.js"),
					},
					Nodes: []gox.CompileClientDependencyGraphNode{
						{
							File: filepath.Join(root, "client", "react", "menu", "menu.jsx"),
							Imports: []string{
								filepath.Join(root, "client", "react", "menu", "messge.jsx"),
								filepath.Join(root, "client", "react", "menu", "menu.module.css"),
								filepath.Join(root, "client", "react", "node_modules", "react", "index.js"),
							},
						},
						{
							File: filepath.Join(root, "client", "react", "menu", "messge.jsx"),
							Imports: []string{
								filepath.Join(root, "client", "react", "menu", "logo.svg"),
							},
						},
					},
				},
			},
		},
	}

	filter, err := parseGraphFilter("cd")
	if err != nil {
		t.Fatal(err)
	}
	text := FormatWithFilter(result, root, "pages/index.gox", true, filter)
	for _, want := range []string{
		`client   react`,
		`.jsx    "client/react/menu/messge.jsx"`,
		`.svg    "client/react/menu/logo.svg"`,
		`.css    "client/react/menu/menu.module.css"`,
		`Deps    : 3`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, ".jsx    \"client/react/menu/messge.jsx\"") || !strings.Contains(text, "│  └─ .svg    \"client/react/menu/logo.svg\"") {
		t.Fatalf("client dependency graph should preserve nested import relationships:\n%s", text)
	}
	if strings.Contains(text, "node_modules") {
		t.Fatalf("client dependency graph should hide node_modules by default:\n%s", text)
	}
}

func TestParseGraphArgsSupportsOptionTerminator(t *testing.T) {
	opts, err := parseGraphArgs([]string{"--", "-page.gox"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.EntryPath != "-page.gox" {
		t.Fatalf("EntryPath = %q", opts.EntryPath)
	}
}
