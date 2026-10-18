package lsp

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type FoldingRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type FoldingRange struct {
	StartLine      int    `json:"startLine"`
	StartCharacter int    `json:"startCharacter,omitempty"`
	EndLine        int    `json:"endLine"`
	EndCharacter   int    `json:"endCharacter,omitempty"`
	Kind           string `json:"kind,omitempty"`
}

type SelectionRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Positions    []Position             `json:"positions"`
}

type SelectionRange struct {
	Range  Range           `json:"range"`
	Parent *SelectionRange `json:"parent,omitempty"`
}

type structuralSpan struct {
	start int
	end   int
	kind  string
}

func foldingRanges(document Document) []FoldingRange {
	spans := structuralSpans(document.Text)
	result := make([]FoldingRange, 0, len(spans))
	seen := map[[3]int]bool{}
	for _, span := range spans {
		start, startErr := document.Position(span.start)
		end, endErr := document.Position(span.end)
		if startErr != nil || endErr != nil || end.Line <= start.Line {
			continue
		}
		endLine := end.Line
		closingOffset := span.end
		if closingOffset > span.start {
			closingOffset--
		}
		if linePrefixWhitespace(document.Text, closingOffset) && endLine > start.Line {
			endLine--
		}
		if endLine <= start.Line {
			continue
		}
		endCharacter := end.Character
		if endLine != end.Line {
			endCharacter = 0
		}
		key := [3]int{start.Line, endLine, foldKindCode(span.kind)}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, FoldingRange{StartLine: start.Line, StartCharacter: start.Character, EndLine: endLine, EndCharacter: endCharacter, Kind: span.kind})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].StartLine != result[j].StartLine {
			return result[i].StartLine < result[j].StartLine
		}
		return result[i].EndLine > result[j].EndLine
	})
	return result
}

func selectionRanges(document Document, positions []Position) []SelectionRange {
	spans := structuralSpans(document.Text)
	result := make([]SelectionRange, 0, len(positions))
	for _, position := range positions {
		offset, err := document.Offset(position)
		if err != nil {
			result = append(result, SelectionRange{Range: Range{Start: position, End: position}})
			continue
		}
		candidates := containingSpans(spans, offset)
		if line, ok := sourceLineSpan(document.Text, offset); ok {
			candidates = append(candidates, line)
		}
		if start, end, ok := identifierOffsetsAt(document.Text, offset); ok {
			candidates = append(candidates, structuralSpan{start: start, end: end})
		}
		candidates = append(candidates, structuralSpan{start: 0, end: len(document.Text)})
		candidates = uniqueNestedSpans(candidates)
		var parent *SelectionRange
		for index := len(candidates) - 1; index >= 0; index-- {
			start, startErr := document.Position(candidates[index].start)
			end, endErr := document.Position(candidates[index].end)
			if startErr != nil || endErr != nil {
				continue
			}
			value := &SelectionRange{Range: Range{Start: start, End: end}, Parent: parent}
			parent = value
		}
		if parent == nil {
			result = append(result, SelectionRange{Range: Range{Start: position, End: position}})
		} else {
			result = append(result, *parent)
		}
	}
	return result
}

func structuralSpans(source string) []structuralSpan {
	spans := delimiterAndCommentSpans(source)
	spans = append(spans, templateElementSpans(source)...)
	return spans
}

func delimiterAndCommentSpans(source string) []structuralSpan {
	type opener struct {
		value byte
		start int
	}
	var spans []structuralSpan
	var stack []opener
	for offset := 0; offset < len(source); {
		if strings.HasPrefix(source[offset:], "//") {
			end := strings.IndexByte(source[offset:], '\n')
			if end < 0 {
				end = len(source) - offset
			}
			offset += end
			continue
		}
		if strings.HasPrefix(source[offset:], "/*") {
			end := strings.Index(source[offset+2:], "*/")
			if end < 0 {
				end = len(source) - offset - 2
			}
			end += offset + 4
			if end > len(source) {
				end = len(source)
			}
			spans = append(spans, structuralSpan{start: offset, end: end, kind: "comment"})
			offset = end
			continue
		}
		if source[offset] == '"' || source[offset] == '\'' || source[offset] == '`' {
			offset = skipQuoted(source, offset)
			continue
		}
		switch source[offset] {
		case '{', '(', '[':
			stack = append(stack, opener{value: source[offset], start: offset})
		case '}', ')', ']':
			wanted := byte('{')
			if source[offset] == ')' {
				wanted = '('
			} else if source[offset] == ']' {
				wanted = '['
			}
			for index := len(stack) - 1; index >= 0; index-- {
				if stack[index].value != wanted {
					continue
				}
				open := stack[index]
				stack = stack[:index]
				kind := "region"
				if wanted == '(' && previousWord(source, open.start) == "import" {
					kind = "imports"
				}
				spans = append(spans, structuralSpan{start: open.start, end: offset + 1, kind: kind})
				break
			}
		}
		offset++
	}
	for _, open := range stack {
		if strings.Count(source[open.start:], "\n") > 0 {
			spans = append(spans, structuralSpan{start: open.start, end: len(source), kind: "region"})
		}
	}
	return spans
}

