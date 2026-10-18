package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameLocalSymbolAcrossGoAndTemplateWithoutTouchingShadow(t *testing.T) {
	source := `module page
func Card() gox.Component {
	name := "George"
	if true { name := "Milo"; _ = name }
	return <span>{{ name }}</span>
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	position := referencePosition(t, document, strings.Index(source, `name := "George"`))
	prepared := server.prepareRename(document, position)
	if prepared == nil || prepared.Placeholder != "name" {
		t.Fatalf("prepare rename = %#v", prepared)
	}
	edit := server.rename(document, position, "title")
	if edit == nil || len(edit.Changes[document.URI]) != 2 {
		t.Fatalf("rename edit = %#v", edit)
	}
	assertRenameOffsets(t, document, edit.Changes[document.URI], "title",
		strings.Index(source, `name := "George"`),
		strings.LastIndex(source, "name }}"),
	)
}

func TestRenameExportedGOXSymbolAcrossModules(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	usersSource := "module users\nfunc Lookup(id int) string { return \"George\" }\n"
	pageSource := "module page\nimport users \"./users.gox\"\nfunc Build() { _ = users.Lookup(1) }\n"
	writeWorkspaceGOX(t, usersPath, usersSource)
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	position := referencePosition(t, document, strings.Index(pageSource, "Lookup"))
	edit := server.rename(document, position, "Find")
	if edit == nil || len(edit.Changes) != 2 || len(edit.Changes[FileURI(usersPath)]) != 1 || len(edit.Changes[FileURI(pagePath)]) != 1 {
		t.Fatalf("cross-module rename = %#v", edit)
	}
}

func TestRenameComponentUpdatesOpeningAndClosingTags(t *testing.T) {
	root := t.TempDir()
	cardPath := filepath.Join(root, "card.gox")
	pagePath := filepath.Join(root, "page.gox")
	cardSource := "module card\nfunc Card(name string, children gox.Component) gox.Component { return <article>{{ children }}</article> }\n"
	pageSource := "module page\nimport card \"./card.gox\"\nfunc Build() gox.Component { return <&card.Card name={{ \"George\" }}><span>Hi</span></&card.Card> }\n"
	writeWorkspaceGOX(t, cardPath, cardSource)
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	opening := strings.Index(pageSource, "card.Card") + len("card.")
	closing := strings.LastIndex(pageSource, "card.Card") + len("card.")
	edit := server.rename(document, referencePosition(t, document, opening), "Cardes")
	if edit == nil || len(edit.Changes[FileURI(cardPath)]) != 1 || len(edit.Changes[FileURI(pagePath)]) != 2 {
		t.Fatalf("component rename = %#v", edit)
	}
	assertRenameOffsets(t, document, edit.Changes[FileURI(pagePath)], "Cardes", opening, closing)
}

func TestRenameRejectsInferredGoPackagesAndInvalidNames(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, usersPath, "module users\nfunc Lookup() string { return \"George\" }\n")
	pageSource := "module page\nimport (\n users \"./users.gox\"\n \"strings\"\n)\nfunc Build() { _ = users.Lookup(); _ = strings.TrimSpace(\" x \") }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	for _, offset := range []int{strings.Index(pageSource, "strings.TrimSpace") + len("strings.")} {
		position := referencePosition(t, document, offset)
		if prepared := server.prepareRename(document, position); prepared != nil {
			t.Fatalf("prepare rename at %d = %#v", offset, prepared)
		}
	}
	lookup := referencePosition(t, document, strings.Index(pageSource, "Lookup"))
	for _, invalid := range []string{"", "_", "for", "two words", "1name"} {
		if edit := server.rename(document, lookup, invalid); edit != nil {
			t.Fatalf("rename to %q = %#v", invalid, edit)
		}
	}
}

func TestRenameExplicitGoxImportAliasAcrossGoAndTemplate(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, usersPath, "module users\nfunc Card() gox.Component { return <span /> }\nfunc Lookup() string { return \"George\" }\n")
	pageSource := "module page\nimport people \"./users.gox\"\nfunc Build() gox.Component { _ = people.Lookup(); return <&people.Card /> }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, _ := server.symbols.Document(FileURI(pagePath))
	position := referencePosition(t, document, strings.Index(pageSource, `people "`))
	edit, message := server.renameResult(document, position, "accounts")
	if message != "" || edit == nil || len(edit.Changes) != 1 || len(edit.Changes[document.URI]) != 3 {
		t.Fatalf("alias rename = (%#v, %q)", edit, message)
	}
	if _, changed := edit.Changes[FileURI(usersPath)]; changed {
		t.Fatalf("alias rename changed imported module: %#v", edit)
	}
}

