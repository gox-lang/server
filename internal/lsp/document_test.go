package lsp

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDocumentStoreLifecycleUsesLatestFullContent(t *testing.T) {
	store := NewDocumentStore()
	uri := "file:///workspace/pages/index.gox"
	if err := store.Open(TextDocumentItem{URI: uri, LanguageID: "gox", Version: 1, Text: "George"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Change(VersionedTextDocumentIdentifier{URI: uri, Version: 2}, []TextDocumentContentChangeEvent{{Text: "Hello, George"}}); err != nil {
		t.Fatal(err)
	}
	document, ok := store.Get(uri)
	if !ok || document.Version != 2 || document.Text != "Hello, George" || document.LanguageID != "gox" {
		t.Fatalf("document = %#v, %v", document, ok)
	}
	if store.Len() != 1 || !store.Close(uri) || store.Len() != 0 {
		t.Fatalf("unexpected close state: len=%d", store.Len())
	}
}

func TestDocumentStoreRejectsStaleAndIncrementalChanges(t *testing.T) {
	store := NewDocumentStore()
	uri := "file:///workspace/pages/index.gox"
	if err := store.Open(TextDocumentItem{URI: uri, Version: 4, Text: "current"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Change(VersionedTextDocumentIdentifier{URI: uri, Version: 4}, []TextDocumentContentChangeEvent{{Text: "stale"}}); !errors.Is(err, ErrDocumentVersionNotIncreasing) {
		t.Fatalf("stale error = %v", err)
	}
	changeRange := &Range{Start: Position{}, End: Position{Character: 1}}
	if err := store.Change(VersionedTextDocumentIdentifier{URI: uri, Version: 5}, []TextDocumentContentChangeEvent{{Range: changeRange, Text: "x"}}); !errors.Is(err, ErrIncrementalChangesUnsupported) {
		t.Fatalf("incremental error = %v", err)
	}
	document, _ := store.Get(uri)
	if document.Version != 4 || document.Text != "current" {
		t.Fatalf("rejected change mutated document: %#v", document)
	}
}

func TestDocumentPositionsUseLSPUTF16Encoding(t *testing.T) {
	document := Document{Text: "A😀B\nGeorge"}
	offset, err := document.Offset(Position{Line: 0, Character: 3})
	if err != nil || offset != len("A😀") {
		t.Fatalf("offset = %d, %v", offset, err)
	}
	position, err := document.Position(offset)
	if err != nil || position != (Position{Line: 0, Character: 3}) {
		t.Fatalf("position = %#v, %v", position, err)
	}
}

func TestFilePathDecodesFileURI(t *testing.T) {
	uri := "file:///workspace/George%20M%C3%A1rquez/index.gox"
	want := filepath.FromSlash("/workspace/George Márquez/index.gox")
	if runtime.GOOS == "windows" {
		uri = "file:///C:/workspace/George%20M%C3%A1rquez/index.gox"
		want = filepath.Clean(`C:\workspace\George Márquez\index.gox`)
	}
	path, err := FilePath(uri)
	if err != nil {
		t.Fatal(err)
	}
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	for _, uri := range []string{"https://example.com/index.gox", "file://remote/workspace/index.gox", "file://relative.gox"} {
		if _, err := FilePath(uri); err == nil {
			t.Fatalf("FilePath(%q) accepted invalid URI", uri)
		}
	}
}
