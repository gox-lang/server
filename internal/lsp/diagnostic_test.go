package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticsUseCompilerContractAndUTF16Ranges(t *testing.T) {
	document := Document{
		URI:     "file:///workspace/pages/index.gox",
		Path:    "/workspace/pages/index.gox",
		Version: 7,
		Text: `module main

// 😀
client Menu React {
	src: "menu.jsx"
}
`,
	}
	diagnostics := Diagnostics(document)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	diagnostic := diagnostics[0]
	if diagnostic.Code != "invalid-client-framework-case" || diagnostic.Source != "gox" || diagnostic.Severity != diagnosticSeverityError {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	if diagnostic.Range.Start.Line != 3 || diagnostic.Range.Start.Character != 12 {
		t.Fatalf("range = %#v", diagnostic.Range)
	}
}

func TestDiagnosticsAreEmptyForValidDocument(t *testing.T) {
	document := Document{Path: "/workspace/pages/index.gox", Text: "module main\n"}
	if diagnostics := Diagnostics(document); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestDiagnosticsReportUnsafeGoroutineAsError(t *testing.T) {
	source := `module main

func worker() {}

func GET() {
    go worker()
}
`
	document := Document{
		URI:     "file:///workspace/pages/index.gox",
		Path:    "/workspace/pages/index.gox",
		Version: 1,
		Text:    source,
	}
	diagnostics := Diagnostics(document)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	diagnostic := diagnostics[0]
	if diagnostic.Code != "unsafe-goroutine" || diagnostic.Source != "gox" || diagnostic.Severity != diagnosticSeverityError {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	if !strings.HasPrefix(diagnostic.Message, "goroutine has no panic recovery and may terminate GOX Server") {
		t.Fatalf("message = %q", diagnostic.Message)
	}
	if diagnostic.Range.Start.Line != 5 || diagnostic.Range.Start.Character != 4 || diagnostic.Range.End.Line != 5 || diagnostic.Range.End.Character != 6 {
		t.Fatalf("range = %#v, want token go on line 6", diagnostic.Range)
	}
}

func TestDiagnosticsAcceptProtectedGoroutine(t *testing.T) {
	source := `module main

func worker() {
    defer func() { recover() }()
}

func GET() {
    go worker()
}
`
	document := Document{Path: "/workspace/pages/index.gox", Text: source}
	if diagnostics := Diagnostics(document); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestSemanticDiagnosticsReportUndefinedIdentifiersInGoAndTemplates(t *testing.T) {
	source := `module page
func Card(name string) gox.Component {
	_ = missing
	return <span>{{ absent }} {{ name }}</span>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	if len(diagnostics) != 3 {
		t.Fatalf("semantic diagnostics = %#v", diagnostics)
	}
	want := map[string]int{
		"undefined: missing": strings.Index(source, "missing"),
		"undefined: absent":  strings.Index(source, "absent"),
		"undefined: gox":     strings.Index(source, "gox.Component"),
	}
	for _, diagnostic := range diagnostics {
		offset, err := document.Offset(diagnostic.Range.Start)
		if err != nil || diagnostic.Code != "undefined-identifier" || diagnostic.Source != "gox" || want[diagnostic.Message] != offset {
			t.Errorf("diagnostic = %#v at %d", diagnostic, offset)
		}
		delete(want, diagnostic.Message)
	}
	if len(want) != 0 {
		t.Fatalf("missing diagnostics = %#v", want)
	}
}

func TestSemanticDiagnosticsDoNotReportQualifiedSelectors(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), `module users
func Lookup(name string) string { return name }
`)
	source := `module page
import (
	users "./users.gox"
	"strings"
)
func Build(name string) {
	_ = users.Lookup(name)
	_ = strings.TrimSpace(name)
}
`
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, pagePath, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	if diagnostics := server.diagnostics(document); len(diagnostics) != 0 {
		t.Fatalf("qualified selector diagnostics = %#v", diagnostics)
	}
}
