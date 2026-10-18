package gox

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebSocketIndexExportsHandler(t *testing.T) {
	src := `module websocket

import "github.com/gox-lang/gox"

func Index(socket *gox.Socket) gox.WebSocket {
	socket.When("connect", func(isReconnect bool) {})
}
`
	mod, err := ParseModule("websocket/index.gox", src)
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
	generated := out.String()
	if !strings.Contains(generated, "var GoxWebSocketHandler gox.WebSocketHandler = GoxPage.Index") {
		t.Fatalf("compiled bundle does not export WebSocket handler:\n%s", generated)
	}
	if strings.Contains(generated, "func (m *GoxModuleWebsocket) Index(socket *gox.Socket) gox.WebSocket") {
		t.Fatalf("compiled WebSocket Index still declares marker return:\n%s", generated)
	}
}

func TestCompileWebSocketOmitsPageClientMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entryDir := filepath.Join(dir, "websocket")
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(entryDir, "index.gox")
	if err := os.WriteFile(entry, []byte(`module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, ".build", "websocket")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(outDir, "test.go")
	if _, err := Compile(entry, outPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	generated := string(data)
	for _, unwanted := range []string{
		"goxActionsClientScript",
		"goxStaticImportMapEntries",
		"goxStaticImportMapScript",
		"goxStaticImportMapHash",
	} {
		if strings.Contains(generated, unwanted) {
			t.Fatalf("compiled WebSocket bundle contains unused %s metadata:\n%s", unwanted, generated)
		}
	}
}

func TestWebSocketIndexRejectsInvalidSignature(t *testing.T) {
	_, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index() gox.WebSocket {}
`)
	if err == nil || !strings.Contains(err.Error(), "invalid WebSocket signature in Websocket.Index") {
		t.Fatalf("expected WebSocket signature error, got %v", err)
	}
}

func TestCompileWebSocketWritesAbsoluteLineMarkersInsideCallbacks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entryDir := filepath.Join(dir, "websocket")
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(entryDir, "index.gox")
	source := `module websocket

import (
	"time"
	"github.com/gox-lang/gox"
)

func Index(socket *gox.Socket) gox.WebSocket {
	socket.When("connect", func(isReconnect bool) {
		println("connected")
		// This comment and the empty line must not shift diagnostics.

		timer := time.AfterFunc(5*time.Second, func() {
			socket.Disconnect(0)
		})
	})
}

`
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, ".build", "websocket")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(outDir, "test.go")
	if _, err := Compile(entry, outPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	generated := string(data)
	for _, marker := range []string{
		"//line websocket/index.gox:9:1",
		"//line websocket/index.gox:10:1",
		"//line websocket/index.gox:13:1",
		"//line websocket/index.gox:14:1",
	} {
		if !strings.Contains(generated, marker) {
			t.Fatalf("compiled WebSocket bundle is missing %s:\n%s", marker, generated)
		}
	}
}

func TestCompileWebSocketPreservesOnCallbackParameterNames(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("profile", func(name string, age int, avatar gox.File) {})
}`)
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
	generated := out.String()
	if !strings.Contains(generated, `socket.OnRegistered("profile", []string{"name", "age", "avatar"}, false, func(name string, age int, avatar gox.File)`) {
		t.Fatalf("compiled WebSocket does not preserve On parameter names:\n%s", generated)
	}
}

func TestCompileWebSocketPreservesNamedOnHandlerParameters(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func profile(name string, age int) {}
func Index(socket *gox.Socket) gox.WebSocket { socket.On("profile", profile) }`)
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
	if generated := out.String(); !strings.Contains(generated, `socket.OnRegistered("profile", []string{"name", "age"}, false, m.profile)`) {
		t.Fatalf("compiled named handler signature is missing:\n%s", generated)
	}
}

func TestCompileWebSocketRecordsEventReply(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("profile", func(profileID string) gox.EventReply {
		return profileID
	})
}`)
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
	if generated := out.String(); !strings.Contains(generated, `socket.OnRegistered("profile", []string{"profileID"}, true, func(profileID string) gox.EventReply`) {
		t.Fatalf("compiled EventReply contract is missing:\n%s", generated)
	}
}

func TestCompileWebSocketEmitAcceptsHTMLAndComponents(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Card(name string) gox.Component { return <article>{{name}}</article> }
func Index(socket *gox.Socket) gox.WebSocket {
	title := <h1>Hola Mundo</h1>
	socket.Emit("title", title)
	socket.Emit("card", <&Card name="Javier"/>)
}`)
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
	generated := out.String()
	if !strings.Contains(generated, `title := func(goxWriter gox.Writer)`) {
		t.Fatalf("inline HTML value was not compiled as a writer function:\n%s", generated)
	}
	if !strings.Contains(generated, `socket.Emit("card", func(goxWriter gox.Writer)`) || !strings.Contains(generated, `m.Card(goxWriter, "Javier")`) {
		t.Fatalf("Emit component was not compiled with its active writer:\n%s", generated)
	}
}