type templateTag struct {
	name       string
	start      int
	end        int
	closing    bool
	selfClosed bool
}

func templateElementSpans(source string) []structuralSpan {
	var spans []structuralSpan
	var stack []templateTag
	for offset := 0; offset < len(source); {
		if strings.HasPrefix(source[offset:], "<!--") {
			end := strings.Index(source[offset+4:], "-->")
			if end < 0 {
				end = len(source) - offset - 4
			}
			end += offset + 7
			if end > len(source) {
				end = len(source)
			}
			spans = append(spans, structuralSpan{start: offset, end: end, kind: "comment"})
			offset = end
			continue
		}
		tag, ok := readTemplateTag(source, offset)
		if !ok {
			offset++
			continue
		}
		spans = append(spans, structuralSpan{start: tag.start, end: tag.end, kind: "region"})
		spans = append(spans, templateAttributeSpans(source, tag)...)
		if tag.closing {
			for index := len(stack) - 1; index >= 0; index-- {
				if stack[index].name != tag.name {
					continue
				}
				open := stack[index]
				stack = stack[:index]
				spans = append(spans, structuralSpan{start: open.start, end: tag.end, kind: "region"})
				break
			}
		} else if !tag.selfClosed && !isStructuralHTMLVoidTag(tag.name) {
			stack = append(stack, tag)
		}
		offset = tag.end
	}
	for _, open := range stack {
		if strings.Count(source[open.start:], "\n") > 0 {
			spans = append(spans, structuralSpan{start: open.start, end: len(source), kind: "region"})
		}
	}
	return spans
}

func readTemplateTag(source string, start int) (templateTag, bool) {
	if start >= len(source) || source[start] != '<' {
		return templateTag{}, false
	}
	offset := start + 1
	closing := false
	if offset < len(source) && source[offset] == '/' {
		closing = true
		offset++
	}
	if offset < len(source) && (source[offset] == ':' || source[offset] == '&' || source[offset] == '@') {
		prefix := source[offset : offset+1]
		offset++
		nameStart := offset
		for offset < len(source) {
			value, size := utf8.DecodeRuneInString(source[offset:])
			if value != '_' && value != '-' && value != '.' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
				break
			}
			offset += size
		}
		if offset == nameStart {
			return templateTag{}, false
		}
		name := prefix + source[nameStart:offset]
		return finishTemplateTag(source, start, offset, name, closing)
	}
	nameStart := offset
	for offset < len(source) {
		value, size := utf8.DecodeRuneInString(source[offset:])
		if value != '_' && value != '-' && value != '.' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
			break
		}
		offset += size
	}
	if offset == nameStart && (offset >= len(source) || source[offset] != '>') {
		return templateTag{}, false
	}
	name := source[nameStart:offset]
	return finishTemplateTag(source, start, offset, name, closing)
}

func finishTemplateTag(source string, start, offset int, name string, closing bool) (templateTag, bool) {
	quote := byte(0)
	braceDepth := 0
	for offset < len(source) {
		value := source[offset]
		if quote != 0 {
			if value == '\\' {
				offset += 2
				continue
			}
			if value == quote {
				quote = 0
			}
		} else if value == '"' || value == '\'' {
			quote = value
		} else if value == '{' {
			braceDepth++
		} else if value == '}' && braceDepth > 0 {
			braceDepth--
		} else if value == '>' && braceDepth == 0 {
			selfClosed := offset > start && source[offset-1] == '/'
			return templateTag{name: name, start: start, end: offset + 1, closing: closing, selfClosed: selfClosed}, true
		}
		offset++
	}
	return templateTag{name: name, start: start, end: len(source), closing: closing}, true
}

func skipQuoted(source string, start int) int {
	quote := source[start]
	for offset := start + 1; offset < len(source); offset++ {
		if quote != '`' && source[offset] == '\\' {
			offset++
			continue
		}
		if source[offset] == quote {
			return offset + 1
		}
	}
	return len(source)
}

