package lsp

import (
	"strings"
	"testing"
)

func TestHoverResolvesParametersLocalsAndShadowing(t *testing.T) {
	source := `module profile
func Render(ctx *gox.Context) gox.Page {
	name := "George"
	_ = ctx
	if true {
		name := 42
		_ = name
	}
	return <h1>{{name}}</h1>
}`
	document := Document{URI: "file:///project/pages/index.gox", Path: "/project/pages/index.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	assertLocalHover(t, server, document, strings.Index(source, "ctx\n")+1, "var ctx *gox.Context")
	assertLocalHover(t, server, document, strings.Index(source, "name\n\t}")+1, "var name int")
	assertLocalHover(t, server, document, strings.LastIndex(source, "name}}")+1, "var name string")
}

func assertLocalHover(t *testing.T, server *Server, document Document, offset int, want string) {
	t.Helper()
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	hover := server.hover(document, position)
	if hover == nil || !strings.Contains(hover.Contents.Value, want) {
		t.Fatalf("hover at %d = %#v, want %q", offset, hover, want)
	}
}
