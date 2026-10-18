package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPhase0RecoveryRegressionLocks freezes representative LSP behavior that
// must remain stable while compiler/runtime recovery work is in progress.
//
// This test intentionally exercises user-visible contracts rather than
// implementation details. Recovery phases must keep this test green.
func TestPhase0RecoveryRegressionLocks(t *testing.T) {
	t.Run("gox-sdk-public-api", phase0LockGoxSDKPublicAPI)
	t.Run("imported-gox-completion-boundary", phase0LockImportedGoxCompletion)
	t.Run("hover-definition-signature", phase0LockNavigationAndSignature)
	t.Run("rename", phase0LockRename)
	t.Run("semantic-diagnostics", phase0LockSemanticDiagnostics)
	t.Run("semantic-tokens", phase0LockSemanticTokens)
	t.Run("template-intelligence", phase0LockTemplateIntelligence)
	t.Run("passive-style-script", phase0LockPassiveEmbeddedContent)
	t.Run("client-diagnostics", phase0LockClientDiagnostics)
	t.Run("module-boundary", phase0LockModuleBoundary)
}

func phase0LockGoxSDKPublicAPI(t *testing.T) {
	root, pagePath, pageSource := goxSDKPublicAPIFixture(t)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})

	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	document.Text = strings.Replace(pageSource, "gox.Component", "gox.Co", 1)
	position, err := document.Position(strings.Index(document.Text, "gox.Co") + len("gox.Co"))
	if err != nil {
		t.Fatal(err)
	}
	items := server.completions(document, position).Items
	requireCompletionItem(t, items, "Component")
	assertCompletionMissing(t, items, "RuntimeBridge")
	assertCompletionMissing(t, items, "BindRuntime")

	original := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	if hover := hoverAtOffset(t, server, original, strings.Index(pageSource, "gox.Component")+1); hover == nil || !strings.Contains(hover.Contents.Value, "GOX SDK public API") {
		t.Fatalf("GOX SDK hover = %#v", hover)
	}
	locations := definitionLocationsAtOffset(t, server, original, strings.Index(pageSource, "Component")+1)
	if len(locations) != 1 || string(locations[0].URI) != string(FileURI(filepath.Join(root, "gox-sdk", "gox.go"))) {
		t.Fatalf("GOX SDK definition = %#v", locations)
	}
}

func phase0LockImportedGoxCompletion(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, usersPath, `module users
type User struct { Name string; age int }
func Lookup(id int) User { return User{} }
func hidden() string { return "" }
`)
	pageSource := `module page
import users "./users.gox"
func Build() { var _ users.User }
`
	writeWorkspaceGOX(t, pagePath, pageSource)

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	document.Text = strings.Replace(pageSource, "users.User", "users.", 1)
	items := completionItemsAtEnd(t, server, document, "users.")
	for _, label := range []string{"Lookup", "User"} {
		requireCompletionItem(t, items, label)
	}
	assertCompletionMissing(t, items, "hidden")
}

func phase0LockNavigationAndSignature(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	usersSource := `module users
type User struct { Name string }
func Lookup(id int, strict bool) (User, error) { return User{}, nil }
`
	pageSource := `module page
import users "./users.gox"
func Build() { _, _ = users.Lookup(10, false) }
`
	writeWorkspaceGOX(t, usersPath, usersSource)
	writeWorkspaceGOX(t, pagePath, pageSource)

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	lookupOffset := strings.Index(pageSource, "Lookup") + 1
	if hover := hoverAtOffset(t, server, document, lookupOffset); hover == nil || !strings.Contains(hover.Contents.Value, "func Lookup(id int, strict bool)") {
		t.Fatalf("Lookup hover = %#v", hover)
	}
	assertDefinitionURIAndOffset(t, server, document, lookupOffset, FileURI(usersPath), usersSource, strings.Index(usersSource, "Lookup"))
	help := signatureAtOffset(t, server, document, strings.Index(pageSource, "false)")+len("false"))
	assertSignature(t, help, "func Lookup(id int, strict bool) (User, error)", 1, "id int", "strict bool")
}

func phase0LockRename(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, usersPath, "module users\nfunc Lookup(id int) string { return \"George\" }\n")
	pageSource := "module page\nimport users \"./users.gox\"\nfunc Build() { _ = users.Lookup(1) }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	position := referencePosition(t, document, strings.Index(pageSource, "Lookup"))
	edit := server.rename(document, position, "Find")
	if edit == nil || len(edit.Changes[FileURI(usersPath)]) != 1 || len(edit.Changes[FileURI(pagePath)]) != 1 {
		t.Fatalf("cross-module rename = %#v", edit)
	}
}

