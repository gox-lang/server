package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticDiagnosticsValidateImportedTemplateComponents(t *testing.T) {
	root := t.TempDir()
	cardSource := `module card
func Cardes(name string) gox.Component { return <article>{{ name }}</article> }
func privateCard() gox.Component { return <article>private</article> }
func Helper() string { return "helper" }
`
	pageSource := `module page
import card "./card.gox"
func Build(user string) gox.Component {
	return <main>
		<&card.Cardes name={{ user }} />
		<&card.Card name={{ user }} />
		<&card.privateCard />
		<&card.Helper />
	</main>
}
`
	if err := os.WriteFile(filepath.Join(root, "card.gox"), []byte(cardSource), 0o600); err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	diagnostics := server.diagnostics(document)
	want := map[string]struct {
		code   string
		offset int
	}{
		"undefined: gox":                                         {"undefined-identifier", strings.Index(pageSource, "gox.Component")},
		"undefined component: card.Card":                         {"undefined-component", strings.Index(pageSource, "card.Card name") + len("card.")},
		"cannot refer to unexported component: card.privateCard": {"unexported-component", strings.Index(pageSource, "privateCard")},
		"card.Helper is not a GOX component":                     {"invalid-component", strings.Index(pageSource, "Helper")},
	}
	assertTemplateReferenceDiagnostics(t, document, diagnostics, want)
}

func TestSemanticDiagnosticsValidateLocalTemplateComponents(t *testing.T) {
	root := t.TempDir()
	frameworkRoot := filepath.Join(root, "client", "react")
	if err := os.MkdirAll(filepath.Join(frameworkRoot, "node_modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frameworkRoot, "package.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frameworkRoot, "menu.jsx"), []byte("export default {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	source := `module page
client Menu react { src: "menu.jsx" }
func Card() gox.Component { return <article>Card</article> }
func Build() gox.Component {
	menu := <nav>Menu</nav>
	return <main><&Card/><&menu/><&Missing/><&Menu/><@Menu/></main>
}
`
	pagePath := filepath.Join(root, "page.gox")
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	diagnostics = assertAndRemoveUndefinedGoxDiagnostics(t, document, diagnostics,
		strings.Index(source, "gox.Component"), strings.LastIndex(source, "gox.Component"))
	want := map[string]struct {
		code   string
		offset int
	}{
		"undefined component: Missing": {"undefined-component", strings.Index(source, "Missing")},
		"Menu is not a GOX component":  {"invalid-component", strings.Index(source, "<&Menu") + len("<&")},
	}
	assertTemplateReferenceDiagnostics(t, document, diagnostics, want)
}

func assertTemplateReferenceDiagnostics(t *testing.T, document Document, diagnostics []Diagnostic, want map[string]struct {
	code   string
	offset int
}) {
	t.Helper()
	if len(diagnostics) != len(want) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		expected, exists := want[diagnostic.Message]
		offset, err := document.Offset(diagnostic.Range.Start)
		if !exists || err != nil || diagnostic.Code != expected.code || diagnostic.Source != "gox" || offset != expected.offset {
			t.Errorf("diagnostic = %#v at %d", diagnostic, offset)
		}
		delete(want, diagnostic.Message)
	}
	if len(want) != 0 {
		t.Fatalf("missing diagnostics = %#v", want)
	}
}
