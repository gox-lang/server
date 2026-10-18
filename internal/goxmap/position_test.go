package goxmap

import "testing"

func TestLineIndexHandlesLFCRLFAndTrailingLine(t *testing.T) {
	index := NewLineIndex("a\r\nβ\n")
	if got, want := index.LineCount(), 3; got != want {
		t.Fatalf("LineCount() = %d, want %d", got, want)
	}

	tests := []struct {
		offset int
		want   BytePosition
	}{
		{offset: 0, want: BytePosition{Line: 0, Column: 0}},
		{offset: 1, want: BytePosition{Line: 0, Column: 1}},
		{offset: 2, want: BytePosition{Line: 0, Column: 1}},
		{offset: 3, want: BytePosition{Line: 1, Column: 0}},
		{offset: 5, want: BytePosition{Line: 1, Column: 2}},
		{offset: 6, want: BytePosition{Line: 2, Column: 0}},
	}
	for _, test := range tests {
		got, err := index.BytePosition(test.offset)
		if err != nil || got != test.want {
			t.Errorf("BytePosition(%d) = %#v, %v; want %#v", test.offset, got, err, test.want)
		}
	}
}

func TestLineIndexRoundTripsUTF16(t *testing.T) {
	index := NewLineIndex("a😀b\nñ")
	tests := []struct {
		offset int
		want   UTF16Position
	}{
		{offset: 0, want: UTF16Position{Line: 0, Character: 0}},
		{offset: 1, want: UTF16Position{Line: 0, Character: 1}},
		{offset: 5, want: UTF16Position{Line: 0, Character: 3}},
		{offset: 6, want: UTF16Position{Line: 0, Character: 4}},
		{offset: 7, want: UTF16Position{Line: 1, Character: 0}},
		{offset: 9, want: UTF16Position{Line: 1, Character: 1}},
	}
	for _, test := range tests {
		got, err := index.UTF16Position(test.offset)
		if err != nil || got != test.want {
			t.Fatalf("UTF16Position(%d) = %#v, %v; want %#v", test.offset, got, err, test.want)
		}
		roundTrip, err := index.UTF16Offset(got)
		if err != nil || roundTrip != test.offset {
			t.Errorf("UTF16Offset(%#v) = %d, %v; want %d", got, roundTrip, err, test.offset)
		}
	}
	if _, err := index.UTF16Offset(UTF16Position{Line: 0, Character: 2}); err == nil {
		t.Fatal("UTF16Offset accepted a position inside a surrogate pair")
	}
}

func TestLineIndexRejectsInvalidPositions(t *testing.T) {
	index := NewLineIndex("abc")
	if _, err := index.BytePosition(4); err == nil {
		t.Fatal("BytePosition accepted an offset beyond the source")
	}
	if _, err := index.ByteOffset(BytePosition{Line: 0, Column: 4}); err == nil {
		t.Fatal("ByteOffset accepted a column beyond the line")
	}
}
