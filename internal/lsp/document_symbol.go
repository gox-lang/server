package lsp

import (
	"sort"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"
)

type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

func (s *Server) documentSymbols(document Document) []DocumentSymbol {
	indexed := s.symbols.DocumentSymbols(document.URI)
	if len(indexed) == 0 {
		return []DocumentSymbol{}
	}

	type outlineNode struct {
		source goxcompiler.SourceSymbol
		symbol DocumentSymbol
		start  int
		end    int
	}
	templates := s.symbols.TemplateRanges(document.URI)
	nodes := make([]outlineNode, 0, len(indexed))
	var module *DocumentSymbol
	for _, indexedSymbol := range indexed {
		start, end := sourceDeclarationOffsets(document.Text, indexedSymbol.Symbol, templates)
		symbol, ok := documentSymbolFromSource(document, indexedSymbol.Symbol, start, end)
		if !ok {
			continue
		}
		if indexedSymbol.Symbol.Kind == goxcompiler.SymbolModule {
			value := symbol
			module = &value
			continue
		}
		nodes = append(nodes, outlineNode{source: indexedSymbol.Symbol, symbol: symbol, start: start, end: end})
	}

	parents := make([]int, len(nodes))
	for index := range parents {
		parents[index] = -1
	}
	for child := range nodes {
		if nodes[child].source.Container == "" {
			continue
		}
		bestSize := len(document.Text) + 1
		for parent := range nodes {
			if parent == child || nodes[parent].source.Name != nodes[child].source.Container ||
				nodes[parent].start > nodes[child].start || nodes[parent].end < nodes[child].end {
				continue
			}
			if size := nodes[parent].end - nodes[parent].start; size < bestSize {
				parents[child], bestSize = parent, size
			}
		}
	}
	var build func(int, map[int]bool) DocumentSymbol
	build = func(index int, stack map[int]bool) DocumentSymbol {
		result := nodes[index].symbol
		if stack[index] {
			return result
		}
		stack[index] = true
		for child := range nodes {
			if parents[child] == index {
				result.Children = append(result.Children, build(child, stack))
			}
		}
		delete(stack, index)
		sortDocumentSymbols(result.Children)
		return result
	}
	topLevel := make([]DocumentSymbol, 0, len(nodes))
	for index := range nodes {
		if parents[index] == -1 {
			topLevel = append(topLevel, build(index, make(map[int]bool)))
		}
	}
	sortDocumentSymbols(topLevel)
	if module == nil {
		return topLevel
	}
	module.Children = topLevel
	module.Range = fullDocumentRange(document)
	return []DocumentSymbol{*module}
}

func documentSymbolFromSource(document Document, source goxcompiler.SourceSymbol, startOffset, endOffset int) (DocumentSymbol, bool) {
	start, startErr := document.Position(startOffset)
	end, endErr := document.Position(endOffset)
	selectionStart, selectionStartErr := document.Position(source.Source.Range.Start)
	selectionEnd, selectionEndErr := document.Position(source.Source.Range.End)
	if startErr != nil || endErr != nil || selectionStartErr != nil || selectionEndErr != nil {
		return DocumentSymbol{}, false
	}
	selection := Range{Start: selectionStart, End: selectionEnd}
	return DocumentSymbol{
		Name: source.Name, Detail: source.Detail, Kind: documentSymbolKind(source.Kind),
		Range: Range{Start: start, End: end}, SelectionRange: selection,
	}, true
}

