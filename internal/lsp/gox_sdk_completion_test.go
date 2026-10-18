package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSDKUnqualifiedCompletionImportedAndAutoImport(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	writeGoPackageHoverFile(t, filepath.Join(root, "sdk", "extra.go"), "package gox\ntype Page interface{}\ntype Storage struct{}\ntype WebSocket interface{}\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "sdk", "server", "config.go"), "package server\ntype Config struct{}\n")
	for _, tc := range []struct {
		name, imports, prefix, label, insert, path string
		edits                                      int
	}{
		{"page imported", "import gox \"github.com/gox-lang/gox\"", "Pag", "Page", "gox.Page", "github.com/gox-lang/gox", 0},
		{"storage auto", "", "Stor", "Storage", "gox.Storage", "github.com/gox-lang/gox", 1},
		{"socket alias", "import sdk \"github.com/gox-lang/gox\"", "Webso", "WebSocket", "sdk.WebSocket", "github.com/gox-lang/gox", 0},
		{"config auto", "", "Conf", "Config", "server.Config", "github.com/gox-lang/gox/server", 1},
		{"config imported", "import config \"github.com/gox-lang/gox/server\"", "Conf", "Config", "config.Config", "github.com/gox-lang/gox/server", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, "page.gox")
			source := "module page\n" + tc.imports + "\nfunc Build() { var value " + tc.prefix + "/*cursor*/; _ = value }\n"
			writeWorkspaceGOX(t, path, strings.ReplaceAll(source, "/*cursor*/", ""))
			server := NewServer()
			server.indexWorkspaceRoots([]string{root})
			item := requireCompletionItem(t, lexicalItemsAtMarker(t, server, Document{URI: FileURI(path), Path: path, Text: source}, "/*cursor*/"), tc.label)
			if item.InsertText != tc.insert || len(item.AdditionalTextEdits) != tc.edits {
				t.Fatalf("unexpected completion: %#v", item)
			}
			if !strings.HasPrefix(item.SortText, "3_") {
				t.Fatalf("SDK priority missing: %s", item.SortText)
			}
			if tc.edits > 0 && !strings.Contains(item.AdditionalTextEdits[0].NewText, tc.path) {
				t.Fatal("wrong auto-import")
			}
		})
	}
}
