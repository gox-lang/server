package gox

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gox-lang/server/internal/goxerrors"
)

const importedPanelSource = `module ui
import "github.com/gox-lang/gox"
func Panel(left string, right string, header gox.Component, children gox.Component) gox.Component {
 return <section><h1>{{ left }}:{{ right }}</h1><&header /><&children><p>fallback</p></&children></section>
}

func Plain(title string) gox.Component { return <p>{{ title }}</p> }
`

func TestImportedAndInlineComponentsRenderContract(t *testing.T) {
	root := t.TempDir()
	phase1CopyModuleFiles(t, root)
	phase1WriteFile(t, filepath.Join(root, "pages", "ui.gox"), importedPanelSource)
	entry := filepath.Join(root, "pages", "index.gox")
	phase1WriteFile(t, entry, `module index
import UI "./ui.gox"
import "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page { return <main /> }
func Local(title string, children gox.Component) gox.Component {
 return <div>{{ title }}<&children><i>local fallback</i></&children></div>
}
func Example() gox.Component {
 heading := <b>heading</b>
 absent := <&UI.Panel right="R" header={{ heading }} left="L" />
 present := <&UI.Panel right="R" header={{ heading }} left="L"><p>body</p></&UI.Panel>
 localAbsent := <&Local title="local" />
 localPresent := <&Local title="local"><em>child</em></&Local>
 return <><&absent /><&present /><&localAbsent /><&localPresent /><&UI.Panel right="R" header={{ heading }} left="L" /><&UI.Plain title="end" /></>
}
`)
	out := filepath.Join(root, ".build", "components", "generated.go")
	phase1WriteFile(t, out, "")
	if _, err := Compile(entry, out); err != nil {
		t.Fatal(err)
	}
	want := `<section><h1>L:R</h1><b>heading</b><p>fallback</p></section><section><h1>L:R</h1><b>heading</b><p>body</p></section><div>local<i>local fallback</i></div><div>local<em>child</em></div><section><h1>L:R</h1><b>heading</b><p>fallback</p></section><p>end</p>`
	phase1WriteFile(t, filepath.Join(filepath.Dir(out), "render_test.go"), `package main
import ("bytes"; "html"; "testing"; "github.com/gox-lang/gox")
type outputWriter struct { gox.Writer; output bytes.Buffer }
func (w *outputWriter) Write(p []byte) (int,error) { return w.output.Write(p) }
func (w *outputWriter) WriteTextString(s string) { w.output.WriteString(html.EscapeString(s)) }
func TestRender(t *testing.T) {
 w := &outputWriter{}
 GoxPage.Example(w)
 if got, want := w.output.String(), `+strconv.Quote(want)+`; got != want { t.Fatalf("HTML = %q; want %q", got, want) }
}
`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=readonly", "-count=1", ".")
	cmd.Dir = filepath.Dir(out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated Go render failed: %v\n%s", err, output)
	}
}

func TestImportedComponentRejectsInvalidTemplateContracts(t *testing.T) {
	for _, test := range []struct{ name, invocation, kind string }{
		{"missing", `<&UI.Plain />`, "missing-component-prop"},
		{"unknown", `<&UI.Plain title="ok" extra="bad" />`, "unknown-component-prop"},
		{"duplicate", `<&UI.Plain title="a" title="b" />`, "duplicate-component-prop"},
		{"children", `<&UI.Plain title="ok"><p>bad</p></&UI.Plain>`, "component-does-not-accept-children"},
		{"empty_block", `<&UI.Plain title="ok"></&UI.Plain>`, "component-does-not-accept-children"},
	} {
		for _, inline := range []bool{false, true} {
			t.Run(test.name+"/inline="+strconv.FormatBool(inline), func(t *testing.T) {
				body := "return " + test.invocation
				if inline {
					body = "view := " + test.invocation + "\nreturn <&view />"
				}
				err := phase2CompileExpectError(t, phase1GeneratedGoFixture{
					entryRel: "pages/index.gox",
					source:   "module index\nimport UI \"./ui.gox\"\nimport \"github.com/gox-lang/gox\"\nfunc GET(ctx *gox.Context) gox.Page {\n" + body + "\n}\n",
					files:    map[string]string{"pages/ui.gox": importedPanelSource},
				})
				phase2RequireErrorKind(t, err, test.kind)
			})
		}
	}
}

func TestInlineComponentWithAliasedSDKType(t *testing.T) {
	phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
		entryRel: "pages/index.gox",
		source: `module index
import gx "github.com/gox-lang/gox"
func Panel(header gx.Component, children gx.Component) gx.Component {
 return <div><&header /><&children /></div>
}
func GET(ctx *gx.Context) gx.Page {
 var heading gx.Component = <h1>Title</h1>
 view := <&Panel header={{ heading }} />
 return <&view />
}
`,
	})
}

func TestInlineImportedDiagnosticUsesOriginalSourcePosition(t *testing.T) {
	const source = `module index
import UI "./ui.gox"
import "github.com/gox-lang/gox"
func Before() gox.Component {
 return <section>
  <p>Earlier multiline template</p>
 </section>
}
func GET(ctx *gox.Context) gox.Page {
 first := <div>
  <p>Earlier inline template</p>
 </div>
 view := <&UI.Plain />
 return <><&first /><&view /></>
}
`
	err := phase2CompileExpectError(t, phase1GeneratedGoFixture{
		entryRel: "pages/index.gox", source: source,
		files: map[string]string{"pages/ui.gox": importedPanelSource},
	})
	var diagnostic *goxerrors.GoxError
	if !errors.As(err, &diagnostic) {
		t.Fatalf("expected GOX diagnostic, got %v", err)
	}
	line, column := sourceLineColumn(source, strings.Index(source, "UI.Plain"))
	if diagnostic.Line != line || diagnostic.Column != column {
		t.Fatalf("diagnostic = %d:%d; want source position %d:%d: %v", diagnostic.Line, diagnostic.Column, line, column, err)
	}
}
