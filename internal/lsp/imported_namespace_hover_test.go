package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestImportedGOXNamespaceHoverListsOnlyExportedModuleDeclarations(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	usersSource := `module users
client Menu react {
	src: "menu.jsx"
}
type User struct { Name string; age int }
type Reader interface { Read() string }
const Limit = 10
var OtraVariable string
func SplitName(fullname string) (string, string) { return fullname, fullname }
func privateFunction() {}
func (user User) GetName() string { return user.Name }
`
	pageSource := `module page
import users "./users.gox"
func Build() { var _ users.User }
`
	writeWorkspaceGOX(t, usersPath, usersSource)
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	for _, offset := range []int{strings.Index(pageSource, "users"), strings.LastIndex(pageSource, "users")} {
		hover := hoverAtOffset(t, server, document, offset+1)
		if hover == nil {
			t.Fatalf("namespace hover at %d is nil", offset)
		}
		for _, want := range []string{
			"module users {",
			"struct User",
			"interface Reader",
			"const Limit = 10",
			"var OtraVariable string",
			"func SplitName(fullname string) (string, string)",
			"explicit alias `users`",
		} {
			if !strings.Contains(hover.Contents.Value, want) {
				t.Errorf("namespace hover missing %q: %s", want, hover.Contents.Value)
			}
		}
		for _, unwanted := range []string{"privateFunction", "GetName", "client Menu", "field Name", "age"} {
			if strings.Contains(hover.Contents.Value, unwanted) {
				t.Errorf("namespace hover contains %q: %s", unwanted, hover.Contents.Value)
			}
		}
	}
}

func TestImportedGOXNamespaceHoverIdentifiesInferredAlias(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, usersPath, "module users\ntype User struct{}\n")
	pageSource := "module page\nimport \"./users.gox\"\nfunc Build() { var _ users.User }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	hover := hoverAtOffset(t, server, document, strings.LastIndex(pageSource, "users")+1)
	if hover == nil || !strings.Contains(hover.Contents.Value, "inferred alias `users`") || !strings.Contains(hover.Contents.Value, "struct User") {
		t.Fatalf("inferred namespace hover = %#v", hover)
	}
}
