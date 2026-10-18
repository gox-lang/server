package goxmap

import "testing"

func TestMapLooksUpExactAndAnchorMappings(t *testing.T) {
	sourceMap, err := New("generated.go", []Mapping{
		{Generated: Range{Start: 20, End: 24}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 5, End: 9}}, Kind: Exact},
		{Generated: Range{Start: 0, End: 10}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 0, End: 5}}, Kind: Anchor},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, ok := sourceMap.OriginalPosition(22)
	if !ok || got != (Position{URI: "page.gox", Offset: 7, Exact: true}) {
		t.Fatalf("OriginalPosition(22) = %#v, %v", got, ok)
	}
	got, ok = sourceMap.OriginalPosition(4)
	if !ok || got != (Position{URI: "page.gox", Offset: 0, Exact: false}) {
		t.Fatalf("OriginalPosition(4) = %#v, %v", got, ok)
	}
	if _, ok := sourceMap.OriginalPosition(15); ok {
		t.Fatal("unmapped generated position unexpectedly resolved")
	}

	positions := sourceMap.GeneratedPositions("page.gox", 7)
	if len(positions) != 1 || positions[0] != (Position{URI: "generated.go", Offset: 22, Exact: true}) {
		t.Fatalf("GeneratedPositions(page.gox, 7) = %#v", positions)
	}
}

func TestMapRejectsAmbiguousOrInvalidSegments(t *testing.T) {
	_, err := New("generated.go", []Mapping{
		{Generated: Range{Start: 0, End: 4}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 0, End: 4}}, Kind: Exact},
		{Generated: Range{Start: 3, End: 5}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 8, End: 10}}, Kind: Exact},
	})
	if err == nil {
		t.Fatal("New accepted overlapping generated ranges")
	}
	_, err = New("generated.go", []Mapping{
		{Generated: Range{Start: 0, End: 3}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 0, End: 4}}, Kind: Exact},
	})
	if err == nil {
		t.Fatal("New accepted unequal exact ranges")
	}
}

func TestMapLooksUpOriginalRangesWithoutCrossingSegments(t *testing.T) {
	sourceMap, err := New("generated.go", []Mapping{
		{Generated: Range{Start: 0, End: 10}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 20, End: 30}}, Kind: Exact},
		{Generated: Range{Start: 10, End: 20}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 40, End: 60}}, Kind: Anchor},
	})
	if err != nil {
		t.Fatal(err)
	}

	original, kind, ok := sourceMap.OriginalRange(Range{Start: 2, End: 6})
	if !ok || kind != Exact || original != (SourceRange{URI: "page.gox", Range: Range{Start: 22, End: 26}}) {
		t.Fatalf("exact range = %#v, %v, %v", original, kind, ok)
	}
	original, kind, ok = sourceMap.OriginalRange(Range{Start: 12, End: 16})
	if !ok || kind != Anchor || original != (SourceRange{URI: "page.gox", Range: Range{Start: 40, End: 60}}) {
		t.Fatalf("anchor range = %#v, %v, %v", original, kind, ok)
	}
	if _, _, ok := sourceMap.OriginalRange(Range{Start: 8, End: 12}); ok {
		t.Fatal("range crossing two mappings unexpectedly resolved")
	}
}

func TestComposeMapsAcrossAnIntermediateDocument(t *testing.T) {
	upper, err := New("preprocessed.go", []Mapping{
		{Generated: Range{Start: 10, End: 20}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 100, End: 110}}, Kind: Exact},
	})
	if err != nil {
		t.Fatal(err)
	}
	lower, err := New("bundle.go", []Mapping{
		{Generated: Range{Start: 30, End: 36}, Original: SourceRange{URI: "preprocessed.go", Range: Range{Start: 12, End: 18}}, Kind: Exact},
	})
	if err != nil {
		t.Fatal(err)
	}

	composed, err := Compose("bundle.go", lower, upper)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := composed.OriginalPosition(33)
	if !ok || got != (Position{URI: "page.gox", Offset: 105, Exact: true}) {
		t.Fatalf("composed OriginalPosition(33) = %#v, %v", got, ok)
	}
}

func TestComposeTranslatesAnchorThroughExactIntermediateRange(t *testing.T) {
	upper, err := New("preprocessed.go", []Mapping{
		{Generated: Range{Start: 0, End: 100}, Original: SourceRange{URI: "page.gox", Range: Range{Start: 200, End: 300}}, Kind: Exact},
	})
	if err != nil {
		t.Fatal(err)
	}
	lower, err := New("generated.go", []Mapping{
		{Generated: Range{Start: 10, End: 30}, Original: SourceRange{URI: "preprocessed.go", Range: Range{Start: 40, End: 60}}, Kind: Anchor},
	})
	if err != nil {
		t.Fatal(err)
	}

	composed, err := Compose("generated.go", lower, upper)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := composed.OriginalPosition(15)
	if !ok || got != (Position{URI: "page.gox", Offset: 240, Exact: false}) {
		t.Fatalf("composed anchor = %#v, %v", got, ok)
	}
}