func templateAttributeSpans(source string, tag templateTag) []structuralSpan {
	if tag.closing || tag.end <= tag.start+1 {
		return nil
	}
	offset := tag.start + 1
	if offset < tag.end && (source[offset] == ':' || source[offset] == '&' || source[offset] == '@') {
		offset++
	}
	for offset < tag.end && (source[offset] == '_' || source[offset] == '-' || source[offset] == '.' || source[offset] >= 'a' && source[offset] <= 'z' || source[offset] >= 'A' && source[offset] <= 'Z' || source[offset] >= '0' && source[offset] <= '9') {
		offset++
	}
	var spans []structuralSpan
	for offset < tag.end {
		for offset < tag.end && unicode.IsSpace(rune(source[offset])) {
			offset++
		}
		if offset >= tag.end || source[offset] == '>' || source[offset] == '/' {
			break
		}
		start := offset
		for offset < tag.end && (source[offset] == '_' || source[offset] == '-' || source[offset] == ':' || source[offset] == '@' || source[offset] == '.' || source[offset] >= 'a' && source[offset] <= 'z' || source[offset] >= 'A' && source[offset] <= 'Z' || source[offset] >= '0' && source[offset] <= '9') {
			offset++
		}
		if offset == start {
			offset++
			continue
		}
		for offset < tag.end && unicode.IsSpace(rune(source[offset])) {
			offset++
		}
		if offset < tag.end && source[offset] == '=' {
			offset++
			for offset < tag.end && unicode.IsSpace(rune(source[offset])) {
				offset++
			}
			if offset < tag.end && (source[offset] == '"' || source[offset] == '\'') {
				offset = skipQuoted(source, offset)
			} else if offset+1 < tag.end && source[offset:offset+2] == "{{" {
				if close := strings.Index(source[offset+2:tag.end], "}}"); close >= 0 {
					offset += close + 4
				} else {
					offset = tag.end
				}
			} else {
				for offset < tag.end && !unicode.IsSpace(rune(source[offset])) && source[offset] != '>' {
					offset++
				}
			}
		}
		spans = append(spans, structuralSpan{start: start, end: offset})
	}
	return spans
}

func sourceLineSpan(source string, offset int) (structuralSpan, bool) {
	if offset < 0 || offset > len(source) {
		return structuralSpan{}, false
	}
	start := strings.LastIndex(source[:offset], "\n") + 1
	end := len(source)
	if relative := strings.IndexByte(source[offset:], '\n'); relative >= 0 {
		end = offset + relative
	}
	for start < end && unicode.IsSpace(rune(source[start])) {
		start++
	}
	for end > start && unicode.IsSpace(rune(source[end-1])) {
		end--
	}
	return structuralSpan{start: start, end: end}, end > start
}

func previousWord(source string, offset int) string {
	for offset > 0 && unicode.IsSpace(rune(source[offset-1])) {
		offset--
	}
	end := offset
	for offset > 0 && (source[offset-1] == '_' || source[offset-1] >= 'a' && source[offset-1] <= 'z' || source[offset-1] >= 'A' && source[offset-1] <= 'Z') {
		offset--
	}
	return source[offset:end]
}

func linePrefixWhitespace(source string, offset int) bool {
	if offset > len(source) {
		offset = len(source)
	}
	start := strings.LastIndex(source[:offset], "\n") + 1
	return strings.TrimSpace(source[start:offset]) == ""
}

func containingSpans(spans []structuralSpan, offset int) []structuralSpan {
	result := make([]structuralSpan, 0)
	for _, span := range spans {
		if span.start <= offset && offset <= span.end {
			result = append(result, span)
		}
	}
	return result
}

func uniqueNestedSpans(spans []structuralSpan) []structuralSpan {
	sort.SliceStable(spans, func(i, j int) bool {
		left, right := spans[i].end-spans[i].start, spans[j].end-spans[j].start
		if left != right {
			return left < right
		}
		return spans[i].start > spans[j].start
	})
	result := make([]structuralSpan, 0, len(spans))
	for _, span := range spans {
		if span.end < span.start {
			continue
		}
		if len(result) > 0 {
			last := result[len(result)-1]
			if last.start == span.start && last.end == span.end {
				continue
			}
			if span.start > last.start || span.end < last.end {
				continue
			}
		}
		result = append(result, span)
	}
	return result
}

func foldKindCode(kind string) int {
	if kind == "comment" {
		return 1
	}
	if kind == "imports" {
		return 2
	}
	return 0
}

func isStructuralHTMLVoidTag(name string) bool {
	switch strings.ToLower(name) {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}
