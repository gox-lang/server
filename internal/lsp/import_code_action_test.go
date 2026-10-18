package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrganizeImportsBuildsExplicitBlockAndRemovesUnusedImports(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\ntype User struct { Name string }\n")
	pagePath := filepath.Join(root, "page.gox")
	source := `module page
import fmt "fmt"
import (
	unused "bytes"
	users "users.gox"
	strings "strings"
)
func Build() { _, _, _ = fmt.Sprintf, users.User{}, strings.TrimSpace }
`
	writeWorkspaceGOX(t, pagePath, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	edit, ok := server.organizeImportsEdit(document, true)
	if !ok {
		t.Fatal("organize imports did not return an edit")
	}
	want := `import (
	fmt "fmt"
	strings "strings"
	users "users.gox"
)`
	if edit.NewText != want {
		t.Fatalf("organized imports = %q, want %q", edit.NewText, want)
	}
	start, _ := document.Offset(edit.Range.Start)
	end, _ := document.Offset(edit.Range.End)
	if source[start:end] != source[strings.Index(source, "import fmt"):strings.Index(source, "func Build")-1] {
		t.Fatalf("organize range selected %q", source[start:end])
	}
}

func TestImportCodeActionsAddMissingGoPackageAndRemoveUnusedImports(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nimport bytes \"bytes\"\nfunc Build() { _ = strings.TrimSpace(\" George \") }\n"
	writeWorkspaceGOX(t, pagePath, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	diagnostics := server.diagnostics(document)
	missing := requireDiagnosticCode(t, diagnostics, "undefined-identifier", "undefined: strings")
	unused := requireDiagnosticCode(t, diagnostics, "unused-import", "")
	actions := server.codeActions(document, CodeActionParams{Context: CodeActionContext{Diagnostics: []Diagnostic{missing, unused}}})
	assertCodeActionEdit(t, actions, "Import strings from strings", "strings \"strings\"")
	assertCodeActionEdit(t, actions, "Remove unused imports", "")
}

func TestServerAdvertisesAndReturnsOrganizeImportsCodeAction(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	source := "module page\nimport strings \"strings\"\nimport fmt \"fmt\"\nfunc Build() { _, _ = strings.TrimSpace, fmt.Sprintf }\n"
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + FileURI(path) + `","languageId":"gox","version":1,"text":` + string(encoded) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/codeAction","params":{"textDocument":{"uri":"` + FileURI(path) + `"},"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"context":{"diagnostics":[],"only":["source.organizeImports"]}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	var initializeResult json.RawMessage
	var codeActionResult json.RawMessage
	for _, response := range responses {
		switch string(response.ID) {
		case "1":
			initializeResult = response.Result
		case "2":
			codeActionResult = response.Result
		}
	}
	if !bytes.Contains(initializeResult, []byte(`"codeActionProvider"`)) || len(codeActionResult) == 0 {
		t.Fatalf("responses = %#v", responses)
	}
	var actions []CodeAction
	if err := json.Unmarshal(codeActionResult, &actions); err != nil {
		t.Fatal(err)
	}
	assertCodeActionEdit(t, actions, "Organize Imports", "import (\n")
}

func requireDiagnosticCode(t *testing.T, diagnostics []Diagnostic, code, message string) Diagnostic {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && (message == "" || diagnostic.Message == message) {
			return diagnostic
		}
	}
	t.Fatalf("missing diagnostic %s %q in %#v", code, message, diagnostics)
	return Diagnostic{}
}

func assertCodeActionEdit(t *testing.T, actions []CodeAction, title, contains string) {
	t.Helper()
	for _, action := range actions {
		if action.Title != title || action.Edit == nil {
			continue
		}
		for _, edits := range action.Edit.Changes {
			for _, edit := range edits {
				if strings.Contains(edit.NewText, contains) {
					return
				}
			}
		}
	}
	t.Fatalf("missing code action %q containing %q in %#v", title, contains, actions)
}