func phase0LockSemanticDiagnostics(t *testing.T) {
	source := `module page
func Build(name string) {
	_ = missing
	_ = name
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "undefined-identifier", "undefined: missing", strings.Index(source, "missing"))
}

func phase0LockSemanticTokens(t *testing.T) {
	document := Document{Path: "/workspace/pages/index.gox", Text: `module page
func Card() gox.Component { return <span>George</span> }
func Render() gox.Component { value := "George"; return <main>{{ value }}<&Card /></main> }
`}
	tokens := semanticTokensForDocument(document)
	if len(tokens.Data) == 0 || len(tokens.Data)%5 != 0 {
		t.Fatalf("semantic token data = %#v", tokens.Data)
	}
	want := map[uint32]bool{
		semanticTokenTypeIndexes["function"]:     false,
		semanticTokenTypeIndexes["variable"]:     false,
		semanticTokenTypeIndexes["goxTag"]:       false,
		semanticTokenTypeIndexes["goxComponent"]: false,
	}
	for index := 0; index < len(tokens.Data); index += 5 {
		if _, ok := want[tokens.Data[index+3]]; ok {
			want[tokens.Data[index+3]] = true
		}
	}
	for tokenType, found := range want {
		if !found {
			t.Fatalf("semantic token type %d missing from %#v", tokenType, tokens.Data)
		}
	}
}

func phase0LockTemplateIntelligence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	indexedSource := `module page
import gox "github.com/gox-lang/gox"
client Menu react { src: "menu/menu.jsx" }
func Card(name string, count int, children gox.Component) gox.Component { return <article><&children /></article> }
func Render() gox.Component { return <main><&Card name="George" count={{ 1 }} /><@Menu /></main> }
`
	server := NewServer()
	indexCompletionDocument(t, server, path, indexedSource)

	componentLive := strings.Replace(indexedSource, `<&Card name="George" count={{ 1 }} />`, `<&Card name="George" co/*cursor*/ />`, 1)
	componentDocument := Document{URI: FileURI(path), Path: path, Text: componentLive}
	requireCompletionItem(t, lexicalItemsAtMarker(t, server, componentDocument, "/*cursor*/"), "count")

	sigilLive := strings.Replace(indexedSource, `<&Card name="George" count={{ 1 }} />`, `&Ca/*cursor*/`, 1)
	sigilDocument := Document{URI: FileURI(path), Path: path, Text: sigilLive}
	requireSigilCompletion(t, lexicalItemsAtMarker(t, server, sigilDocument, "/*cursor*/"), "&Card")

	clientLive := strings.Replace(indexedSource, `<@Menu />`, `@Me/*client*/`, 1)
	clientDocument := Document{URI: FileURI(path), Path: path, Text: clientLive}
	requireSigilCompletion(t, lexicalItemsAtMarker(t, server, clientDocument, "/*client*/"), "@Menu")
}

func phase0LockPassiveEmbeddedContent(t *testing.T) {
	source := `module page
import gox "github.com/gox-lang/gox"
func Card(value string) gox.Component {
	return <main><style>{{ missingStyle }}</style><script>{{ missingScript }}</script><div>{{ value }}</div></main>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"missingStyle", "missingScript"} {
		position := referencePosition(t, document, strings.Index(source, needle)+1)
		if hover := server.hover(document, position); hover != nil {
			t.Fatalf("hover inside passive embedded content = %#v", hover)
		}
		if definitions := server.definitionLocations(document, position); len(definitions) != 0 {
			t.Fatalf("definitions inside passive embedded content = %#v", definitions)
		}
	}
	for _, diagnostic := range server.diagnostics(document) {
		if strings.Contains(diagnostic.Message, "missingStyle") || strings.Contains(diagnostic.Message, "missingScript") {
			t.Fatalf("passive embedded diagnostic leaked: %#v", diagnostic)
		}
	}
}

func phase0LockClientDiagnostics(t *testing.T) {
	root := t.TempDir()
	writeClientDiagnosticProject(t, root)
	pagePath := filepath.Join(root, "pages", "index.gox")
	document := Document{
		URI:  FileURI(pagePath),
		Path: pagePath,
		Text: "module page\nclient Menu react {\n\tsrc: \"menu/menu.jsx\"\n}\n",
	}
	server := NewServer()

	diagnostics := server.diagnostics(document)
	if !hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) || hasDiagnosticCode(diagnostics, "missing-client-entry") {
		t.Fatalf("client diagnostics before framework install = %#v", diagnostics)
	}

	installClientFrameworkForDiagnostic(t, root, "react")
	diagnostics = server.diagnostics(document)
	if hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) || !hasDiagnosticCode(diagnostics, "missing-client-entry") {
		t.Fatalf("client diagnostics after framework install = %#v", diagnostics)
	}
}

func phase0LockModuleBoundary(t *testing.T) {
	root := t.TempDir()
	libraryPath := filepath.Join(root, "library.gox")
	pagePath := filepath.Join(root, "page.gox")
	librarySource := `module library
func Public() string { return "ok" }
func private() string { return "hidden" }
`
	pageSource := `module page
import library "./library.gox"
func Build() { _ = library.Public() }
`
	writeWorkspaceGOX(t, libraryPath, librarySource)
	writeWorkspaceGOX(t, pagePath, pageSource)

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	publicOffset := strings.Index(pageSource, "Public") + 1
	assertDefinitionURIAndOffset(t, server, document, publicOffset, FileURI(libraryPath), librarySource, strings.Index(librarySource, "Public"))

	completionDocument := document
	completionDocument.Text = strings.Replace(pageSource, "library.Public()", "library.", 1)
	items := completionItemsAtEnd(t, server, completionDocument, "library.")
	requireCompletionItem(t, items, "Public")
	assertCompletionMissing(t, items, "private")
}
