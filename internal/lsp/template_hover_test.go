package lsp

import (
	"strings"
	"testing"
)

func TestHoverResolvesTemplateForAndRepeatBindings(t *testing.T) {
	source := `module user
func Render(ctx *gox.Context) gox.Page {
	users := []string{"George", "Milo"}
	return <main>
		<:for index user range={{ users }}>
			<span>{{ index }} {{ user }}</span>
		</:for>
		<:repeat attempt times={{ 2 }}>{{ attempt }}</:repeat>
	</main>
}`
	document := Document{URI: "file:///project/pages/index.gox", Path: "/project/pages/index.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	assertLocalHover(t, server, document, strings.Index(source, "{{ index }}")+3, "var index int")
	assertLocalHover(t, server, document, strings.Index(source, "{{ user }}")+3, "var user string")
	assertLocalHover(t, server, document, strings.Index(source, "{{ attempt }}")+3, "var attempt int")
	assertLocalHover(t, server, document, strings.Index(source, "index user")+1, "var index int")
	assertLocalHover(t, server, document, strings.Index(source, "user range")+1, "var user string")
}

func TestHoverDoesNotLeakForBindingsIntoEmpty(t *testing.T) {
	source := `module user
func Render(ctx *gox.Context) gox.Page {
	users := []string{"George"}
	return <:for _ user range={{ users }}>
		<:empty>{{ user }}</:empty>
		<:each>{{ user }}</:each>
	</:for>
}`
	document := Document{URI: "file:///project/pages/index.gox", Path: "/project/pages/index.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	emptyOffset := strings.Index(source, "{{ user }}") + 3
	eachOffset := strings.LastIndex(source, "{{ user }}") + 3
	emptyPosition, err := document.Position(emptyOffset)
	if err != nil {
		t.Fatal(err)
	}
	if hover := server.hover(document, emptyPosition); hover != nil {
		t.Fatalf("empty user hover = %#v, want nil", hover)
	}
	assertLocalHover(t, server, document, eachOffset, "var user string")
}
