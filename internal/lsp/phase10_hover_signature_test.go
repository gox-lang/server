package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase10SemanticHoverOwnsImportedGoxObjects(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	usersPath := filepath.Join(root, "users.gox")
	writeWorkspaceGOX(t, usersPath, `module users
type User struct { Name string; age int }
func (user User) GetName() string { return user.Name }
func Lookup(id int, strict bool) (User, error) { return User{}, nil }
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build(user users.User) string {
	_ = user.Name
	_ = user.GetName()
	_, _ = users.Lookup(10, false)
	_ = user.age
	return user.Name
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	method := server.semanticHoverAt(document, strings.Index(pageSource, "user.GetName")+len("user.")+1)
	if method == nil || !strings.Contains(method.Contents.Value, "func (user User) GetName() string") {
		t.Fatalf("semantic imported GOX method hover = %#v", method)
	}
	field := server.semanticHoverAt(document, strings.Index(pageSource, "user.Name")+len("user.")+1)
	if field == nil || !strings.Contains(field.Contents.Value, "field Name string") {
		t.Fatalf("semantic imported GOX field hover = %#v", field)
	}
	lookup := server.semanticHoverAt(document, strings.Index(pageSource, "users.Lookup")+len("users.")+1)
	if lookup == nil || !strings.Contains(lookup.Contents.Value, "func Lookup(id int, strict bool) (User, error)") {
		t.Fatalf("semantic imported GOX function hover = %#v", lookup)
	}
	private := server.semanticHoverAt(document, strings.Index(pageSource, "user.age")+len("user.")+1)
	if private != nil {
		t.Fatalf("private imported GOX field hover = %#v", private)
	}
}

func TestPhase10SemanticSignatureOwnsImportedGoxCallInsideTemplate(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), `module users
type User struct { Name string }
func Lookup(id int, strict bool) User { return User{} }
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Render() string {
	return <span>{{ users.Lookup(10, false) }}</span>
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	open := strings.Index(pageSource, "users.Lookup(") + len("users.Lookup")
	candidate, ok := server.semanticSignatureCandidate(document, open)
	if !ok {
		t.Fatal("semantic signature candidate was not resolved")
	}
	if candidate.Label != "func Lookup(id int, strict bool) User" {
		t.Fatalf("semantic signature label = %q", candidate.Label)
	}
	if len(candidate.Parameters) != 2 || candidate.Parameters[0].Name != "id" || candidate.Parameters[0].Type != "int" || candidate.Parameters[1].Name != "strict" || candidate.Parameters[1].Type != "bool" {
		t.Fatalf("semantic signature parameters = %#v", candidate.Parameters)
	}
}

func TestPhase10SemanticHoverAndSignatureRespectGoxInternalMetadata(t *testing.T) {
	root, pagePath, _ := goxSDKPublicAPIFixture(t)
	pageSource := `module page
import gox "github.com/gox-lang/gox"
func Use(ctx *gox.Context) {
	_ = ctx.Request
	_ = ctx.RuntimeField
	ctx.RuntimeMethod()
}
`
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	request := server.semanticHoverAt(document, strings.Index(pageSource, "ctx.Request")+len("ctx.")+1)
	if request == nil || !strings.Contains(request.Contents.Value, "field Request string") {
		t.Fatalf("public SDK field hover = %#v", request)
	}
	if hover := server.semanticHoverAt(document, strings.Index(pageSource, "ctx.RuntimeField")+len("ctx.")+1); hover != nil {
		t.Fatalf("internal SDK field hover = %#v", hover)
	}
	open := strings.Index(pageSource, "ctx.RuntimeMethod(") + len("ctx.RuntimeMethod")
	if candidate, ok := server.semanticSignatureCandidate(document, open); ok {
		t.Fatalf("internal SDK method signature = %#v", candidate)
	}
}

func TestPhase10SemanticHoverYieldsToFallbackForInvalidTypesAndBlankIdentifier(t *testing.T) {
	invalidSource := `module profile
func Render(ctx *gox.Context) gox.Page {
	_ = ctx
	return <span>{{ctx}}</span>
}
`
	invalidDocument := Document{URI: "file:///project/pages/index.gox", Path: "/project/pages/index.gox", Text: invalidSource}
	invalidServer := NewServer()
	if err := invalidServer.symbols.UpdateOpen(invalidDocument); err != nil {
		t.Fatal(err)
	}
	ctxOffset := strings.Index(invalidSource, "_ = ctx") + len("_ = ") + 1
	if hover := invalidServer.semanticHoverAt(invalidDocument, ctxOffset); hover != nil {
		t.Fatalf("semantic hover must yield invalid ctx type to compatibility fallback: %#v", hover)
	}
	position, err := invalidDocument.Position(ctxOffset)
	if err != nil {
		t.Fatal(err)
	}
	ctxHover := invalidServer.hover(invalidDocument, position)
	if ctxHover == nil || !strings.Contains(ctxHover.Contents.Value, "var ctx *gox.Context") {
		t.Fatalf("fallback ctx hover = %#v", ctxHover)
	}

	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), `module users
type User struct { Name string }
func Lookup() (User, error) { return User{}, nil }
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build() {
	_, err := users.Lookup()
	_ = err
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	blankServer := NewServer()
	blankServer.indexWorkspaceRoots([]string{root})
	blankDocument, ok := blankServer.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	blankOffset := strings.Index(pageSource, "_, err")
	if hover := blankServer.semanticHoverAt(blankDocument, blankOffset); hover != nil {
		t.Fatalf("semantic blank identifier hover = %#v", hover)
	}
	if hover := hoverAtOffset(t, blankServer, blankDocument, blankOffset); hover != nil {
		t.Fatalf("blank identifier hover = %#v", hover)
	}
}