func sourceDeclarationOffsets(source string, symbol goxcompiler.SourceSymbol, templates []goxmap.Range) (int, int) {
	selectionStart := symbol.Source.Range.Start
	selectionEnd := symbol.Source.Range.End
	if symbol.Kind == goxcompiler.SymbolModule {
		return 0, len(source)
	}
	start := sourceLineStart(source, selectionStart)
	end := sourceLineEnd(source, selectionEnd)
	linePrefix := strings.TrimSpace(source[start:selectionStart])
	blockDeclaration := symbol.Kind == goxcompiler.SymbolFunction || symbol.Kind == goxcompiler.SymbolClient ||
		symbol.Kind == goxcompiler.SymbolStruct || symbol.Kind == goxcompiler.SymbolInterface ||
		symbol.Kind == goxcompiler.SymbolMethod && strings.HasPrefix(linePrefix, "func")
	if blockDeclaration {
		if opening := sourceOpeningBrace(source, selectionEnd, templates); opening >= 0 {
			if closing := sourceClosingBrace(source, opening, templates); closing >= 0 {
				end = closing + 1
			}
		}
	}
	if symbol.Kind == goxcompiler.SymbolVariable && symbol.Container != "" {
		for _, template := range templates {
			if template.Start >= selectionEnd && template.Start < end && template.End > end {
				end = template.End
				break
			}
		}
	}
	return start, end
}

func sourceLineStart(source string, offset int) int {
	if offset > len(source) {
		offset = len(source)
	}
	if index := strings.LastIndexByte(source[:offset], '\n'); index >= 0 {
		return index + 1
	}
	return 0
}

func sourceLineEnd(source string, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset < len(source) {
		if index := strings.IndexByte(source[offset:], '\n'); index >= 0 {
			return offset + index
		}
	}
	return len(source)
}

func sourceOpeningBrace(source string, offset int, templates []goxmap.Range) int {
	for offset < len(source) {
		if end, ok := sourceTemplateEndAt(offset, templates); ok {
			offset = end
			continue
		}
		if source[offset] == '{' {
			return offset
		}
		offset++
	}
	return -1
}

func sourceClosingBrace(source string, opening int, templates []goxmap.Range) int {
	depth := 0
	quote := byte(0)
	lineComment, blockComment := false, false
	for offset := opening; offset < len(source); offset++ {
		if end, ok := sourceTemplateEndAt(offset, templates); ok {
			offset = end - 1
			continue
		}
		current := source[offset]
		next := byte(0)
		if offset+1 < len(source) {
			next = source[offset+1]
		}
		if lineComment {
			if current == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if current == '*' && next == '/' {
				blockComment = false
				offset++
			}
			continue
		}
		if quote != 0 {
			if current == '\\' && quote != '`' {
				offset++
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '/' && next == '/' {
			lineComment = true
			offset++
			continue
		}
		if current == '/' && next == '*' {
			blockComment = true
			offset++
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		switch current {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return offset
			}
		}
	}
	return -1
}

func sourceTemplateEndAt(offset int, templates []goxmap.Range) (int, bool) {
	for _, template := range templates {
		if offset >= template.Start && offset < template.End {
			return template.End, true
		}
	}
	return 0, false
}

func documentSymbolKind(kind goxcompiler.SourceSymbolKind) int {
	switch kind {
	case goxcompiler.SymbolModule:
		return 2
	case goxcompiler.SymbolClient:
		return 5
	case goxcompiler.SymbolMethod:
		return 6
	case goxcompiler.SymbolField:
		return 8
	case goxcompiler.SymbolFunction:
		return 12
	case goxcompiler.SymbolVariable:
		return 13
	case goxcompiler.SymbolConstant:
		return 14
	case goxcompiler.SymbolInterface:
		return 11
	case goxcompiler.SymbolStruct:
		return 23
	case goxcompiler.SymbolType:
		return 5
	default:
		return 13
	}
}

func sortDocumentSymbols(symbols []DocumentSymbol) {
	sort.SliceStable(symbols, func(left, right int) bool {
		return comparePosition(symbols[left].SelectionRange.Start, symbols[right].SelectionRange.Start) < 0
	})
}

func fullDocumentRange(document Document) Range {
	end, err := document.Position(len(document.Text))
	if err != nil {
		return Range{}
	}
	return Range{Start: Position{}, End: end}
}
