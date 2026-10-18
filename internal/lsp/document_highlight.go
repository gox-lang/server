package lsp

import (
	"strings"
	"unicode/utf8"
)

const (
	documentHighlightText  = 1
	documentHighlightRead  = 2
	documentHighlightWrite = 3
)

type DocumentHighlight struct {
	Range Range `json:"range"`
	Kind  int   `json:"kind,omitempty"`
}

func (s *Server) documentHighlights(document Document, position Position) []DocumentHighlight {
	locations := s.references(document, position, true)
	highlights := make([]DocumentHighlight, 0, len(locations))
	for _, location := range locations {
		if location.URI != document.URI {
			continue
		}
		start, startErr := document.Offset(location.Range.Start)
		end, endErr := document.Offset(location.Range.End)
		if startErr != nil || endErr != nil {
			continue
		}
		highlights = append(highlights, DocumentHighlight{
			Range: location.Range,
			Kind:  s.documentHighlightKind(document, start, end),
		})
	}
	return highlights
}

func (s *Server) documentHighlightKind(document Document, start, end int) int {
	if _, ok := s.symbols.LocalDeclarationAt(document.URI, start); ok {
		return documentHighlightWrite
	}
	for _, indexed := range s.symbols.DocumentSymbols(document.URI) {
		if indexed.Symbol.Source.Range.Contains(start) || start == indexed.Symbol.Source.Range.End {
			return documentHighlightWrite
		}
	}
	if identifierIsAssigned(document.Text, start, end) {
		return documentHighlightWrite
	}
	return documentHighlightRead
}

func identifierIsAssigned(source string, start, end int) bool {
	if start < 0 || end < start || end > len(source) {
		return false
	}
	after := skipSourceSpacesForward(source, end)
	if after+1 < len(source) && (source[after:after+2] == "++" || source[after:after+2] == "--") {
		return true
	}
	statementStart := highlightStatementStart(source, start)
	statementEnd := highlightStatementEnd(source, end)
	operator := assignmentOperatorOffset(source, statementStart, statementEnd)
	return operator >= 0 && start < operator
}

func highlightStatementStart(source string, offset int) int {
	for offset > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:offset])
		if value == '\n' || value == ';' || value == '{' || value == '}' {
			break
		}
		offset -= size
	}
	return offset
}

func highlightStatementEnd(source string, offset int) int {
	for offset < len(source) {
		value, size := utf8.DecodeRuneInString(source[offset:])
		if value == '\n' || value == ';' || value == '{' || value == '}' {
			break
		}
		offset += size
	}
	return offset
}

func assignmentOperatorOffset(source string, start, end int) int {
	quoted := rune(0)
	escaped := false
	for offset := start; offset < end; {
		if strings.HasPrefix(source[offset:end], "<<=") || strings.HasPrefix(source[offset:end], ">>=") {
			return offset
		}
		value, size := utf8.DecodeRuneInString(source[offset:end])
		if quoted != 0 {
			if escaped {
				escaped = false
			} else if value == '\\' && quoted != '`' {
				escaped = true
			} else if value == quoted {
				quoted = 0
			}
			offset += size
			continue
		}
		if value == '"' || value == '\'' || value == '`' {
			quoted = value
			offset += size
			continue
		}
		if value == ':' && offset+size < end && source[offset+size] == '=' {
			return offset
		}
		if value == '=' {
			previous := byte(0)
			if offset > start {
				previous = source[offset-1]
			}
			next := byte(0)
			if offset+size < end {
				next = source[offset+size]
			}
			if previous != '=' && previous != '!' && previous != '<' && previous != '>' && next != '=' {
				return offset
			}
		}
		if strings.ContainsRune("+-*/%&|^", value) && offset+size < end && source[offset+size] == '=' {
			return offset
		}
		offset += size
	}
	return -1
}
