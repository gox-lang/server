// Package goxmap provides source positions and bidirectional mappings between
// generated GOX artifacts and their original source files.
//
// Offsets and columns are byte based. Protocol-specific encodings, such as the
// UTF-16 positions used by LSP, are converted only at the package boundary.
package goxmap

import (
	"fmt"
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

// Range is a zero-based, half-open byte range [Start, End).
type Range struct {
	Start int
	End   int
}

// Valid reports whether the range contains non-negative, ordered offsets.
func (r Range) Valid() bool {
	return r.Start >= 0 && r.End >= r.Start
}

// Empty reports whether the range contains no bytes.
func (r Range) Empty() bool {
	return r.Start == r.End
}

// Len returns the number of bytes in the range.
func (r Range) Len() int {
	return r.End - r.Start
}

// Contains reports whether offset is inside the half-open range.
func (r Range) Contains(offset int) bool {
	return offset >= r.Start && offset < r.End
}

// BytePosition identifies a zero-based line and byte column.
type BytePosition struct {
	Line   int
	Column int
}

// UTF16Position identifies a zero-based line and UTF-16 code-unit column, as
// required by the Language Server Protocol.
type UTF16Position struct {
	Line      int
	Character int
}

// LineIndex converts positions in one immutable source string. Newline bytes
// are represented by the position immediately after the line content. CRLF is
// treated as one newline and neither byte is part of the line content.
type LineIndex struct {
	text   string
	starts []int
	ends   []int
}

// NewLineIndex indexes text without copying it. The string remains immutable by
// construction and may be shared safely by readers.
func NewLineIndex(text string) *LineIndex {
	starts := []int{0}
	ends := make([]int, 0, 1)
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		end := i
		if end > starts[len(starts)-1] && text[end-1] == '\r' {
			end--
		}
		ends = append(ends, end)
		starts = append(starts, i+1)
	}
	ends = append(ends, len(text))
	return &LineIndex{text: text, starts: starts, ends: ends}
}

// Text returns the indexed source text.
func (i *LineIndex) Text() string {
	return i.text
}

// LineCount returns the number of logical lines, including the empty final line
// when text ends in a newline.
func (i *LineIndex) LineCount() int {
	return len(i.starts)
}

// BytePosition converts an offset to a byte-based position.
func (i *LineIndex) BytePosition(offset int) (BytePosition, error) {
	if offset < 0 || offset > len(i.text) {
		return BytePosition{}, fmt.Errorf("offset %d is outside source of length %d", offset, len(i.text))
	}
	line := sort.Search(len(i.starts), func(n int) bool { return i.starts[n] > offset }) - 1
	if line < 0 {
		line = 0
	}
	column := offset - i.starts[line]
	lineLength := i.ends[line] - i.starts[line]
	if column > lineLength {
		column = lineLength
	}
	return BytePosition{Line: line, Column: column}, nil
}

// ByteOffset converts a byte-based position to an offset.
func (i *LineIndex) ByteOffset(position BytePosition) (int, error) {
	if position.Line < 0 || position.Line >= len(i.starts) {
		return 0, fmt.Errorf("line %d is outside source with %d lines", position.Line, len(i.starts))
	}
	lineLength := i.ends[position.Line] - i.starts[position.Line]
	if position.Column < 0 || position.Column > lineLength {
		return 0, fmt.Errorf("column %d is outside line %d with %d bytes", position.Column, position.Line, lineLength)
	}
	return i.starts[position.Line] + position.Column, nil
}

// UTF16Position converts an offset to an LSP-compatible UTF-16 position.
func (i *LineIndex) UTF16Position(offset int) (UTF16Position, error) {
	position, err := i.BytePosition(offset)
	if err != nil {
		return UTF16Position{}, err
	}
	start := i.starts[position.Line]
	content := i.text[start : start+position.Column]
	if !utf8.ValidString(content) {
		return UTF16Position{}, fmt.Errorf("offset %d does not end on a UTF-8 boundary", offset)
	}
	units := 0
	for _, r := range content {
		units += utf16.RuneLen(r)
	}
	return UTF16Position{Line: position.Line, Character: units}, nil
}

// UTF16Offset converts an LSP-compatible UTF-16 position to a byte offset. A
// position may not split a surrogate pair.
func (i *LineIndex) UTF16Offset(position UTF16Position) (int, error) {
	if position.Line < 0 || position.Line >= len(i.starts) {
		return 0, fmt.Errorf("line %d is outside source with %d lines", position.Line, len(i.starts))
	}
	if position.Character < 0 {
		return 0, fmt.Errorf("UTF-16 character %d cannot be negative", position.Character)
	}
	start, end := i.starts[position.Line], i.ends[position.Line]
	content := i.text[start:end]
	if !utf8.ValidString(content) {
		return 0, fmt.Errorf("line %d contains invalid UTF-8", position.Line)
	}
	units := 0
	bytes := 0
	for _, r := range content {
		if units == position.Character {
			return start + bytes, nil
		}
		runeUnits := utf16.RuneLen(r)
		if units+runeUnits > position.Character {
			return 0, fmt.Errorf("UTF-16 character %d splits a surrogate pair on line %d", position.Character, position.Line)
		}
		units += runeUnits
		bytes += utf8.RuneLen(r)
	}
	if units == position.Character {
		return end, nil
	}
	return 0, fmt.Errorf("UTF-16 character %d is outside line %d with %d code units", position.Character, position.Line, units)
}
