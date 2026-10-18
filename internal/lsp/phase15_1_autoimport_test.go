package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase15_1GoAutoImportRequestReusesPreparedCandidate(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nfunc Build() { str/*cursor*/ }\n"
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: strings.ReplaceAll(source, "/*cursor*/", "")}
	writeWorkspaceGOX(t, pagePath, document.Text)

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(document.Text, "str") + len("str")
	state := server.newGoAutoImportCompletionState(document, offset)
	if len(state.prepared) != 0 {
		t.Fatalf("fresh request prepared %d candidates; want 0", len(state.prepared))
	}

	items := server.autoImportGoPackageCompletions(document, lexicalCompletionContext{Prefix: "strings"}, state)
	item := requireCompletionItem(t, items, "strings")
	if len(item.AdditionalTextEdits) != 1 || item.AdditionalTextEdits[0].NewText != "\n\nimport strings \"strings\"\n" {
		t.Fatalf("strings auto-import = %#v", item)
	}
	prepared, ok := state.prepared["strings"]
	if !ok {
		t.Fatalf("strings candidate was not cached in request state: %#v", state.prepared)
	}
	count := len(state.prepared)

	again, ok := server.prepareGoAutoImportCandidate(document, state, GoPackageCandidate{Path: "strings", Name: "strings"})
	if !ok || again.Edit != prepared.Edit {
		t.Fatalf("prepared candidate was not reused: first=%#v second=%#v ok=%v", prepared, again, ok)
	}
	if len(state.prepared) != count {
		t.Fatalf("reusing candidate changed prepared count: got %d want %d", len(state.prepared), count)
	}
}

func TestPhase15_1GoAutoImportRequestPreservesAliasConflicts(t *testing.T) {
	root := t.TempDir()
	writeGoAutoImportFixture(t, root)
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nfunc Build() { strings := 1; _ = strings; str/*cursor*/ }\n"
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: strings.ReplaceAll(source, "/*cursor*/", "")}
	writeWorkspaceGOX(t, pagePath, document.Text)

	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	offset := strings.LastIndex(document.Text, "str") + len("str")
	state := server.newGoAutoImportCompletionState(document, offset)
	items := server.autoImportGoPackageCompletions(document, lexicalCompletionContext{Prefix: "strings"}, state)
	for _, item := range items {
		if item.Label == "strings" && len(item.AdditionalTextEdits) > 0 {
			t.Fatalf("local alias conflict received Go auto-import: %#v", item)
		}
	}
	if _, prepared := state.prepared["strings"]; prepared {
		t.Fatalf("conflicting strings candidate should not construct an import edit")
	}
}
