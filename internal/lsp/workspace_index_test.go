package lsp

import "testing"

func TestWorkspaceIndexReplacesAndRemovesDocumentSymbols(t *testing.T) {
	index := NewWorkspaceIndex()
	first := Document{
		URI:  "file:///workspace/components/card.gox",
		Path: "/workspace/components/card.gox",
		Text: "module card\n\nfunc Card() string { return \"George\" }\n",
	}
	second := Document{
		URI:  "file:///workspace/pages/index.gox",
		Path: "/workspace/pages/index.gox",
		Text: "module main\n\nfunc Card() string { return \"George\" }\n",
	}
	if err := index.Update(first); err != nil {
		t.Fatal(err)
	}
	if err := index.Update(second); err != nil {
		t.Fatal(err)
	}
	if definitions := index.Definitions("Card"); len(definitions) != 2 || definitions[0].URI != first.URI || definitions[1].URI != second.URI {
		t.Fatalf("Card definitions = %#v", definitions)
	}

	first.Text = "module card\n\nfunc Panel() string { return \"George\" }\n"
	if err := index.Update(first); err != nil {
		t.Fatal(err)
	}
	if definitions := index.Definitions("Card"); len(definitions) != 1 || definitions[0].URI != second.URI {
		t.Fatalf("Card definitions after replacement = %#v", definitions)
	}
	if definitions := index.Definitions("Panel"); len(definitions) != 1 || definitions[0].URI != first.URI {
		t.Fatalf("Panel definitions = %#v", definitions)
	}

	index.Remove(second.URI)
	if definitions := index.Definitions("Card"); len(definitions) != 0 {
		t.Fatalf("Card definitions after removal = %#v", definitions)
	}
}

func TestWorkspaceIndexPreservesLastValidSymbolsOnParseError(t *testing.T) {
	index := NewWorkspaceIndex()
	document := Document{
		URI:  "file:///workspace/components/card.gox",
		Path: "/workspace/components/card.gox",
		Text: "module card\n\nfunc Card() string { return \"George\" }\n",
	}
	if err := index.Update(document); err != nil {
		t.Fatal(err)
	}
	document.Text = "module card\n\nfunc Card("
	if err := index.Update(document); err == nil {
		t.Fatal("invalid document unexpectedly updated the index")
	}
	if definitions := index.Definitions("Card"); len(definitions) != 1 {
		t.Fatalf("last valid definitions were not preserved: %#v", definitions)
	}
}

func TestWorkspaceIndexPrefersOpenContentAndRestoresDiskContent(t *testing.T) {
	index := NewWorkspaceIndex()
	disk := Document{
		URI:  "file:///workspace/components/card.gox",
		Path: "/workspace/components/card.gox",
		Text: "module card\n\nfunc Card() string { return \"George\" }\n",
	}
	if err := index.UpdateDisk(disk); err != nil {
		t.Fatal(err)
	}
	open := disk
	open.Version = 1
	open.Text = "module card\n\nfunc Panel() string { return \"George\" }\n"
	if err := index.UpdateOpen(open); err != nil {
		t.Fatal(err)
	}
	if len(index.Definitions("Panel")) != 1 || len(index.Definitions("Card")) != 0 {
		t.Fatalf("open overlay was not preferred: Panel=%#v Card=%#v", index.Definitions("Panel"), index.Definitions("Card"))
	}
	disk.Text = "module card\n\nfunc SavedCard() string { return \"George\" }\n"
	if err := index.UpdateDisk(disk); err != nil {
		t.Fatal(err)
	}
	if len(index.Definitions("Panel")) != 1 || len(index.Definitions("SavedCard")) != 0 {
		t.Fatalf("disk update replaced open overlay")
	}
	index.CloseOpen(open.URI)
	if len(index.Definitions("Panel")) != 0 || len(index.Definitions("SavedCard")) != 1 {
		t.Fatalf("disk content was not restored: Panel=%#v SavedCard=%#v", index.Definitions("Panel"), index.Definitions("SavedCard"))
	}
}
