package gox

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Expectations here are the language contract: absent children select the
// fallback, supplied children replace it, and component props remain distinct.
func TestComponentChildrenRuntimeContract(t *testing.T) {
	const source = `module index
import "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page { return <main /> }
func Panel(title string, children gox.Component) gox.Component {
 return <div><h1>{{ title }}</h1><&children><p>fallback</p></&children></div>
}
func Empty(children gox.Component) gox.Component { return <span><&children /></span> }
func Props(header gox.Component, children gox.Component) gox.Component {
 return <article><&header /><&children /></article>
}
func Pair(left string, right string) gox.Component { return <p>{{ left }}:{{ right }}</p> }
func Example() gox.Component {
 heading := <h2>Heading</h2>
 return <><&Panel title="Absent" /><&Panel title="Present"><p>Body</p></&Panel><&Empty /><&Props header={{ heading }}><b>Child</b></&Props><&Pair right="R" left="L" /></>
}
`
	mod, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	phase1CopyModuleFiles(t, root)
	if err := WriteBundle(bundle, filepath.Join(root, "generated.go")); err != nil {
		t.Fatal(err)
	}
	phase1WriteFile(t, filepath.Join(root, "render_test.go"), `package main
import ("bytes"; "html"; "testing"; "github.com/gox-lang/gox")
type outputWriter struct { gox.Writer; output bytes.Buffer }
func (w *outputWriter) Write(p []byte) (int, error) { return w.output.Write(p) }
func (w *outputWriter) WriteTextString(s string) { w.output.WriteString(html.EscapeString(s)) }
func TestRender(t *testing.T) {
 w := &outputWriter{}
 GoxPage.Example(w)
 want := "<div><h1>Absent</h1><p>fallback</p></div><div><h1>Present</h1><p>Body</p></div><span></span><article><h2>Heading</h2><b>Child</b></article><p>L:R</p>"
 if got := w.output.String(); got != want { t.Fatalf("HTML = %q, want %q", got, want) }
}
`)
	cmd := exec.Command("go", "test", "-mod=readonly", "-count=1", ".")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated component execution: %v\n%s", err, output)
	}
}

func TestComponentWithoutChildrenRejectsBlockSyntax(t *testing.T) {
	for _, invocation := range []string{`<&Panel></&Panel>`, `<&Panel><p>Body</p></&Panel>`} {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Panel() gox.Component { return <div /> }
func GET(ctx *gox.Context) gox.Page { return `+invocation+` }
`)
		if err == nil || !strings.Contains(err.Error(), "does not accept children") {
			t.Fatalf("%s: %v", invocation, err)
		}
	}
}
