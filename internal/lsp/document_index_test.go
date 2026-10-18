package lsp

import "testing"

func TestDocumentPositionIndexFollowsTextCopiesAndEdits(t *testing.T) {
	original := Document{Text: "hello\r\n😀 español\n"}.withLineIndex()
	for _, text := range []string{original.Text, "\n😀\nchanged", "abc\ndef"} {
		copy := original
		copy.Text = text
		fresh := Document{Text: text}
		for offset := 0; offset <= len(text); offset++ {
			got, err := copy.Position(offset)
			want, wantErr := fresh.Position(offset)
			if (err == nil) != (wantErr == nil) || got != want {
				t.Fatalf("offset %d: %v/%v want %v/%v", offset, got, err, want, wantErr)
			}
			if err == nil {
				a, ae := copy.Offset(got)
				b, be := fresh.Offset(want)
				if a != b || (ae == nil) != (be == nil) {
					t.Fatal("UTF-16 index mismatch")
				}
			}
		}
	}
	store := NewDocumentStore()
	uri := "file:///tmp/index.gox"
	if err := store.Open(TextDocumentItem{URI: uri, Version: 1, Text: original.Text}); err != nil {
		t.Fatal(err)
	}
	if err := store.Change(VersionedTextDocumentIdentifier{URI: uri, Version: 2}, []TextDocumentContentChangeEvent{{Text: "x\ny"}}); err != nil {
		t.Fatal(err)
	}
	doc, _ := store.Get(uri)
	pos, err := doc.Position(2)
	if err != nil || pos != (Position{Line: 1, Character: 0}) {
		t.Fatalf("stale stored index: %v %v", pos, err)
	}
}
