package gox

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"strings"
	"testing"
)

func TestHTMLContentTypeIsExportedOutsideGeneratedHandler(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <h1>Hello Gox</h1>
}
`

	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	if err != nil {
		t.Fatal(err)
	}

	file := &ast.File{Name: ast.NewIdent("main"), Decls: bundle.Decls}
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), file); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, `var GoxHTTPContentTypes map[string]string = map[string]string{"GET": "text/html; charset=utf-8"}`) {
		t.Fatalf("compiled bundle does not export HTTP content type metadata:\n%s", out)
	}
	if strings.Contains(out, `ctx.Response.Headers.Set("Content-Type"`) {
		t.Fatalf("compiled handler still mutates Content-Type inside the handler:\n%s", out)
	}
	if !strings.Contains(out, `func GoxHTTPHandlerGET(ctx *gox.Context)`) {
		t.Fatalf("compiled bundle does not generate a named HTTP handler wrapper:\n%s", out)
	}
	if strings.Contains(out, `"GET": GoxPage.GET`) {
		t.Fatalf("compiled bundle still exports a method value in GoxHTTPHandlers:\n%s", out)
	}
}
