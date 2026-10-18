package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticDiagnosticsReportUnresolvedGoxImportAndUndefinedAlias(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build() {
	_, _ = users.Lookup("diez", false)
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	diagnostics := server.diagnostics(document)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	diagnostic := diagnostics[0]
	if diagnostic.Code != "unresolved-gox-import" || diagnostic.Message != `cannot resolve GOX import "./users.gox"` {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	assertDiagnosticOffsets(t, document, diagnostic, strings.Index(pageSource, `"./users.gox"`), len(`"./users.gox"`))
	aliasDiagnostic := diagnostics[1]
	if aliasDiagnostic.Code != "undefined-identifier" || aliasDiagnostic.Message != "undefined: users" {
		t.Fatalf("alias diagnostic = %#v", aliasDiagnostic)
	}
	assertDiagnosticOffsets(t, document, aliasDiagnostic, strings.Index(pageSource, "users.Lookup"), len("users"))
}

func TestSemanticDiagnosticsReportInvalidImportedGoxModule(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\nfunc Lookup(\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport users \"./users.gox\"\nfunc Build() {}\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	diagnostics := server.diagnostics(document)
	if len(diagnostics) != 1 || diagnostics[0].Code != "invalid-gox-import" ||
		!strings.Contains(diagnostics[0].Message, `cannot load GOX import "./users.gox"`) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestSemanticDiagnosticsReportGoxImportResolvingToDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "users.gox"), 0o755); err != nil {
		t.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport users \"./users.gox\"\nfunc Build() {}\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	diagnostics := server.diagnostics(document)
	if len(diagnostics) != 1 || diagnostics[0].Code != "invalid-gox-import" ||
		diagnostics[0].Message != `GOX import "./users.gox" resolves to a directory` {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestSemanticDiagnosticsReportUnresolvedGoImport(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport missing \"example/missing\"\nfunc Build() {}\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	diagnostics := server.diagnostics(document)
	if len(diagnostics) != 1 || diagnostics[0].Code != "unresolved-go-import" ||
		diagnostics[0].Message != `cannot resolve Go import "example/missing"` {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	assertDiagnosticOffsets(t, document, diagnostics[0], strings.Index(pageSource, `"example/missing"`), len(`"example/missing"`))
}

func TestSemanticDiagnosticsIgnoreIncompleteKnownGoImportPath(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), "package register\nfunc Register() {}\n")
	pagePath := filepath.Join(root, "pages", "index.gox")
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})

	for _, importPath := range []string{
		"bil",
		"billing",
		"gox-",
		"gox-editor",
		"gox-editor-test",
		"gox-editor-test/bi",
	} {
		t.Run(importPath, func(t *testing.T) {
			pageSource := "module page\nimport \"" + importPath + "\"\nfunc Build() {}\n"
			writeWorkspaceGOX(t, pagePath, pageSource)
			document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
			for _, diagnostic := range server.importResolutionDiagnostics(document) {
				if diagnostic.Code == "unresolved-go-import" {
					t.Fatalf("unexpected unresolved diagnostic: %#v", diagnostic)
				}
			}
		})
	}
}

func TestSemanticDiagnosticsAcceptResolvedGoxImport(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\nfunc Lookup(name string, enabled bool) (string, bool) { return name, enabled }\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport users \"./users.gox\"\nfunc Build() { _, _ = users.Lookup(\"diez\", false) }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	if diagnostics := server.diagnostics(document); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func assertDiagnosticOffsets(t *testing.T, document Document, diagnostic Diagnostic, start, length int) {
	t.Helper()
	gotStart, startErr := document.Offset(diagnostic.Range.Start)
	gotEnd, endErr := document.Offset(diagnostic.Range.End)
	if startErr != nil || endErr != nil {
		t.Fatalf("diagnostic range cannot be converted: %v, %v", startErr, endErr)
	}
	if gotStart != start || gotEnd != start+length {
		t.Fatalf("diagnostic offsets = %d:%d, want %d:%d", gotStart, gotEnd, start, start+length)
	}
}
