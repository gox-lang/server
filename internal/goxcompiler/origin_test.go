package gox

import (
	"go/ast"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/goxmap"
)

func TestParseModuleSeedsOriginsBeforeASTRewrites(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	name := "George"
	Image := <img alt={{name}}>
	return <main><&Image/></main>
}
`
	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	if mod.Origins == nil || mod.Origins.Len() == 0 {
		t.Fatal("module has no seeded AST origins")
	}
	if _, ok := mod.Origins.Get(mod.File.Name); ok {
		t.Fatal("generated package name unexpectedly has a GOX origin")
	}

	var george *ast.BasicLit
	var generatedWriter *ast.Ident
	ast.Inspect(mod.File, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.BasicLit:
			if value.Value == `"George"` {
				george = value
			}
		case *ast.Ident:
			if generatedWriter == nil && value.Name == "goxWriter" {
				generatedWriter = value
			}
		}
		return true
	})
	if george == nil || generatedWriter == nil {
		t.Fatalf("expected nodes were not found: George=%v goxWriter=%v", george != nil, generatedWriter != nil)
	}

	origin, ok := mod.Origins.Get(george)
	georgeOffset := strings.Index(src, `"George"`)
	if !ok || !origin.Exact() || origin.Source.URI != "pages/index.gox" || origin.Source.Range != (goxmap.Range{Start: georgeOffset, End: georgeOffset + len(`"George"`)}) {
		t.Fatalf("George origin = %#v, %v", origin, ok)
	}
	origin, ok = mod.Origins.Get(generatedWriter)
	templateOffset := strings.Index(src, "<img")
	if !ok || origin.Exact() || origin.Source.URI != "pages/index.gox" || origin.Source.Range.Start != templateOffset {
		t.Fatalf("generated writer origin = %#v, %v", origin, ok)
	}
}

func TestOriginTableInheritCreatesAnchor(t *testing.T) {
	table := NewOriginTable()
	source := ast.NewIdent("George")
	generated := ast.NewIdent("goxGeorge")
	table.Set(source, NodeOrigin{
		Source: goxmap.SourceRange{URI: "page.gox", Range: goxmap.Range{Start: 10, End: 16}},
		Kind:   goxmap.Exact,
	})
	if !table.Inherit(generated, source) {
		t.Fatal("origin was not inherited")
	}
	origin, ok := table.Get(generated)
	if !ok || origin.Kind != goxmap.Anchor || origin.Source.Range != (goxmap.Range{Start: 10, End: 16}) {
		t.Fatalf("inherited origin = %#v, %v", origin, ok)
	}
}

func TestOriginTableInheritTreePreservesExistingChildOrigin(t *testing.T) {
	table := NewOriginTable()
	source := ast.NewIdent("template")
	reused := ast.NewIdent("George")
	generated := &ast.CallExpr{Fun: ast.NewIdent("write"), Args: []ast.Expr{reused}}
	table.Set(source, NodeOrigin{
		Source: goxmap.SourceRange{URI: "page.gox", Range: goxmap.Range{Start: 20, End: 40}},
		Kind:   goxmap.Anchor,
	})
	table.Set(reused, NodeOrigin{
		Source: goxmap.SourceRange{URI: "page.gox", Range: goxmap.Range{Start: 30, End: 36}},
		Kind:   goxmap.Exact,
	})
	if !table.InheritTree(generated, source) {
		t.Fatal("tree origin was not inherited")
	}
	rootOrigin, ok := table.Get(generated)
	if !ok || rootOrigin.Kind != goxmap.Anchor || rootOrigin.Source.Range.Start != 20 {
		t.Fatalf("generated root origin = %#v, %v", rootOrigin, ok)
	}
	reusedOrigin, ok := table.Get(reused)
	if !ok || reusedOrigin.Kind != goxmap.Exact || reusedOrigin.Source.Range != (goxmap.Range{Start: 30, End: 36}) {
		t.Fatalf("reused child origin = %#v, %v", reusedOrigin, ok)
	}
}

func TestParseModulePropagatesOriginsThroughReturnRewrites(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	if ctx != nil {
		return <main>George</main>
	}
	return "George"
}
`
	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	var templateWrite *ast.SelectorExpr
	var flexibleWrite *ast.SelectorExpr
	var george *ast.BasicLit
	ast.Inspect(mod.File, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SelectorExpr:
			if value.Sel == nil {
				break
			}
			switch value.Sel.Name {
			case "Write":
				if templateWrite == nil {
					templateWrite = value
				}
			case "WriteTextString":
				if flexibleWrite == nil {
					flexibleWrite = value
				}
			}
		case *ast.BasicLit:
			if value.Value == `"George"` {
				george = value
			}
		}
		return true
	})
	if templateWrite == nil || flexibleWrite == nil || george == nil {
		t.Fatalf("expected rewritten nodes were not found: template=%v flexible=%v literal=%v", templateWrite != nil, flexibleWrite != nil, george != nil)
	}

	templateOrigin, ok := mod.Origins.Get(templateWrite)
	templateOffset := strings.Index(src, "<main>")
	if !ok || templateOrigin.Exact() || templateOrigin.Source.URI != "pages/index.gox" || templateOrigin.Source.Range.Start != templateOffset {
		t.Fatalf("template write origin = %#v, %v", templateOrigin, ok)
	}

	flexibleOrigin, ok := mod.Origins.Get(flexibleWrite)
	literalOffset := strings.LastIndex(src, `"George"`)
	literalRange := goxmap.Range{Start: literalOffset, End: literalOffset + len(`"George"`)}
	if !ok || flexibleOrigin.Exact() || flexibleOrigin.Source.URI != "pages/index.gox" || flexibleOrigin.Source.Range != literalRange {
		t.Fatalf("flexible write origin = %#v, %v", flexibleOrigin, ok)
	}

	literalOrigin, ok := mod.Origins.Get(george)
	if !ok || !literalOrigin.Exact() || literalOrigin.Source.URI != "pages/index.gox" || literalOrigin.Source.Range != literalRange {
		t.Fatalf("reused literal origin = %#v, %v", literalOrigin, ok)
	}
}

