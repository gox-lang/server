package lsp

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type LinkedEditingRanges struct {
	Ranges      []Range `json:"ranges"`
	WordPattern string  `json:"wordPattern,omitempty"`
}

type templateTagOccurrence struct {
	Name       string
	NameStart  int
	NameEnd    int
	Closing    bool
	SelfClosed bool
}

type templateTagPair struct {
	Open  templateTagOccurrence
	Close templateTagOccurrence
}

func linkedEditingRanges(document Document, position Position) *LinkedEditingRanges {
	offset, err := document.Offset(position)
	if err != nil {
		return nil
	}
	if classifyCursorContext(document.Text, offset, nil).passiveEmbedded() {
		return nil
	}
	for _, pair := range scanTemplateTagPairs(document.Text) {
		if !tagNameContainsOffset(pair.Open, offset) && !tagNameContainsOffset(pair.Close, offset) {
			continue
		}
		openStart, openStartErr := document.Position(pair.Open.NameStart)
		openEnd, openEndErr := document.Position(pair.Open.NameEnd)
		closeStart, closeStartErr := document.Position(pair.Close.NameStart)
		closeEnd, closeEndErr := document.Position(pair.Close.NameEnd)
		if openStartErr != nil || openEndErr != nil || closeStartErr != nil || closeEndErr != nil {
			return nil
		}
		return &LinkedEditingRanges{
			Ranges: []Range{
				{Start: openStart, End: openEnd},
				{Start: closeStart, End: closeEnd},
			},
			WordPattern: `[A-Za-z_][A-Za-z0-9_.:-]*`,
		}
	}
	return nil
}

func tagNameContainsOffset(tag templateTagOccurrence, offset int) bool {
	return offset >= tag.NameStart && offset <= tag.NameEnd
}

func scanTemplateTagPairs(source string) []templateTagPair {
	var pairs []templateTagPair
	var stack []templateTagOccurrence
	for offset := 0; offset < len(source); {
		if strings.HasPrefix(source[offset:], "<!--") {
			end := strings.Index(source[offset+4:], "-->")
			if end < 0 {
				break
			}
			offset += 4 + end + 3
			continue
		}
		if strings.HasPrefix(source[offset:], "{{") {
			offset = skipTemplateExpression(source, offset+2)
			continue
		}
		if source[offset] != '<' {
			_, size := utf8.DecodeRuneInString(source[offset:])
			offset += size
			continue
		}
		tag, end, ok := scanTemplateTag(source, offset)
		if !ok {
			offset++
			continue
		}
		offset = end
		if tag.SelfClosed {
			continue
		}
		if !tag.Closing {
			stack = append(stack, tag)
			continue
		}
		for index := len(stack) - 1; index >= 0; index-- {
			if stack[index].Name != tag.Name {
				continue
			}
			pairs = append(pairs, templateTagPair{Open: stack[index], Close: tag})
			stack = stack[:index]
			break
		}
	}
	return pairs
}

func scanTemplateTag(source string, start int) (templateTagOccurrence, int, bool) {
	offset := start + 1
	closing := false
	if offset < len(source) && source[offset] == '/' {
		closing = true
		offset++
	}
	if offset >= len(source) || source[offset] == '!' || source[offset] == '?' {
		return templateTagOccurrence{}, start + 1, false
	}
	sigil := byte(0)
	if source[offset] == ':' || source[offset] == '&' || source[offset] == '@' {
		sigil = source[offset]
		offset++
	}
	nameStart := offset
	for offset < len(source) {
		value, size := utf8.DecodeRuneInString(source[offset:])
		if value != '_' && value != '.' && value != '-' && value != ':' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
			break
		}
		offset += size
	}
	if offset == nameStart || unicode.IsDigit([]rune(source[nameStart:offset])[0]) {
		return templateTagOccurrence{}, start + 1, false
	}
	name := source[nameStart:offset]
	if sigil != 0 {
		name = string(sigil) + name
	}
	tagEnd, selfClosed, ok := scanTemplateTagEnd(source, offset)
	if !ok {
		return templateTagOccurrence{}, start + 1, false
	}
	return templateTagOccurrence{Name: name, NameStart: nameStart, NameEnd: offset, Closing: closing, SelfClosed: selfClosed}, tagEnd, true
}

func scanTemplateTagEnd(source string, offset int) (int, bool, bool) {
	quote := byte(0)
	for offset < len(source) {
		if quote != 0 {
			if source[offset] == '\\' && offset+1 < len(source) {
				offset += 2
				continue
			}
			if source[offset] == quote {
				quote = 0
			}
			offset++
			continue
		}
		if strings.HasPrefix(source[offset:], "{{") {
			offset = skipTemplateExpression(source, offset+2)
			continue
		}
		if source[offset] == '"' || source[offset] == '\'' {
			quote = source[offset]
			offset++
			continue
		}
		if source[offset] == '>' {
			before := offset - 1
			for before >= 0 && (source[before] == ' ' || source[before] == '\t' || source[before] == '\r' || source[before] == '\n') {
				before--
			}
			return offset + 1, before >= 0 && source[before] == '/', true
		}
		offset++
	}
	return offset, false, false
}

func skipTemplateExpression(source string, offset int) int {
	quote := byte(0)
	for offset < len(source) {
		if quote != 0 {
			if source[offset] == '\\' && quote != '`' && offset+1 < len(source) {
				offset += 2
				continue
			}
			if source[offset] == quote {
				quote = 0
			}
			offset++
			continue
		}
		if source[offset] == '"' || source[offset] == '\'' || source[offset] == '`' {
			quote = source[offset]
			offset++
			continue
		}
		if strings.HasPrefix(source[offset:], "}}") {
			return offset + 2
		}
		offset++
	}
	return offset
}
