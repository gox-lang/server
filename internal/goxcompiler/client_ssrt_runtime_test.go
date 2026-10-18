package gox

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute generated Go against the real SDK island writer. Only asset lookup
// is a fixture; the expected HTML is the SSRT language contract.
func TestClientSSRTRuntimeContract(t *testing.T) {
	for _, framework := range []string{"react", "preact", "solid", "vue", "svelte"} {
		t.Run(framework, func(t *testing.T) {
			source := strings.ReplaceAll(`module index
import "github.com/gox-lang/gox"
client Menu FRAMEWORK { src: "menu/menu.jsx" }
func GET(ctx *gox.Context) gox.Page { return <main /> }
func Panel(children gox.Component) gox.Component {
 return <section><&children><p>fallback</p></&children></section>
}
func Example(ctx *gox.Context) gox.Component {
 calls := 0
 items := func() []string { calls++; return []string{"A", "B"} }
 label := "<unsafe>"
 inline := <:if value :assign={{ label }} condition={{ value != "" }}><strong>inline</strong></:if>
 empty := []string{}
 return <><@Menu hydrate="load" title="menu" /><@Menu hydrate="visible" title={{ label }}><:if-group><:if value :assign={{ label }} condition={{ value == "" }}><p>WRONG</p></:if><:else-if label :assign={{ value }} condition={{ label != "" }}><:switch value={{ label }}><:case is={{ "<unsafe>" }}><h2>{{ label }}</h2></:case><:default><p>WRONG</p></:default></:switch></:else-if><:else><p>WRONG</p></:else></:if-group><&inline /><:for i item range={{ empty }}><:empty><p>empty</p></:empty><:each><p>{{ i }}:{{ item }}</p></:each></:for><&Panel /><&Panel><b>child</b></&Panel><:for i item range={{ items() }}><:empty><p>WRONG</p></:empty><:each><:repeat n times={{ 2 }}><span>{{ i }}:{{ n }}:{{ item }}</span></:repeat></:each></:for><@Menu hydrate="idle"><em>nested</em></@Menu></@Menu><p>{{ calls }}</p></>
}
`, "FRAMEWORK", framework)
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
			phase1WriteFile(t, filepath.Join(root, "render_test.go"), ssrtRenderTest)
			cmd := exec.Command("go", "test", "-mod=readonly", "-count=1", ".")
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("execute generated SSRT: %v\n%s", err, out)
			}
		})
	}
}

const ssrtRenderTest = `package main
import ("bytes"; "fmt"; "html"; "strconv"; "testing"; "github.com/gox-lang/gox")
type outputWriter struct { gox.Writer; output bytes.Buffer }
func (w *outputWriter) Write(p []byte) (int,error) { return w.output.Write(p) }
func (w *outputWriter) WriteRawString(s string) { w.output.WriteString(s) }
func (w *outputWriter) WriteRawBytes(p []byte) { w.output.Write(p) }
func (w *outputWriter) WriteTextAny(v any) { w.WriteTextString(fmt.Sprint(v)) }
func (w *outputWriter) WriteTextString(s string) { w.output.WriteString(html.EscapeString(s)) }
func (w *outputWriter) WriteTextBytes(p []byte) { w.WriteTextString(string(p)) }
func (w *outputWriter) WriteTextInt(n int) { w.output.WriteString(strconv.Itoa(n)) }
func (w *outputWriter) WriteByte(b byte) error { return w.output.WriteByte(b) }
type assets struct{}
func (assets) ResolveClientIsland(key string) (gox.ClientIslandSnapshot,bool) { return gox.ClientIslandSnapshot{ScriptURL:"/menu.js"},true }
func (assets) ClientRuntimeHTML() []byte { return nil }
func TestRender(t *testing.T) {
 w := &outputWriter{}
 ctx := &gox.Context{Writer:w}
 gox.BindClientIslandRuntime(ctx,assets{})
 GoxPage.Example(w,ctx)
 want := "<gox-isle data-script=\"/menu.js\" data-hydrate=\"load\" data-props=\"{&#34;title&#34;:&#34;menu&#34;}\"></gox-isle><gox-isle data-script=\"/menu.js\" data-hydrate=\"visible\" data-props=\"{&#34;title&#34;:&#34;\\u003cunsafe\\u003e&#34;}\"><h2>&lt;unsafe&gt;</h2><strong>inline</strong><p>empty</p><section><p>fallback</p></section><section><b>child</b></section><span>0:0:A</span><span>0:1:A</span><span>1:0:B</span><span>1:1:B</span><gox-isle data-script=\"/menu.js\" data-hydrate=\"idle\"><em>nested</em></gox-isle></gox-isle><p>1</p>"
 if got := w.output.String(); got != want { t.Fatalf("HTML = %q, want %q",got,want) }
}
`