func TestParseModulePropagatesFunctionStructureOrigins(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <main>George</main>
}

func Save(name string) gox.Action {
	return name
}
`
	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	var page *ast.FuncDecl
	var action *ast.FuncDecl
	for _, decl := range mod.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		switch fn.Name.Name {
		case "GET":
			page = fn
		case GoxActionName(mod.Name, "Save"):
			action = fn
		}
	}
	if page == nil || action == nil {
		t.Fatalf("transformed functions were not found: page=%v action=%v", page != nil, action != nil)
	}

	assertAnchoredAt := func(label string, node ast.Node, offset int) {
		t.Helper()
		origin, ok := mod.Origins.Get(node)
		if !ok || origin.Exact() || origin.Source.URI != "pages/index.gox" || origin.Source.Range.Start != offset {
			t.Fatalf("%s origin = %#v, %v", label, origin, ok)
		}
	}

	pageNameOffset := strings.Index(src, "GET")
	assertAnchoredAt("page receiver", page.Recv, pageNameOffset)
	if page.Type.Params == nil || len(page.Type.Params.List) != 1 {
		t.Fatalf("page params = %#v", page.Type.Params)
	}
	pageReturnOffset := strings.Index(src, "gox.Page")
	assertAnchoredAt("page context", page.Type.Params.List[0], pageReturnOffset)

	actionNameOffset := strings.Index(src, "Save")
	assertAnchoredAt("action name", action.Name, actionNameOffset)
	assertAnchoredAt("action receiver", action.Recv, actionNameOffset)
	if action.Type.Params == nil || len(action.Type.Params.List) != 2 {
		t.Fatalf("action params = %#v", action.Type.Params)
	}
	actionReturnOffset := strings.Index(src, "gox.Action")
	assertAnchoredAt("action context", action.Type.Params.List[1], actionReturnOffset)

	nameOrigin, ok := mod.Origins.Get(action.Type.Params.List[0])
	nameOffset := strings.Index(src, "name string")
	if !ok || !nameOrigin.Exact() || nameOrigin.Source.URI != "pages/index.gox" || nameOrigin.Source.Range.Start != nameOffset {
		t.Fatalf("preserved action param origin = %#v, %v", nameOrigin, ok)
	}
}

func TestParseModulePropagatesBoundExpressionOrigins(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

var greeting = "George"

func Message() string {
	return greeting
}

func GET(ctx *gox.Context) gox.Page {
	message := Message()
	return <main>{{message}}</main>
}
`
	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	selectors := map[string]*ast.SelectorExpr{}
	ast.Inspect(mod.File, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if ok && receiver.Name == "m" {
			if selector.Sel.Name == "Message" || selector.Sel.Name == "greeting" {
				selectors[selector.Sel.Name] = selector
			}
		}
		return true
	})

	for name, offset := range map[string]int{
		"Message":  strings.LastIndex(src, "Message"),
		"greeting": strings.LastIndex(src, "greeting"),
	} {
		selector := selectors[name]
		if selector == nil {
			t.Fatalf("bound selector %s was not found", name)
		}
		selectorOrigin, ok := mod.Origins.Get(selector)
		if !ok || selectorOrigin.Exact() || selectorOrigin.Source.URI != "pages/index.gox" || selectorOrigin.Source.Range.Start != offset {
			t.Fatalf("%s selector origin = %#v, %v", name, selectorOrigin, ok)
		}
		identOrigin, ok := mod.Origins.Get(selector.Sel)
		if !ok || !identOrigin.Exact() || identOrigin.Source.URI != "pages/index.gox" || identOrigin.Source.Range != (goxmap.Range{Start: offset, End: offset + len(name)}) {
			t.Fatalf("%s identifier origin = %#v, %v", name, identOrigin, ok)
		}
	}
}
