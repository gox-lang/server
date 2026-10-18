package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientSourceDiagnosticsDistinguishFrameworkInstallationFromMissingEntry(t *testing.T) {
	frameworks := []struct {
		name      string
		extension string
	}{
		{name: "react", extension: ".jsx"},
		{name: "preact", extension: ".jsx"},
		{name: "solid", extension: ".jsx"},
		{name: "vue", extension: ".vue"},
		{name: "svelte", extension: ".svelte"},
	}

	for _, framework := range frameworks {
		t.Run(framework.name, func(t *testing.T) {
			root := t.TempDir()
			writeClientDiagnosticProject(t, root)
			entry := "menu/menu" + framework.extension
			source := "module page\n\nclient Menu " + framework.name + " {\n\tsrc: \"" + entry + "\"\n}\n"
			pagePath := filepath.Join(root, "pages", "index.gox")
			document := Document{URI: FileURI(pagePath), Path: pagePath, Version: 1, Text: source}

			diagnostics := NewServer().diagnostics(document)
			frameworkDiagnostic := requireClientDiagnostic(t, diagnostics, diagnosticClientFrameworkNotInstalled)
			if !strings.Contains(frameworkDiagnostic.Message, "gox client add "+framework.name) {
				t.Fatalf("framework diagnostic message = %q", frameworkDiagnostic.Message)
			}
			assertDiagnosticText(t, document, frameworkDiagnostic, framework.name)
			if hasDiagnosticCode(diagnostics, "missing-client-entry") {
				t.Fatalf("missing-client-entry must not be reported before %s is installed: %#v", framework.name, diagnostics)
			}

			installClientFrameworkForDiagnostic(t, root, framework.name)
			document.Version++
			diagnostics = NewServer().diagnostics(document)
			if hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) {
				t.Fatalf("installed framework reported as missing: %#v", diagnostics)
			}
			entryDiagnostic := requireClientDiagnostic(t, diagnostics, "missing-client-entry")
			assertDiagnosticText(t, document, entryDiagnostic, entry)

			entryPath := filepath.Join(root, "client", framework.name, filepath.FromSlash(entry))
			if err := os.MkdirAll(filepath.Dir(entryPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(entryPath, []byte("export default {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			document.Version++
			diagnostics = NewServer().diagnostics(document)
			if hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) || hasDiagnosticCode(diagnostics, "missing-client-entry") {
				t.Fatalf("valid client source diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestClientFrameworkInstallationRequiresPackageJSONAndNodeModules(t *testing.T) {
	states := []struct {
		name        string
		packageJSON bool
		nodeModules bool
	}{
		{name: "neither"},
		{name: "package-only", packageJSON: true},
		{name: "node-modules-only", nodeModules: true},
		{name: "both", packageJSON: true, nodeModules: true},
	}

	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			root := t.TempDir()
			writeClientDiagnosticProject(t, root)
			frameworkRoot := filepath.Join(root, "client", "react")
			if err := os.MkdirAll(frameworkRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			if state.packageJSON {
				if err := os.WriteFile(filepath.Join(frameworkRoot, "package.json"), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if state.nodeModules {
				if err := os.MkdirAll(filepath.Join(frameworkRoot, "node_modules"), 0o700); err != nil {
					t.Fatal(err)
				}
			}

			source := "module page\nclient Menu react {\n\tsrc: \"menu/menu.jsx\"\n}\n"
			pagePath := filepath.Join(root, "pages", "index.gox")
			document := Document{URI: FileURI(pagePath), Path: pagePath, Text: source}
			diagnostics := NewServer().diagnostics(document)
			missingFramework := hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled)
			if want := !(state.packageJSON && state.nodeModules); missingFramework != want {
				t.Fatalf("client-framework-not-installed = %v, want %v; diagnostics=%#v", missingFramework, want, diagnostics)
			}
			if missingFramework && hasDiagnosticCode(diagnostics, "missing-client-entry") {
				t.Fatalf("framework installation diagnostic must take precedence: %#v", diagnostics)
			}
		})
	}
}

func TestClientSourceDiagnosticsRefreshWithoutDocumentVersionChange(t *testing.T) {
	root := t.TempDir()
	writeClientDiagnosticProject(t, root)
	pagePath := filepath.Join(root, "pages", "index.gox")
	source := "module page\nclient Menu react {\n\tsrc: \"menu/menu.jsx\"\n}\n"
	document := Document{URI: FileURI(pagePath), Path: pagePath, Version: 1, Text: source}
	server := NewServer()

	diagnostics := server.diagnostics(document)
	if !hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) {
		t.Fatalf("initial diagnostics = %#v", diagnostics)
	}

	installClientFrameworkForDiagnostic(t, root, "react")
	diagnostics = server.diagnostics(document)
	if hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) || !hasDiagnosticCode(diagnostics, "missing-client-entry") {
		t.Fatalf("diagnostics after framework install without document change = %#v", diagnostics)
	}

	entryPath := filepath.Join(root, "client", "react", "menu", "menu.jsx")
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, []byte("export default {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	diagnostics = server.diagnostics(document)
	if hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) || hasDiagnosticCode(diagnostics, "missing-client-entry") {
		t.Fatalf("diagnostics after entry creation without document change = %#v", diagnostics)
	}
	snapshot := server.analysisSnapshot(document)
	if err := os.Remove(entryPath); err != nil {
		t.Fatal(err)
	}
	if diagnostics := server.diagnostics(document); !hasDiagnosticCode(diagnostics, "missing-client-entry") {
		t.Fatalf("diagnostics after entry deletion = %#v", diagnostics)
	}
	if err := os.Remove(filepath.Join(root, "client", "react", "node_modules")); err != nil {
		t.Fatal(err)
	}
	diagnostics = server.diagnostics(document)
	if !hasDiagnosticCode(diagnostics, diagnosticClientFrameworkNotInstalled) || hasDiagnosticCode(diagnostics, "missing-client-entry") {
		t.Fatalf("diagnostics after framework removal = %#v", diagnostics)
	}
	if server.analysisSnapshot(document) != snapshot {
		t.Fatal("filesystem diagnostics rebuilt the semantic snapshot")
	}
}

func writeClientDiagnosticProject(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/clientdiagnostic\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func installClientFrameworkForDiagnostic(t *testing.T, root, framework string) {
	t.Helper()
	frameworkRoot := filepath.Join(root, "client", framework)
	if err := os.MkdirAll(filepath.Join(frameworkRoot, "node_modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frameworkRoot, "package.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireClientDiagnostic(t *testing.T, diagnostics []Diagnostic, code string) Diagnostic {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return diagnostic
		}
	}
	t.Fatalf("missing diagnostic %q in %#v", code, diagnostics)
	return Diagnostic{}
}

func hasDiagnosticCode(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func assertDiagnosticText(t *testing.T, document Document, diagnostic Diagnostic, want string) {
	t.Helper()
	start, err := document.Offset(diagnostic.Range.Start)
	if err != nil {
		t.Fatal(err)
	}
	end, err := document.Offset(diagnostic.Range.End)
	if err != nil {
		t.Fatal(err)
	}
	if got := document.Text[start:end]; got != want {
		t.Fatalf("diagnostic range text = %q, want %q", got, want)
	}
}
