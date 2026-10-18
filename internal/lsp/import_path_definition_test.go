package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestImportDefinitionUsesTheWholeGOXLiteral(t *testing.T) {
	root := t.TempDir()
	userPath := filepath.Join(root, "user.gox")
	cardPath := filepath.Join(root, "card.gox")
	pagePath := filepath.Join(root, "page.gox")
	if err := os.WriteFile(userPath, []byte("module users\nfunc User() string { return \"user\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cardPath, []byte("module card\nfunc Card() string { return \"card\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pageSource := "module page\nimport (\n\t\"./user.gox\"\n\t\"./card.gox\"\n)\nfunc Build() {}\n"
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	for _, path := range []string{"./user.gox", "./card.gox"} {
		literal := `"` + path + `"`
		start := strings.Index(pageSource, literal)
		wantStart, _ := document.Position(start)
		wantEnd, _ := document.Position(start + len(literal))
		for cursor := start; cursor <= start+len(literal); cursor++ {
			position, _ := document.Position(cursor)
			links := server.importDefinitionLinks(document, position)
			if len(links) != 1 {
				t.Fatalf("%s definition at byte %d = %#v", path, cursor-start, links)
			}
			if links[0].OriginSelectionRange != (Range{Start: wantStart, End: wantEnd}) {
				t.Fatalf("%s origin range = %#v", path, links[0].OriginSelectionRange)
			}
			if links[0].TargetURI != FileURI(filepath.Join(root, filepath.Base(path))) {
				t.Fatalf("%s target = %q", path, links[0].TargetURI)
			}
		}
	}
}

func TestImportDefinitionUsesTheWholeGoPackageLiteral(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(root, "library")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "api.go"), []byte("package library\nfunc Build() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport \"example/library\"\nfunc Build() {}\n"
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	literal := `"example/library"`
	start := strings.Index(pageSource, literal)
	wantStart, _ := document.Position(start)
	wantEnd, _ := document.Position(start + len(literal))
	for cursor := start; cursor <= start+len(literal); cursor++ {
		position, _ := document.Position(cursor)
		links := server.importDefinitionLinks(document, position)
		if len(links) != 1 {
			t.Fatalf("Go import definition at byte %d = %#v", cursor-start, links)
		}
		if links[0].OriginSelectionRange != (Range{Start: wantStart, End: wantEnd}) {
			t.Fatalf("Go import origin range = %#v", links[0].OriginSelectionRange)
		}
		if links[0].TargetURI != FileURI(filepath.Join(packageDir, "api.go")) {
			t.Fatalf("Go import target = %q", links[0].TargetURI)
		}
	}
}

func TestDefinitionProtocolReturnsImportLocationLink(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "user.gox")
	pagePath := filepath.Join(root, "page.gox")
	if err := os.WriteFile(targetPath, []byte("module users\nfunc User() string { return \"user\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pageSource := "module page\nimport \"./user.gox\"\nfunc Build() {}\n"
	position := Position{Line: 1, Character: 10}
	request := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + FileURI(pagePath) + `","languageId":"gox","version":1,"text":` + mustJSONImportTest(t, pageSource) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"` + FileURI(pagePath) + `"},"position":{"line":` + strconv.Itoa(position.Line) + `,"character":` + strconv.Itoa(position.Character) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output strings.Builder
	if err := NewServer().Serve(strings.NewReader(request), &output); err != nil {
		t.Fatal(err)
	}
	for _, response := range decodeFramedResponses(t, output.String()) {
		if string(response.ID) != "2" {
			continue
		}
		var links []LocationLink
		if err := json.Unmarshal(response.Result, &links); err != nil {
			t.Fatal(err)
		}
		if len(links) != 1 || links[0].OriginSelectionRange != (Range{Start: Position{Line: 1, Character: 7}, End: Position{Line: 1, Character: 19}}) {
			t.Fatalf("definition links = %#v", links)
		}
		return
	}
	t.Fatal("definition response not found")
}

func mustJSONImportTest(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
