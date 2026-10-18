package lsp

import (
	"unicode"
	"unicode/utf8"
)

type selectorOccurrence struct {
	qualifier      string
	qualifierStart int
	qualifierEnd   int
	member         string
	memberStart    int
	memberEnd      int
}

type selectorResolution int

const (
	selectorUnknownReceiver selectorResolution = iota
	selectorExists
	selectorMissing
	selectorUnexported
)

func completionContainsLabel(items []CompletionItem, label string) bool {
	for _, item := range items {
		if item.Label == label {
			return true
		}
	}
	return false
}

func selectorOccurrences(source string) []selectorOccurrence {
	const (
		selectorCode = iota
		selectorLineComment
		selectorBlockComment
		selectorQuotedString
		selectorRawString
		selectorRuneString
	)
	state := selectorCode
	escaped := false
	var occurrences []selectorOccurrence
	for offset := 0; offset < len(source); {
		value, size := utf8.DecodeRuneInString(source[offset:])
		next := rune(0)
		if offset+size < len(source) {
			next, _ = utf8.DecodeRuneInString(source[offset+size:])
		}
		switch state {
		case selectorCode:
			switch {
			case value == '/' && next == '/':
				state = selectorLineComment
				offset += size + 1
				continue
			case value == '/' && next == '*':
				state = selectorBlockComment
				offset += size + 1
				continue
			case value == '"':
				state = selectorQuotedString
			case value == '`':
				state = selectorRawString
			case value == '\'':
				state = selectorRuneString
			case selectorIdentifierStart(value):
				qualifierStart := offset
				qualifierEnd := consumeSelectorIdentifier(source, offset)
				dot := skipSelectorSpaces(source, qualifierEnd)
				if dot < len(source) && source[dot] == '.' {
					memberStart := skipSelectorSpaces(source, dot+1)
					if memberStart < len(source) {
						memberRune, _ := utf8.DecodeRuneInString(source[memberStart:])
						if selectorIdentifierStart(memberRune) {
							memberEnd := consumeSelectorIdentifier(source, memberStart)
							occurrences = append(occurrences, selectorOccurrence{
								qualifier: source[qualifierStart:qualifierEnd], qualifierStart: qualifierStart,
								qualifierEnd: qualifierEnd, member: source[memberStart:memberEnd],
								memberStart: memberStart, memberEnd: memberEnd,
							})
							offset = memberEnd
							continue
						}
					}
				}
				offset = qualifierEnd
				continue
			}
		case selectorLineComment:
			if value == '\n' {
				state = selectorCode
			}
		case selectorBlockComment:
			if value == '*' && next == '/' {
				state = selectorCode
				offset += size + 1
				continue
			}
		case selectorRawString:
			if value == '`' {
				state = selectorCode
			}
		case selectorQuotedString, selectorRuneString:
			if escaped {
				escaped = false
			} else if value == '\\' {
				escaped = true
			} else if state == selectorQuotedString && value == '"' || state == selectorRuneString && value == '\'' {
				state = selectorCode
			}
		}
		offset += size
	}
	return occurrences
}

func selectorIdentifierStart(value rune) bool {
	return value == '_' || unicode.IsLetter(value)
}

func consumeSelectorIdentifier(source string, offset int) int {
	for offset < len(source) {
		value, size := utf8.DecodeRuneInString(source[offset:])
		if value != '_' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
			break
		}
		offset += size
	}
	return offset
}

func skipSelectorSpaces(source string, offset int) int {
	for offset < len(source) {
		value, size := utf8.DecodeRuneInString(source[offset:])
		if !unicode.IsSpace(value) {
			break
		}
		offset += size
	}
	return offset
}
