package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClientWorkspaceFileChangeRepublishesOpenProjectDiagnostics(t *testing.T) {
	root := t.TempDir()
	writeClientDiagnosticProject(t, root)
	pagePath := filepath.Join(root, "pages", "index.gox")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := "module page\nclient Menu react {\n\tsrc: \"menu/menu.jsx\"\n}\n"
	server := NewServer()
	server.rememberWorkspaceRoot(root)
	uri := FileURI(pagePath)
	if err := server.documents.Open(TextDocumentItem{URI: uri, LanguageID: "gox", Version: 1, Text: source}); err != nil {
		t.Fatal(err)
	}

	initial, ok := server.documents.Get(uri)
	if !ok {
		t.Fatal("open document missing")
	}
	if diagnostics := server.diagnostics(initial); !hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) {
		t.Fatalf("initial diagnostics = %#v", diagnostics)
	}

	installClientFrameworkForDiagnostic(t, root, "react")
	var output bytes.Buffer
	conn := newConnection(bytes.NewReader(nil), &output)
	packageURI := FileURI(filepath.Join(root, "client", "react", "package.json"))
	if err := server.publishWorkspaceFileDiagnostics(conn, packageURI, false); err != nil {
		t.Fatal(err)
	}
	diagnostics := lastPublishedDiagnostics(t, output.String(), uri)
	if hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) || !hasDiagnosticCode(diagnostics, "missing-client-entry") {
		t.Fatalf("diagnostics after framework filesystem event = %#v", diagnostics)
	}

	entryPath := filepath.Join(root, "client", "react", "menu", "menu.jsx")
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, []byte("export default {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := server.publishWorkspaceFileDiagnostics(conn, FileURI(entryPath), false); err != nil {
		t.Fatal(err)
	}
	diagnostics = lastPublishedDiagnostics(t, output.String(), uri)
	if hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) || hasDiagnosticCode(diagnostics, "missing-client-entry") {
		t.Fatalf("diagnostics after entry filesystem event = %#v", diagnostics)
	}
	if err := os.Remove(entryPath); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := server.publishWorkspaceFileDiagnostics(conn, FileURI(entryPath), false); err != nil {
		t.Fatal(err)
	}
	diagnostics = lastPublishedDiagnostics(t, output.String(), uri)
	if !hasDiagnosticCode(diagnostics, "missing-client-entry") {
		t.Fatalf("diagnostics after deleting entry = %#v", diagnostics)
	}
}

func TestClientWorkspaceRootForURIRecognizesOnlySupportedFrameworks(t *testing.T) {
	root := t.TempDir()
	writeClientDiagnosticProject(t, root)
	server := NewServer()

	for _, framework := range []string{"react", "preact", "solid", "vue", "svelte"} {
		uri := FileURI(filepath.Join(root, "client", framework, "menu", "entry.jsx"))
		got, ok := server.clientWorkspaceRootForURI(uri)
		if !ok || !samePath(got, root) {
			t.Fatalf("clientWorkspaceRootForURI(%s) = %q, %v; want %q, true", framework, got, ok, root)
		}
	}
	if got, ok := server.clientWorkspaceRootForURI(FileURI(filepath.Join(root, "client", "unknown", "entry.js"))); ok || got != "" {
		t.Fatalf("unsupported framework root = %q, %v", got, ok)
	}
}

func lastPublishedDiagnostics(t *testing.T, stream, uri string) []Diagnostic {
	t.Helper()
	messages := decodeFramedMessages(t, stream)
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Method != "textDocument/publishDiagnostics" {
			continue
		}
		var params PublishDiagnosticsParams
		if err := json.Unmarshal(messages[i].Params, &params); err != nil {
			t.Fatal(err)
		}
		if params.URI == uri {
			return params.Diagnostics
		}
	}
	t.Fatalf("missing diagnostics publication for %s in %q", uri, stream)
	return nil
}
