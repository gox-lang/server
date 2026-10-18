package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStructLiteralFieldCompletion(t *testing.T) {
	for _, tc := range []struct{ name, body, prefix, want string }{
		{"empty", "var Config = server.Config{\n/*cursor*/\n}", "", "Cluster"},
		{"partial missing comma", "func Build() server.Config { return server.Config{\nClust/*cursor*/\nDev: server.DevConfig{},\n} }", "Clust", "Cluster"},
		{"nested", "var Config = server.Config{Dev: server.DevConfig{ En/*cursor*/ }}", "En", "Enabled"},
		{"elided slice", "var Config = []server.Config{{ Cl/*cursor*/ }}", "Cl", "Cluster"},
		{"used field", "var Config = server.Config{Dev: server.DevConfig{}, /*cursor*/}", "", "Cluster"},
		{"existing comma", "var Config = server.Config{Cl/*cursor*/,}", "Cl", "Cluster"},
		{"existing colon", "var Config = server.Config{Clust/*cursor*/: nil}", "Clust", "Cluster"},
		{"local type", "type Local struct { Name string }; var x = Local{ Na/*cursor*/ }", "Na", "Name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeGoAutoImportFixture(t, root)
			writeGoPackageHoverFile(t, filepath.Join(root, "sdk", "server", "config.go"), "package server\ntype Config struct { Cluster any; Dev DevConfig; secret bool }; type DevConfig struct { Enabled bool }\n")
			path := filepath.Join(root, "app", "config.gox")
			source := "module app\nimport server \"github.com/gox-lang/gox/server\"\n" + tc.body + "\n"
			writeWorkspaceGOX(t, path, strings.ReplaceAll(source, "/*cursor*/", ""))
			srv := NewServer()
			srv.indexWorkspaceRoots([]string{root})
			items := lexicalItemsAtMarker(t, srv, Document{URI: FileURI(path), Path: path, Text: source}, "/*cursor*/")
			item := requireCompletionItem(t, items, tc.want)
			if tc.name == "existing colon" && item.TextEdit.NewText != "Cluster" {
				t.Fatalf("colon duplicated: %#v", item)
			}
			if tc.name == "existing comma" && strings.HasSuffix(item.TextEdit.NewText, ",") {
				t.Fatal("comma duplicated")
			}
			if item.Kind != 5 || item.TextEdit == nil || len(item.AdditionalTextEdits) != 0 {
				t.Fatalf("not a field: %#v", item)
			}
			for _, candidate := range items {
				if tc.name == "used field" && candidate.Label == "Dev" {
					t.Fatal("already assigned field suggested")
				}
				if candidate.Label == "secret" || candidate.Kind != 5 {
					t.Fatalf("unexpected suggestion: %#v", candidate)
				}
			}
		})
	}
}