func TestRenameExplicitGoImportAlias(t *testing.T) {
	source := "module page\nimport text \"strings\"\nfunc Build() { _ = text.TrimSpace(\" x \") }\n"
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	position := referencePosition(t, document, strings.Index(source, `text "`))
	edit, message := server.renameResult(document, position, "stringsx")
	if message != "" || edit == nil || len(edit.Changes[document.URI]) != 2 {
		t.Fatalf("Go alias rename = (%#v, %q)", edit, message)
	}
}

func TestRenameImportAliasRejectsCollision(t *testing.T) {
	source := "module page\nimport (\n text \"strings\"\n fmt \"fmt\"\n)\nfunc Build() { _ = text.TrimSpace(\" x \"); _ = fmt.Sprint(1) }\n"
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	position := referencePosition(t, document, strings.Index(source, `text "`))
	edit, message := server.renameResult(document, position, "fmt")
	want := `cannot rename alias "text" to "fmt": the new name conflicts with another import`
	if edit != nil || message != want {
		t.Fatalf("alias collision = (%#v, %q), want %q", edit, message, want)
	}
}

func TestRenameModuleUpdatesOnlyInferredAliases(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	inferredPath := filepath.Join(root, "inferred.gox")
	explicitPath := filepath.Join(root, "explicit.gox")
	writeWorkspaceGOX(t, usersPath, "module users\nfunc Lookup() string { return \"George\" }\n")
	writeWorkspaceGOX(t, inferredPath, "module inferred\nimport \"./users.gox\"\nfunc Build() { _ = users.Lookup() }\n")
	writeWorkspaceGOX(t, explicitPath, "module explicit\nimport people \"./users.gox\"\nfunc Build() { _ = people.Lookup() }\n")
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, _ := server.symbols.Document(FileURI(usersPath))
	position := referencePosition(t, document, strings.Index(document.Text, "users"))
	edit, message := server.renameResult(document, position, "accounts")
	if message != "" || edit == nil || len(edit.Changes[document.URI]) != 1 || len(edit.Changes[FileURI(inferredPath)]) != 1 {
		t.Fatalf("module rename = (%#v, %q)", edit, message)
	}
	if _, changed := edit.Changes[FileURI(explicitPath)]; changed {
		t.Fatalf("module rename changed explicit alias: %#v", edit)
	}
}

