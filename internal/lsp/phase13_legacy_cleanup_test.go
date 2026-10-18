package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase13LegacyPseudoTypeCheckerRemoved(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"func (s *Server) inferLocalSymbol(",
		"func (s *Server) inferLocalSymbolWithGuard(",
		"func (s *Server) inferExpressionType(",
		"func (s *Server) inferCallType(",
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, pattern := range forbidden {
			if strings.Contains(string(content), pattern) {
				t.Fatalf("legacy pseudo type checker %q still exists in %s", pattern, name)
			}
		}
	}
}

func TestPhase13SemanticConsumersKeepInferredLocalAndDanglingSelector(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	usersPath := filepath.Join(root, "users.gox")
	usersSource := `module users
type User struct { Name string }
func Lookup(id int) User { return User{} }
func (user User) GetName() string { return user.Name }
`
	writeWorkspaceGOX(t, usersPath, usersSource)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build() string {
	user := users.Lookup(1)
	name := user.GetName()
	view := <span>{{ name }}</span>
	_ = view
	return name
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "name"), "var name string")
	methodOffset := strings.Index(pageSource, "GetName") + 1
	assertDefinitionURIAndOffset(t, server, document, methodOffset, FileURI(usersPath), usersSource, strings.Index(usersSource, "GetName"))
	if diagnostics := server.diagnostics(document); len(diagnostics) != 0 {
		t.Fatalf("valid semantic document diagnostics = %#v", diagnostics)
	}

	edited := document
	edited.Text = strings.Replace(pageSource, "user.GetName()", "user.", 1)
	completionOffset := strings.Index(edited.Text, "user.") + len("user.")
	position, err := edited.Position(completionOffset)
	if err != nil {
		t.Fatal(err)
	}
	items := server.completions(edited, position).Items
	requireCompletionItem(t, items, "GetName")
	requireCompletionItem(t, items, "Name")
}
