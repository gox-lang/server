package gox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestPhase3SemanticMarkerIdentityUsesOwningImportPath(t *testing.T) {
	file := phase3ParseGoFile(t, `package main

import (
	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

type Component interface{}
type Page interface{}
type Action interface{}
type WebSocket interface{}
type Channel interface{}

func SDKPage() gox.Page { return nil }
func SDKComponent() gox.Component { return nil }
func SDKAction() gox.Action { return nil }
func SDKWebSocket() gox.WebSocket { return nil }
func SDKChannel() gox.Channel { return gox.Channel{} }
func SDKConfig() server.Config { return server.Config{} }

func LocalPage() Page { return nil }
func LocalComponent() Component { return nil }
func LocalAction() Action { return nil }
func LocalWebSocket() WebSocket { return nil }
func LocalChannel() Channel { return nil }

func OrdinaryHTML() gox.HTML { return "" }
func OrdinaryFile() gox.File { return gox.File{} }
`)

	want := map[string]FuncKind{
		"SDKPage":        FuncPage,
		"SDKComponent":   FuncComponent,
		"SDKAction":      FuncAction,
		"SDKWebSocket":   FuncWebSocket,
		"SDKChannel":     FuncChannel,
		"SDKConfig":      FuncConfig,
		"LocalPage":      FuncNormal,
		"LocalComponent": FuncNormal,
		"LocalAction":    FuncNormal,
		"LocalWebSocket": FuncNormal,
		"LocalChannel":   FuncNormal,
		"OrdinaryHTML":   FuncNormal,
		"OrdinaryFile":   FuncNormal,
	}

	seen := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		expected, ok := want[fn.Name.Name]
		if !ok {
			continue
		}
		seen[fn.Name.Name] = true
		if got := detectFuncKindFromReturn(file, fn); got != expected {
			t.Fatalf("%s classified as %q, want %q", fn.Name.Name, got, expected)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Fatalf("fixture function %s was not parsed", name)
		}
	}
}

func TestPhase3SemanticLoweringIgnoresLocalMarkerHomonyms(t *testing.T) {
	file := phase3ParseGoFile(t, `package main

type Component interface{}
type Page interface{}

func LocalComponent() Component { return nil }
func LocalPage() Page { return nil }
`)

	if lowered := lowerSemanticGoxFunctions(file, nil); len(lowered) != 0 {
		t.Fatalf("local marker homonyms must not be semantically lowered: %#v", lowered)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || (fn.Name.Name != "LocalComponent" && fn.Name.Name != "LocalPage") {
			continue
		}
		if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
			t.Fatalf("%s result signature was unexpectedly rewritten", fn.Name.Name)
		}
	}
}

func TestPhase3RuntimeTransformIgnoresLocalMarkerHomonyms(t *testing.T) {
	file := phase3ParseGoFile(t, `package main

type Component interface{}
type Page interface{}

func LocalComponent() Component { return nil }
func LocalPage() Page { return nil }
`)

	if err := TransformGoxFunctionsWithOrigins(file, nil); err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || (fn.Name.Name != "LocalComponent" && fn.Name.Name != "LocalPage") {
			continue
		}
		if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
			t.Fatalf("%s result signature was unexpectedly stripped", fn.Name.Name)
		}
		if fn.Type.Params == nil || len(fn.Type.Params.List) != 0 {
			t.Fatalf("%s parameters were unexpectedly rewritten", fn.Name.Name)
		}
	}
}

func phase3ParseGoFile(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "phase3.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return file
}
