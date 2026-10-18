package lsp

import (
	"path/filepath"
	"testing"
)

func TestClientAdaptDiagnostics(t *testing.T) {
	for _, test := range []struct{ markup, code string }{
		{`<@Panel adapt="Top-Left" />`, "invalid-client-adapt"},
		{`<@Panel adapt={{ direction }} />`, "invalid-client-adapt"},
		{`<@Panel adapt="top" adapt="none" />`, "duplicate-client-adapt"},
		{`<div adapt="top" />`, "invalid-client-adapt"},
	} {
		t.Run(test.markup, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "page.gox")
			source := `module page
client Panel react { src: "panel.jsx" }
func Render() string { return ` + test.markup + ` }
`
			doc := Document{URI: FileURI(path), Path: path, Text: source}
			diagnostics := NewServer().diagnostics(doc)
			if !hasDiagnosticCode(diagnostics, test.code) {
				t.Fatalf("missing %s: %#v", test.code, diagnostics)
			}
		})
	}
}
