package gox

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"strings"
	"testing"
)

func compileSocketRegistryHTMLSource(t *testing.T, source string) string {
	t.Helper()
	mod, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	if err != nil {
		t.Fatal(err)
	}
	file := &ast.File{Name: ast.NewIdent("main"), Decls: bundle.Decls}
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), file); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestSocketRegistryEmitHTMLFromPageActionAndComponent(t *testing.T) {
	generated := compileSocketRegistryHTMLSource(t, `module main
import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	socket := &gox.Socket{}
	socket.Emit("direct-page", <aside>Direct</aside>)
	gox.Sockets.Emit("page", <h1>Page</h1>)
	return <main>Response</main>
}

func Notify() gox.Action {
	gox.Sockets.Emit("action", <strong>Action</strong>)
	return nil
}

func Card(name string) gox.Component {
	gox.Sockets.Emit("component", <span>{{name}}</span>)
	return <article>{{name}}</article>
}
`)

	for _, call := range []string{
		`socket.Emit("direct-page", func(goxWriter gox.Writer)`,
		`gox.Sockets.Emit("page", func(goxWriter gox.Writer)`,
		`gox.Sockets.Emit("action", func(goxWriter gox.Writer)`,
		`gox.Sockets.Emit("component", func(goxWriter gox.Writer)`,
	} {
		if !strings.Contains(generated, call) {
			t.Fatalf("HTML emission was not compiled as an independent renderable %q:\n%s", call, generated)
		}
	}
}

func TestSocketRegistryEmitComponentReferenceUsesEmissionWriter(t *testing.T) {
	generated := compileSocketRegistryHTMLSource(t, `module main
import "github.com/gox-lang/gox"

func Badge(text string) gox.Component {
	return <span>{{text}}</span>
}

func GET(ctx *gox.Context) gox.Page {
	gox.Sockets.Emit("badge", <&Badge text="ready"/>)
	return <main>Response</main>
}
`)
	if !strings.Contains(generated, `gox.Sockets.Emit("badge", func(goxWriter gox.Writer)`) {
		t.Fatalf("component emission did not receive a private writer:\n%s", generated)
	}
	if !strings.Contains(generated, `m.Badge(goxWriter, "ready")`) {
		t.Fatalf("component emission used the wrong writer:\n%s", generated)
	}
}
