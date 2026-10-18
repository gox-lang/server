package goxmap

import (
	"encoding/json"
	"testing"
)

func TestMarshalJSONDocumentIncludesReadablePositions(t *testing.T) {
	sourceMap, err := New("bundle.go", []Mapping{{
		Generated: Range{Start: 4, End: 10},
		Original:  SourceRange{URI: "page.gox", Range: Range{Start: 6, End: 12}},
		Kind:      Exact,
	}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalJSONDocument(sourceMap, "one\nGeorge\n", map[string]string{"page.gox": "hello\nGeorge\n"})
	if err != nil {
		t.Fatal(err)
	}
	var document JSONDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != 1 || document.GeneratedURI != "bundle.go" || len(document.Mappings) != 1 {
		t.Fatalf("document = %#v", document)
	}
	mapping := document.Mappings[0]
	if mapping.Kind != "exact" || mapping.Generated.Start != (JSONPosition{Offset: 4, Line: 1, Column: 0}) || mapping.Original.Start != (JSONPosition{Offset: 6, Line: 1, Column: 0}) {
		t.Fatalf("mapping = %#v", mapping)
	}
}

func TestUnmarshalJSONDocumentRestoresLookupMap(t *testing.T) {
	sourceMap, err := New("bundle.go", []Mapping{
		{Generated: Range{Start: 4, End: 10}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 6, End: 12}}, Kind: Exact},
		{Generated: Range{Start: 12, End: 20}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 20, End: 24}}, Kind: Anchor},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalJSONDocument(sourceMap, "one\nGeorge\nsynthetic", map[string]string{"page.gox": "hello\nGeorge\nabcdefghijklmno"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalJSONDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	position, ok := restored.OriginalPosition(8)
	if restored.GeneratedURI() != "bundle.go" || !ok || position.URI != "page.gox" || position.Offset != 10 || !position.Exact {
		t.Fatalf("exact position = %#v, %v", position, ok)
	}
	position, ok = restored.OriginalPosition(15)
	if !ok || position.URI != "page.gox" || position.Offset != 20 || position.Exact {
		t.Fatalf("anchor position = %#v, %v", position, ok)
	}
}
