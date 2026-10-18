package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerReturnsHoverOverLSP(t *testing.T) {
	uri := "file:///workspace/pages/index.gox"
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":"module main\nfunc Card() string { return \"George\" }"}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":1,"character":6}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	for _, response := range decodeFramedResponses(t, output.String()) {
		if string(response.ID) != "2" {
			continue
		}
		var result Hover
		if err := json.Unmarshal(response.Result, &result); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Contents.Value, "func Card() string") {
			t.Fatalf("hover response = %#v", result)
		}
		return
	}
	t.Fatalf("hover response not found: %s", output.String())
}

func TestHoverUsesContextualGOXReferenceDefinition(t *testing.T) {
	source := `module main

client Menu react {
	src: "menu.jsx"
}

func Card() gox.Component {
	return <article>George</article>
}

func Render() gox.Component {
	return <main><&Card/><@Menu/></main>
}
`
	document := Document{URI: "file:///workspace/pages/index.gox", Path: "/workspace/pages/index.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}

	card := hoverAtOffset(t, server, document, strings.LastIndex(source, "Card")+1)
	if card == nil || card.Contents.Kind != "markdown" || !strings.Contains(card.Contents.Value, "func Card() gox.Component") {
		t.Fatalf("Card hover = %#v", card)
	}
	menu := hoverAtOffset(t, server, document, strings.LastIndex(source, "Menu")+1)
	if menu == nil || !strings.Contains(menu.Contents.Value, "client Menu react {\n\tsrc: \"menu.jsx\"\n\tdev: false") {
		t.Fatalf("Menu hover = %#v", menu)
	}
}

func TestHoverDescribesClientDeclarationPropertiesAndValues(t *testing.T) {
	source := `module main

client Menu react {
	src: "menu/menu.jsx"
	dev: true
}

func Render() gox.Component {
	return <@Menu />
}
`
	document := Document{URI: "file:///workspace/pages/index.gox", Path: "/workspace/pages/index.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"react":           "framework react",
		"src":             "property src string",
		`"menu/menu.jsx"`: `string "menu/menu.jsx"`,
		"dev":             "property dev bool",
		"true":            "bool true",
	}
	for token, detail := range want {
		hover := hoverAtOffset(t, server, document, strings.Index(source, token)+1)
		if hover == nil || !strings.Contains(hover.Contents.Value, detail) {
			t.Errorf("hover on %s = %#v, want %q", token, hover, detail)
		}
	}
	menu := hoverAtOffset(t, server, document, strings.LastIndex(source, "Menu")+1)
	if menu == nil || !strings.Contains(menu.Contents.Value, "src: \"menu/menu.jsx\"") || !strings.Contains(menu.Contents.Value, "dev: true") {
		t.Fatalf("@Menu hover = %#v", menu)
	}
}

func TestHoverReturnsNilOutsideIndexedSymbol(t *testing.T) {
	document := Document{URI: "file:///workspace/pages/index.gox", Path: "/workspace/pages/index.gox", Text: "module main\n"}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	if result := hoverAtOffset(t, server, document, len(document.Text)); result != nil {
		t.Fatalf("hover = %#v, want nil", result)
	}
}

func hoverAtOffset(t *testing.T, server *Server, document Document, offset int) *Hover {
	t.Helper()
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	return server.hover(document, position)
}

func TestHoverDescribesGoBuiltins(t *testing.T) {
	source := `module page
func Build() {
	println("hello")
	_ = recover()
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	printlnHover := hoverAtOffset(t, server, document, strings.Index(source, "println")+1)
	if printlnHover == nil || !strings.Contains(printlnHover.Contents.Value, "func println(args ...Type)") {
		t.Fatalf("println hover = %#v", printlnHover)
	}
	recoverHover := hoverAtOffset(t, server, document, strings.Index(source, "recover")+1)
	if recoverHover == nil || !strings.Contains(recoverHover.Contents.Value, "func recover() any") {
		t.Fatalf("recover hover = %#v", recoverHover)
	}
}

func TestHoverPrefersLocalSymbolOverGoBuiltin(t *testing.T) {
	source := `module page
func len(value string) int { return 0 }
func Build() { _ = len("x") }
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	hover := hoverAtOffset(t, server, document, strings.LastIndex(source, "len")+1)
	if hover == nil || !strings.Contains(hover.Contents.Value, "func len(value string) int") {
		t.Fatalf("len hover = %#v", hover)
	}
}

func TestHoverResolvesMethodsOnTypedLocalValues(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	pagePath := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() {
	reader := strings.NewReader("hello")
	_ = reader.Read
}
`
	writeWorkspaceGOX(t, pagePath, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	hover := hoverAtOffset(t, server, document, strings.LastIndex(source, "Read")+1)
	if hover == nil || !strings.Contains(hover.Contents.Value, "Read(b []byte) (n int, err error)") {
		t.Fatalf("Read hover = %#v", hover)
	}
}
