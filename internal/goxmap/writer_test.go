package goxmap

import "testing"

func TestWriterBuildsExactAndGeneratedSegments(t *testing.T) {
	writer, err := NewWriter("generated.go", "page.gox", "hello template world")
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteSource(Range{Start: 0, End: 6}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteGenerated("component", Range{Start: 6, End: 14}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteSource(Range{Start: 14, End: 20}); err != nil {
		t.Fatal(err)
	}

	text, sourceMap, err := writer.Build()
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello component world" {
		t.Fatalf("generated text = %q", text)
	}

	exact, ok := sourceMap.OriginalPosition(2)
	if !ok || exact != (Position{URI: "page.gox", Offset: 2, Exact: true}) {
		t.Fatalf("exact position = %#v, %v", exact, ok)
	}
	anchor, ok := sourceMap.OriginalPosition(8)
	if !ok || anchor != (Position{URI: "page.gox", Offset: 6, Exact: false}) {
		t.Fatalf("anchor position = %#v, %v", anchor, ok)
	}
}

func TestWriterMergesAdjacentExactSegments(t *testing.T) {
	writer, err := NewWriter("generated.go", "page.gox", "abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteSource(Range{Start: 0, End: 3}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteSource(Range{Start: 3, End: 6}); err != nil {
		t.Fatal(err)
	}
	_, sourceMap, err := writer.Build()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sourceMap.Mappings()); got != 1 {
		t.Fatalf("mapping count = %d, want 1", got)
	}
}

func TestWriterRejectsRangesOutsideSource(t *testing.T) {
	writer, err := NewWriter("generated.go", "page.gox", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteSource(Range{Start: 0, End: 4}); err == nil {
		t.Fatal("WriteSource accepted a range outside its source")
	}
	if err := writer.WriteGenerated("x", Range{Start: -1, End: 0}); err == nil {
		t.Fatal("WriteGenerated accepted an invalid anchor")
	}
}
