package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPhase14DiagnosticsCacheReturnsIndependentSlices(t *testing.T) {
	source := `module page
func Build() string {
	return 42
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Version: 1, Text: source}
	server := NewServer()
	first := server.diagnostics(document)
	if len(first) == 0 {
		t.Fatal("expected semantic diagnostic")
	}
	originalMessage := first[0].Message
	first[0].Message = "mutated by caller"
	second := server.diagnostics(document)
	if len(second) == 0 || second[0].Message != originalMessage {
		t.Fatalf("cached diagnostics were externally mutated: %#v", second)
	}
}

func TestPhase14DiagnosticsCachePreservesEmptyArrayOnWire(t *testing.T) {
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Version: 1, Text: "module page\n"}
	server := NewServer()
	diagnostics := server.diagnostics(document)
	if diagnostics == nil || len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want non-nil empty slice", diagnostics)
	}
	data, err := json.Marshal(PublishDiagnosticsParams{URI: document.URI, Version: document.Version, Diagnostics: diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"diagnostics":[]`) {
		t.Fatalf("publish diagnostics JSON = %s, want diagnostics array", data)
	}
}

func TestPhase14TransientCompletionCachePreservesCompletionBehavior(t *testing.T) {
	root := t.TempDir()
	usersPath := root + "/users.gox"
	pagePath := root + "/page.gox"
	writeWorkspaceGOX(t, usersPath, `module users
type User struct { Name string }
func Lookup(id int) User { return User{} }
func (user User) GetName() string { return user.Name }
`)
	pageSource := `module page
import users "./users.gox"
func Build() {
	user := users.Lookup(1)
	_ = user.
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Version: 1, Text: pageSource}
	offset := strings.Index(pageSource, "user.") + len("user.")
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	first := server.completions(document, position).Items
	second := server.completions(document, position).Items
	requireCompletionItem(t, first, "Name")
	requireCompletionItem(t, first, "GetName")
	requireCompletionItem(t, second, "Name")
	requireCompletionItem(t, second, "GetName")
}
