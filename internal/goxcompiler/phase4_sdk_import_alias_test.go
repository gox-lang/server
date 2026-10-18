package gox

import (
	"go/ast"
	"testing"
)

func TestPhase4SemanticSDKAliasCanonicalizesOnlyPackageReferences(t *testing.T) {
	file := phase3ParseGoFile(t, `package main

import gx "github.com/gox-lang/gox"

func GET(ctx *gx.Context) gx.Page { return nil }

func localShadow() int {
	gx := struct{ Value int }{Value: 7}
	return gx.Value
}
`)

	canonicalizeGoxSDKRuntimeImport(file)

	if len(file.Imports) != 1 || file.Imports[0].Name != nil {
		t.Fatalf("SDK runtime import alias was not canonicalized: %#v", file.Imports)
	}

	var get *ast.FuncDecl
	var localSelector *ast.SelectorExpr
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch fn.Name.Name {
		case "GET":
			get = fn
		case "localShadow":
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok || sel.Sel == nil || sel.Sel.Name != "Value" {
					return true
				}
				localSelector = sel
				return false
			})
		}
	}
	if get == nil {
		t.Fatal("GET fixture was not parsed")
	}

	param := get.Type.Params.List[0].Type.(*ast.StarExpr).X.(*ast.SelectorExpr)
	if qualifier := param.X.(*ast.Ident).Name; qualifier != "gox" {
		t.Fatalf("aliased Context qualifier = %q, want gox", qualifier)
	}
	result := get.Type.Results.List[0].Type.(*ast.SelectorExpr)
	if qualifier := result.X.(*ast.Ident).Name; qualifier != "gox" {
		t.Fatalf("aliased Page qualifier = %q, want gox", qualifier)
	}

	if localSelector == nil {
		t.Fatal("local gx.Value selector was not found")
	}
	if qualifier := localSelector.X.(*ast.Ident).Name; qualifier != "gx" {
		t.Fatalf("local alias shadow was rewritten to %q, want gx", qualifier)
	}
}

func TestPhase4SemanticSDKAliasesReachModuleContracts(t *testing.T) {
	mod, err := ParseModule("pages/index.gox", `module index

import gx "github.com/gox-lang/gox"

func GET(ctx *gx.Context) gx.Page {
	return <&Card />
}

func Card() gx.Component {
	return <article>Card</article>
}
`)
	if err != nil {
		t.Fatalf("SDK alias rejected by module contracts: %v", err)
	}
	if got := phase4FuncKind(mod, "GET"); got != FuncPage {
		t.Fatalf("aliased Page classified as %q, want %q", got, FuncPage)
	}
	if got := phase4FuncKind(mod, "Card"); got != FuncComponent {
		t.Fatalf("aliased Component classified as %q, want %q", got, FuncComponent)
	}

	config, err := ParseModule("app/config.gox", `module app
import srv "github.com/gox-lang/gox/server"
func SetGoxConfig() srv.Config { return srv.Config{} }
`)
	if err != nil {
		t.Fatalf("server SDK alias rejected by module contracts: %v", err)
	}
	if got := phase4FuncKind(config, "SetGoxConfig"); got != FuncConfig {
		t.Fatalf("aliased server.Config classified as %q, want %q", got, FuncConfig)
	}
}

func phase4FuncKind(mod *Module, name string) FuncKind {
	if mod == nil {
		return FuncNormal
	}
	for _, fn := range mod.Funcs {
		if fn.Name == name {
			return fn.Kind
		}
	}
	return FuncNormal
}

func TestPhase4SemanticCanonicalQualifierWithoutImportRemainsToolingCompatible(t *testing.T) {
	file := phase3ParseGoFile(t, `package main

func Card() gox.Component { return nil }
func Index() gox.Page { return nil }
`)

	want := map[string]FuncKind{
		"Card":  FuncComponent,
		"Index": FuncPage,
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		expected, ok := want[fn.Name.Name]
		if !ok {
			continue
		}
		if got := detectFuncKindFromReturn(file, fn); got != expected {
			t.Fatalf("%s canonical qualifier classified as %q, want %q", fn.Name.Name, got, expected)
		}
	}
}

func TestPhase4SemanticExplicitForeignGoxBindingWinsOverImplicitNamespace(t *testing.T) {
	file := phase3ParseGoFile(t, `package main

import gox "example.com/not-the-gox-sdk"

func Card() gox.Component { return nil }
`)

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Card" {
			continue
		}
		if got := detectFuncKindFromReturn(file, fn); got != FuncNormal {
			t.Fatalf("foreign gox binding classified as %q, want %q", got, FuncNormal)
		}
		return
	}
	t.Fatal("Card fixture was not parsed")
}

func TestPhase4SemanticProjectionFixtureCompatibilityDoesNotRelaxModuleIdentity(t *testing.T) {
	projection, err := BuildSemanticProjection("pages/index.gox", `module index

type Page interface{}
func LocalPage() Page { return nil }
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := semanticProjectionReturnModes(projection)["Page"]; got != SemanticGoxReturnFlexibleOptional {
		t.Fatalf("synthetic projection Page mode = %q, want %q", got, SemanticGoxReturnFlexibleOptional)
	}

	mod, err := ParseModule("pages/index.gox", `module index

type Page interface{}
func LocalPage() Page { return nil }
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := phase4FuncKind(mod, "LocalPage"); got != FuncNormal {
		t.Fatalf("local Page homonym classified as %q, want %q", got, FuncNormal)
	}
}

func TestPhase4SemanticProjectionCompatibilityPreservesImportedModulePackageName(t *testing.T) {
	projection, err := buildImportedSemanticProjectionForSource("pages/users.gox", `module users

func Count() int { return 1 }
`)
	if err != nil {
		t.Fatal(err)
	}
	if projection == nil || projection.File == nil || projection.File.Name == nil {
		t.Fatal("imported semantic projection has no parsed package")
	}
	if got := projection.File.Name.Name; got != "users" {
		t.Fatalf("imported semantic package = %q, want users", got)
	}
}
