package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceTypeEnvironmentInfersImportedExpressionsAndChains(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	usersSource := `module users
const DefaultName = "George"
const Limit = 10
type User struct { Name string }
func NewUser(name string) User { return User{Name: name} }
func (user User) GetName() string { return user.Name }
`
	pageSource := `module page
import people "./users.gox"
func Build() {
	user := people.NewUser("George")
	name := user.GetName()
	defaultName := people.DefaultName
	limit := people.Limit
	_, _, _, _ = user, name, defaultName, limit
}
`
	writeWorkspaceGOX(t, usersPath, usersSource)
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "user,"), "var user people.User")
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "name,"), "var name string")
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "defaultName,"), "var defaultName string")
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "limit"), "var limit int")

	methodUse := strings.Index(pageSource, "user.GetName") + len("user.") + 1
	assertDefinitionURIAndOffset(t, server, document, methodUse, FileURI(usersPath), usersSource, strings.Index(usersSource, "GetName"))
}

func TestWorkspaceTypeEnvironmentRespectsLocalImportShadowing(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, usersPath, "module users\nconst Name = \"imported\"\n")
	pageSource := `module page
import users "./users.gox"
type Local struct { Name int }
func Build() {
	users := Local{Name: 7}
	name := users.Name
	_ = name
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "name"), "var name int")
}