func TestRenameRejectsLocalCollisionsAndNestedShadowing(t *testing.T) {
	source := `module page
func Card() gox.Component {
	name := "George"
	title := "Profile"
	if true {
		label := name
		_ = label
	}
	return <span>{{ name }} {{ title }}</span>
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	namePosition := referencePosition(t, document, strings.Index(source, `name :=`))
	for _, check := range []struct {
		name    string
		message string
	}{
		{name: "title", message: `cannot rename "name" to "title": "title" is already declared in this scope`},
		{name: "label", message: `cannot rename "name" to "label": the new name would introduce shadowing in a nested scope`},
	} {
		edit, message := server.renameResult(document, namePosition, check.name)
		if edit != nil || message != check.message {
			t.Fatalf("rename name to %s = (%#v, %q), want %q", check.name, edit, message, check.message)
		}
	}
}

func TestRenameAllowsSameLocalNameInUnrelatedSiblingScope(t *testing.T) {
	source := `module page
func Build(enabled bool) {
	if enabled {
		left := 1
		_ = left
	}
	if !enabled {
		right := 2
		_ = right
	}
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	position := referencePosition(t, document, strings.Index(source, `left :=`))
	edit := server.rename(document, position, "right")
	if edit == nil || len(edit.Changes[document.URI]) != 2 {
		t.Fatalf("sibling-scope rename = %#v", edit)
	}
}

func TestRenameRejectsTopLevelAndMemberCollisions(t *testing.T) {
	source := `module users
type User struct {
	Name string
	Age int
}
func Lookup() string { return "George" }
func Find() string { return "Milo" }
func (user User) Display() string { return user.Name }
func (user User) Label() string { return user.Name }
`
	document := Document{URI: "file:///project/users.gox", Path: "/project/users.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		oldName string
		newName string
	}{
		{oldName: "Lookup", newName: "Find"},
		{oldName: "Name", newName: "Age"},
		{oldName: "Display", newName: "Label"},
	}
	for _, item := range cases {
		position := referencePosition(t, document, strings.Index(source, item.oldName))
		if edit := server.rename(document, position, item.newName); edit != nil {
			t.Fatalf("rename %s to %s should conflict: %#v", item.oldName, item.newName, edit)
		}
	}
}

func TestRenameRejectsShadowingTemplateLoopBinding(t *testing.T) {
	source := `module page
func List(users []string) gox.Component {
	name := "George"
	return <ul><:for _ user range={{ users }}><li>{{ user }} {{ name }}</li></:for></ul>
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	position := referencePosition(t, document, strings.Index(source, `name :=`))
	if edit := server.rename(document, position, "user"); edit != nil {
		t.Fatalf("template-shadowing rename = %#v", edit)
	}
}

func TestServerProvidesPrepareRenameAndRename(t *testing.T) {
	uri := "file:///project/page.gox"
	source := "module page\nfunc Build() { name := \"George\"; _ = name }\n"
	document := Document{URI: uri, Path: "/project/page.gox", Text: source}
	position := referencePosition(t, document, strings.LastIndex(source, "name"))
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/prepareRename","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":` + referenceInt(position.Line) + `,"character":` + referenceInt(position.Character) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"textDocument/rename","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":` + referenceInt(position.Line) + `,"character":` + referenceInt(position.Character) + `},"newName":"title"}}`),
		framedMessage(`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	var prepareResult, renameResult json.RawMessage
	for _, response := range responses {
		switch string(response.ID) {
		case "2":
			prepareResult = response.Result
		case "3":
			renameResult = response.Result
		}
	}
	if !bytes.Contains(prepareResult, []byte(`"placeholder":"name"`)) || !bytes.Contains(renameResult, []byte(`"newText":"title"`)) {
		t.Fatalf("rename responses = %#v", responses)
	}
}

func TestServerReportsRenameConflictReason(t *testing.T) {
	uri := "file:///project/page.gox"
	source := "module page\nfunc Build() { name := \"George\"; title := \"Profile\"; _, _ = name, title }\n"
	document := Document{URI: uri, Path: "/project/page.gox", Text: source}
	position := referencePosition(t, document, strings.Index(source, "name :="))
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + uri + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/rename","params":{"textDocument":{"uri":"` + uri + `"},"position":{"line":` + referenceInt(position.Line) + `,"character":` + referenceInt(position.Character) + `},"newName":"title"}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	for _, response := range responses {
		if string(response.ID) != "2" {
			continue
		}
		if response.Error == nil || response.Error.Code != -32803 ||
			response.Error.Message != `cannot rename "name" to "title": "title" is already declared in this scope` {
			t.Fatalf("rename conflict response = %#v", response)
		}
		return
	}
	t.Fatal("missing rename conflict response")
}

func assertRenameOffsets(t *testing.T, document Document, edits []CompletionTextEdit, newName string, offsets ...int) {
	t.Helper()
	want := make(map[int]bool, len(offsets))
	for _, offset := range offsets {
		want[offset] = true
	}
	for _, edit := range edits {
		if edit.NewText != newName {
			t.Fatalf("rename edit = %#v", edit)
		}
		offset, err := document.Offset(edit.Range.Start)
		if err != nil || !want[offset] {
			t.Fatalf("unexpected rename edit = %#v (offset %d)", edit, offset)
		}
		delete(want, offset)
	}
	if len(want) != 0 {
		t.Fatalf("missing rename offsets = %#v", want)
	}
}
