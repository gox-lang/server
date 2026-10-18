package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGoxAutoImportCompletesModuleAndAddsImport(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, usersPath, "module users\ntype User struct { Name string }\n")
	pageSource := "module page\nfunc Build() { use/*cursor*/ }\n"
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(pageSource, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "users")
	if item.InsertText != "users" || len(item.AdditionalTextEdits) != 1 || item.AdditionalTextEdits[0].NewText != "\n\nimport users \"users.gox\"\n" {
		t.Fatalf("module auto-import = %#v", item)
	}
}

func TestGoxAutoImportCompletesQualifiedMembersInsideImportBlock(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, usersPath, `module users
type User struct { Name string }
func Split(name string) string { return name }
func private() {}
`)
	pageSource := `module page
import (
	gox "github.com/gox-lang/gox"
)
func Build() { var _ users.Us/*cursor*/; _ = gox.Component(nil) }
`
	writeWorkspaceGOX(t, pagePath, strings.ReplaceAll(pageSource, "/*cursor*/", ""))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "User")
	if len(item.AdditionalTextEdits) != 1 || item.AdditionalTextEdits[0].NewText != "\tusers \"users.gox\"\n" {
		t.Fatalf("member auto-import = %#v", item)
	}
	items := lexicalItemsAtMarker(t, server, Document{URI: document.URI, Path: document.Path, Text: strings.Replace(pageSource, "users.Us/*cursor*/", "users.pr/*cursor*/", 1)}, "/*cursor*/")
	assertCompletionMissing(t, items, "private")
}

func TestGoxAutoImportCompletesExternalComponents(t *testing.T) {
	root := t.TempDir()
	cardPath := filepath.Join(root, "components", "card.gox")
	pagePath := filepath.Join(root, "pages", "page.gox")
	writeWorkspaceGOX(t, cardPath, `module card
import gox "github.com/gox-lang/gox"
func Card(name string, children gox.Component) gox.Component { return <article>{{ name }}<&children /></article> }
func private() gox.Component { return <span>Private</span> }
`)
	pageSource := `module page
import gox "github.com/gox-lang/gox"
func Render() gox.Component { return <main>&Ca/*cursor*/</main> }
`
	writeWorkspaceGOX(t, pagePath, strings.Replace(pageSource, "&Ca/*cursor*/", "", 1))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	item := requireSigilCompletion(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "&card.Card")
	want := `<&card.Card name={{ ${1} }}>${2}</&card.Card>`
	if item.TextEdit == nil || item.TextEdit.NewText != want || len(item.AdditionalTextEdits) != 1 ||
		item.AdditionalTextEdits[0].NewText != "import (\n\tgox \"github.com/gox-lang/gox\"\n\tcard \"../components/card.gox\"\n)" {
		t.Fatalf("component auto-import = %#v", item)
	}
	assertCompletionMissing(t, lexicalItemsAtMarker(t, server, Document{URI: document.URI, Path: document.Path, Text: strings.Replace(pageSource, "&Ca/*cursor*/", "&pr/*cursor*/", 1)}, "/*cursor*/"), "&card.private")
}

func TestGoxAutoImportExplicitAliasKeepsImportNavigationStableDuringIncompleteTemplate(t *testing.T) {
	root := t.TempDir()
	cardPath := filepath.Join(root, "components", "card.gox")
	pagePath := filepath.Join(root, "pages", "page.gox")
	writeWorkspaceGOX(t, cardPath, "module card\nfunc Card() string { return \"Card\" }\n")
	valid := `module page
import (
	gox "github.com/gox-lang/gox"
	card "../components/card.gox"
)
func Render() gox.Component { return <main><&card.Card name={{ "George" }} /></main> }
`
	writeWorkspaceGOX(t, pagePath, valid)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	document.Text = strings.Replace(valid, `"George"`, "", 1)
	literal := `"../components/card.gox"`
	start := strings.Index(document.Text, literal)
	position, err := document.Position(start + len(literal)/2)
	if err != nil {
		t.Fatal(err)
	}
	links := server.importDefinitionLinks(document, position)
	if len(links) != 1 || links[0].TargetURI != FileURI(cardPath) {
		t.Fatalf("import definition links = %#v", links)
	}
	wantStart, _ := document.Position(start)
	wantEnd, _ := document.Position(start + len(literal))
	if links[0].OriginSelectionRange != (Range{Start: wantStart, End: wantEnd}) {
		t.Fatalf("origin selection range = %#v", links[0].OriginSelectionRange)
	}
	end, _ := document.Position(len(document.Text))
	if hints := server.importAliasInlayHints(document, Range{Start: Position{}, End: end}); len(hints) != 0 {
		t.Fatalf("explicit imports produced alias hints: %#v", hints)
	}
}

func TestGoxAutoImportDoesNotDuplicateImportedModule(t *testing.T) {
	root := t.TempDir()
	cardPath := filepath.Join(root, "card.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, cardPath, `module card
import gox "github.com/gox-lang/gox"
func Card() gox.Component { return <article>Card</article> }
`)
	pageSource := `module page
import (
	"card.gox"
	gox "github.com/gox-lang/gox"
)
func Render() gox.Component { return <main>&card.Ca/*cursor*/</main> }
`
	writeWorkspaceGOX(t, pagePath, strings.Replace(pageSource, "&card.Ca/*cursor*/", "<&card.Card />", 1))
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}

	item := requireSigilCompletion(t, lexicalItemsAtMarker(t, server, document, "/*cursor*/"), "&card.Card")
	if len(item.AdditionalTextEdits) != 0 {
		t.Fatalf("existing import produced edits: %#v", item)
	}
}