func TestCompileWebSocketEventReplyReturnsHTMLValue(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("profile", func(name string) gox.EventReply {
		return <h1>Hola {{name}}</h1>
	})
}`)
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
	generated := out.String()
	if strings.Contains(generated, "ctx.Writer") {
		t.Fatalf("WebSocket EventReply incorrectly uses the HTTP writer:\n%s", generated)
	}
	if !strings.Contains(generated, `return func(goxWriter gox.Writer)`) || !strings.Contains(generated, `goxWriter.WriteTextString(name)`) {
		t.Fatalf("EventReply HTML was not compiled as a renderable value:\n%s", generated)
	}
}

func TestCompileWebSocketRejectsUnmarkedOnReturn(t *testing.T) {
	_, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("profile", func(profileID string) string { return profileID })
}`)
	if err == nil || !strings.Contains(err.Error(), "return type must be gox.EventReply") {
		t.Fatalf("expected EventReply signature error, got %v", err)
	}
}

func TestCompileWebSocketRejectsMultipleOnReturns(t *testing.T) {
	_, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("profile", func(profileID string) (string, error) { return profileID, nil })
}`)
	if err == nil || !strings.Contains(err.Error(), "can return at most one value") {
		t.Fatalf("expected single reply signature error, got %v", err)
	}
}

func TestCompileWebSocketRewritesEmitAwaitAtomically(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	var theme map[string]any
	err := socket.Emit("get-theme", 7).Await(&theme)
	_ = err
	socket.Emit("typing", true)
}`)
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
	generated := out.String()
	if !strings.Contains(generated, `err := socket.EmitAwait(&theme, "get-theme", 7)`) {
		t.Fatalf("Emit Await was not rewritten atomically:\n%s", generated)
	}
	if !strings.Contains(generated, `socket.Emit("typing", true)`) {
		t.Fatalf("plain Emit was rewritten unexpectedly:\n%s", generated)
	}
}

func TestCompilePageRewritesServerSocketGetByIDEmitAwait(t *testing.T) {
	mod, err := ParseModule("pages/index.gox", `module pages
import "github.com/gox-lang/gox"
func Index(ctx *gox.Context) gox.Page {
	var profile map[string]any
	target, found := gox.Sockets.GetByID("socket-1")
	_ = found
	err := target.Emit("profile", 42).Await(&profile)
	_ = err
}`)
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
	generated := out.String()
	if !strings.Contains(generated, `err := target.EmitAwait(&profile, "profile", 42)`) {
		t.Fatalf("distributed Await was not rewritten atomically:\n%s", generated)
	}
}

func TestCompilePageRewritesAwaitFromSelectedSocketVariable(t *testing.T) {
	mod, err := ParseModule("pages/index.gox", `module pages
import "github.com/gox-lang/gox"
func Index(ctx *gox.Context) gox.Page {
	target, found := gox.Sockets.GetByID("socket-1")
	_ = found
	var profile string
	err := target.Emit("profile").Await(&profile)
	_ = err
}`)
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
	if generated := out.String(); !strings.Contains(generated, `err := target.EmitAwait(&profile, "profile")`) {
		t.Fatalf("selected socket Await was not rewritten:\n%s", generated)
	}
}

func TestCompileWebSocketEventReplyReturnsClientWithRenderContext(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
client Counter react {
    src: "playground/Counter.jsx"
}
func Index(socket *gox.Socket) gox.WebSocket {
    socket.On("loadClient", func(name string) gox.EventReply {
        return <section><@Counter hydrate="load" name={{ name }} /></section>
    })
}`)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), &ast.File{Name: ast.NewIdent("main"), Decls: bundle.Decls}); err != nil {
		t.Fatal(err)
	}
	generated := out.String()
	for _, want := range []string{"return func(ctx *gox.Context)", "gox.WriteClientIsland(ctx,", "gox.WriteClientPropsAttribute(ctx.Writer,"} {
		if !strings.Contains(generated, want) {
			t.Fatalf("missing %q:\n%s", want, generated)
		}
	}
	if strings.Contains(generated, "goxWriter") {
		t.Fatalf("mixed render writers:\n%s", generated)
	}
}
