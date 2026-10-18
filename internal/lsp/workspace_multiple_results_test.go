package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceTypeEnvironmentInfersMultipleFunctionResultsAndBlankIdentifiers(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	usersSource := `module users
type User struct { Name string }
func Lookup() (User, error) { return User{}, nil }
`
	pageSource := `module page
import users "./users.gox"
func Build() {
	user, err := users.Lookup()
	_, secondErr := users.Lookup()
	thirdUser, _ := users.Lookup()
	var fourthUser, fourthErr = users.Lookup()
	var _, fifthErr = users.Lookup()
	_, _, _, _, _, _, _ = user, err, secondErr, thirdUser, fourthUser, fourthErr, fifthErr
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
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "user,"), "var user users.User")
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "err,"), "var err error")
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "secondErr,"), "var secondErr error")
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "thirdUser"), "var thirdUser users.User")
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "fourthUser"), "var fourthUser users.User")
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "fourthErr"), "var fourthErr error")
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "fifthErr"), "var fifthErr error")
	blankOffset := strings.Index(pageSource, "_, secondErr")
	if hover := hoverAtOffset(t, server, document, blankOffset); hover != nil {
		t.Fatalf("blank identifier hover = %#v", hover)
	}
}

func TestWorkspaceTypeEnvironmentInfersMultipleMethodResults(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	usersSource := `module users
type User struct { Name string }
func NewUser() User { return User{} }
func (user User) Split() (string, int) { return user.Name, 30 }
`
	pageSource := `module page
import people "./users.gox"
func Build() {
	user := people.NewUser()
	name, age := user.Split()
	_, _ = name, age
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
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "name,"), "var name string")
	assertLocalHover(t, server, document, strings.LastIndex(pageSource, "age"), "var age int")
}
