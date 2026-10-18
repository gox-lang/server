package lsp

import (
	"sort"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type hierarchyData struct {
	URI       string                       `json:"uri"`
	Name      string                       `json:"name"`
	Kind      goxcompiler.SourceSymbolKind `json:"kind"`
	Container string                       `json:"container,omitempty"`
	Start     int                          `json:"start"`
}

type CallHierarchyItem struct {
	Name           string        `json:"name"`
	Kind           int           `json:"kind"`
	Detail         string        `json:"detail,omitempty"`
	URI            string        `json:"uri"`
	Range          Range         `json:"range"`
	SelectionRange Range         `json:"selectionRange"`
	Data           hierarchyData `json:"data"`
}

type CallHierarchyIncomingCallsParams struct {
	Item CallHierarchyItem `json:"item"`
}
type CallHierarchyOutgoingCallsParams struct {
	Item CallHierarchyItem `json:"item"`
}
type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}
type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

type TypeHierarchyItem struct {
	Name           string        `json:"name"`
	Kind           int           `json:"kind"`
	Detail         string        `json:"detail,omitempty"`
	URI            string        `json:"uri"`
	Range          Range         `json:"range"`
	SelectionRange Range         `json:"selectionRange"`
	Data           hierarchyData `json:"data"`
}

type TypeHierarchySupertypesParams struct {
	Item TypeHierarchyItem `json:"item"`
}
type TypeHierarchySubtypesParams struct {
	Item TypeHierarchyItem `json:"item"`
}

func (s *Server) prepareCallHierarchy(document Document, position Position) []CallHierarchyItem {
	definitions, err := s.definitionsAt(document, position)
	if err != nil {
		return []CallHierarchyItem{}
	}
	var items []CallHierarchyItem
	for _, definition := range definitions {
		if definition.Symbol.Kind != goxcompiler.SymbolFunction && definition.Symbol.Kind != goxcompiler.SymbolMethod {
			continue
		}
		if item, ok := s.callHierarchyItem(definition); ok {
			items = append(items, item)
		}
	}
	return items
}

func (s *Server) incomingCalls(item CallHierarchyItem) []CallHierarchyIncomingCall {
	target, ok := s.hierarchySymbol(item.Data)
	if !ok {
		return []CallHierarchyIncomingCall{}
	}
	targetDocument, ok := s.symbols.Document(target.URI)
	if !ok {
		return []CallHierarchyIncomingCall{}
	}
	position, err := targetDocument.Position(target.Symbol.Source.Range.Start)
	if err != nil {
		return []CallHierarchyIncomingCall{}
	}
	grouped := map[hierarchyData][]Range{}
	for _, location := range s.references(targetDocument, position, false) {
		document, ok := s.symbols.Document(location.URI)
		if !ok {
			continue
		}
		offset, err := document.Offset(location.Range.Start)
		if err != nil || !isCallOccurrence(document.Text, offset) {
			continue
		}
		caller, ok := s.enclosingCallable(document, offset)
		if !ok {
			continue
		}
		data := hierarchyDataFor(caller)
		grouped[data] = append(grouped[data], location.Range)
	}
	result := make([]CallHierarchyIncomingCall, 0, len(grouped))
	for data, ranges := range grouped {
		caller, ok := s.hierarchySymbol(data)
		if !ok {
			continue
		}
		from, ok := s.callHierarchyItem(caller)
		if ok {
			result = append(result, CallHierarchyIncomingCall{From: from, FromRanges: ranges})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return hierarchyItemLess(result[i].From.Data, result[j].From.Data) })
	return result
}

func (s *Server) outgoingCalls(item CallHierarchyItem) []CallHierarchyOutgoingCall {
	caller, ok := s.hierarchySymbol(item.Data)
	if !ok {
		return []CallHierarchyOutgoingCall{}
	}
	document, ok := s.symbols.Document(caller.URI)
	if !ok {
		return []CallHierarchyOutgoingCall{}
	}
	start, end := sourceDeclarationOffsets(document.Text, caller.Symbol, s.symbols.TemplateRanges(document.URI))
	grouped := map[hierarchyData][]Range{}
	for offset := start; offset < end; {
		nameStart, nameEnd, found := nextSourceIdentifier(document.Text, offset, end)
		if !found {
			break
		}
		offset = nameEnd
		if nameStart == caller.Symbol.Source.Range.Start || !isCallOccurrence(document.Text, nameStart) {
			continue
		}
		position, err := document.Position(nameStart)
		if err != nil {
			continue
		}
		definitions, err := s.definitionsAt(document, position)
		if err != nil {
			continue
		}
		fromStart, _ := document.Position(nameStart)
		fromEnd, _ := document.Position(nameEnd)
		for _, target := range definitions {
			if target.Symbol.Kind != goxcompiler.SymbolFunction && target.Symbol.Kind != goxcompiler.SymbolMethod {
				continue
			}
			data := hierarchyDataFor(target)
			grouped[data] = append(grouped[data], Range{Start: fromStart, End: fromEnd})
		}
	}
	result := make([]CallHierarchyOutgoingCall, 0, len(grouped))
	for data, ranges := range grouped {
		target, ok := s.hierarchySymbol(data)
		if !ok {
			continue
		}
		to, ok := s.callHierarchyItem(target)
		if ok {
			result = append(result, CallHierarchyOutgoingCall{To: to, FromRanges: ranges})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return hierarchyItemLess(result[i].To.Data, result[j].To.Data) })
	return result
}

func (s *Server) prepareTypeHierarchy(document Document, position Position) []TypeHierarchyItem {
	definitions, err := s.definitionsAt(document, position)
	if err != nil {
		return []TypeHierarchyItem{}
	}
	var result []TypeHierarchyItem
	for _, definition := range definitions {
		if definition.Symbol.Kind == goxcompiler.SymbolMethod {
			if owner, ok := s.containerType(definition.URI, definition.Symbol.Container); ok {
				definition = owner
			}
		} else if !sourceTypeKind(definition.Symbol.Kind) {
			resolved := s.resolveTypeSymbols(document, definition.Symbol.Type)
			if len(resolved) == 0 {
				continue
			}
			definition = resolved[0]
		}
		if item, ok := s.typeHierarchyItem(definition); ok {
			result = append(result, item)
		}
	}
	return result
}

func (s *Server) typeHierarchySupertypes(item TypeHierarchyItem) []TypeHierarchyItem {
	target, ok := s.hierarchySymbol(item.Data)
	if !ok || target.Symbol.Kind == goxcompiler.SymbolInterface {
		return []TypeHierarchyItem{}
	}
	return s.typeHierarchyItems(s.implementationsForSymbol(target))
}

func (s *Server) typeHierarchySubtypes(item TypeHierarchyItem) []TypeHierarchyItem {
	target, ok := s.hierarchySymbol(item.Data)
	if !ok || target.Symbol.Kind != goxcompiler.SymbolInterface {
		return []TypeHierarchyItem{}
	}
	return s.typeHierarchyItems(s.implementationsForSymbol(target))
}

func (s *Server) typeHierarchyItems(symbols []IndexedSymbol) []TypeHierarchyItem {
	var result []TypeHierarchyItem
	for _, symbol := range uniqueIndexedSymbols(symbols) {
		if item, ok := s.typeHierarchyItem(symbol); ok {
			result = append(result, item)
		}
	}
	return result
}

func (s *Server) callHierarchyItem(symbol IndexedSymbol) (CallHierarchyItem, bool) {
	rangeValue, selection, ok := s.hierarchyRanges(symbol)
	if !ok {
		return CallHierarchyItem{}, false
	}
	return CallHierarchyItem{Name: symbol.Symbol.Name, Kind: documentSymbolKind(symbol.Symbol.Kind), Detail: symbol.Symbol.Detail, URI: symbol.URI, Range: rangeValue, SelectionRange: selection, Data: hierarchyDataFor(symbol)}, true
}

func (s *Server) typeHierarchyItem(symbol IndexedSymbol) (TypeHierarchyItem, bool) {
	rangeValue, selection, ok := s.hierarchyRanges(symbol)
	if !ok {
		return TypeHierarchyItem{}, false
	}
	return TypeHierarchyItem{Name: symbol.Symbol.Name, Kind: documentSymbolKind(symbol.Symbol.Kind), Detail: symbol.Symbol.Detail, URI: symbol.URI, Range: rangeValue, SelectionRange: selection, Data: hierarchyDataFor(symbol)}, true
}

func (s *Server) hierarchyRanges(symbol IndexedSymbol) (Range, Range, bool) {
	document, ok := s.symbols.Document(symbol.URI)
	if !ok {
		return Range{}, Range{}, false
	}
	start, end := sourceDeclarationOffsets(document.Text, symbol.Symbol, s.symbols.TemplateRanges(symbol.URI))
	rangeStart, firstErr := document.Position(start)
	rangeEnd, secondErr := document.Position(end)
	selectionStart, thirdErr := document.Position(symbol.Symbol.Source.Range.Start)
	selectionEnd, fourthErr := document.Position(symbol.Symbol.Source.Range.End)
	if firstErr != nil || secondErr != nil || thirdErr != nil || fourthErr != nil {
		return Range{}, Range{}, false
	}
	return Range{Start: rangeStart, End: rangeEnd}, Range{Start: selectionStart, End: selectionEnd}, true
}

func (s *Server) hierarchySymbol(data hierarchyData) (IndexedSymbol, bool) {
	for _, symbol := range s.symbols.DocumentSymbols(data.URI) {
		if symbol.Symbol.Name == data.Name && symbol.Symbol.Kind == data.Kind && symbol.Symbol.Container == data.Container && symbol.Symbol.Source.Range.Start == data.Start {
			return symbol, true
		}
	}
	return IndexedSymbol{}, false
}

func (s *Server) enclosingCallable(document Document, offset int) (IndexedSymbol, bool) {
	bestSize := len(document.Text) + 1
	var best IndexedSymbol
	found := false
	for _, symbol := range s.symbols.DocumentSymbols(document.URI) {
		if symbol.Symbol.Kind != goxcompiler.SymbolFunction && symbol.Symbol.Kind != goxcompiler.SymbolMethod {
			continue
		}
		start, end := sourceDeclarationOffsets(document.Text, symbol.Symbol, s.symbols.TemplateRanges(document.URI))
		if offset < start || offset > end || end-start >= bestSize {
			continue
		}
		best, bestSize, found = symbol, end-start, true
	}
	return best, found
}

func hierarchyDataFor(symbol IndexedSymbol) hierarchyData {
	return hierarchyData{URI: symbol.URI, Name: symbol.Symbol.Name, Kind: symbol.Symbol.Kind, Container: symbol.Symbol.Container, Start: symbol.Symbol.Source.Range.Start}
}
func hierarchyItemLess(left, right hierarchyData) bool {
	if left.URI != right.URI {
		return left.URI < right.URI
	}
	return left.Start < right.Start
}

func isCallOccurrence(source string, offset int) bool {
	start, end, ok := identifierOffsetsAt(source, offset)
	if !ok {
		return false
	}
	next := skipSourceSpacesForward(source, end)
	if next < len(source) && source[next] == '(' {
		return true
	}
	return templateSymbolTagAt(source, start)
}

func nextSourceIdentifier(source string, offset, limit int) (int, int, bool) {
	for offset < limit {
		value := source[offset]
		if value == '"' || value == '\'' || value == '`' {
			offset = skipQuoted(source, offset)
			continue
		}
		if offset+1 < limit && source[offset:offset+2] == "//" {
			if end := strings.IndexByte(source[offset:], '\n'); end >= 0 {
				offset += end
				continue
			}
			return 0, 0, false
		}
		if offset+1 < limit && source[offset:offset+2] == "/*" {
			if end := strings.Index(source[offset+2:], "*/"); end >= 0 {
				offset += end + 4
				continue
			}
			return 0, 0, false
		}
		if value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' {
			start := offset
			for offset < limit && (source[offset] == '_' || source[offset] >= 'a' && source[offset] <= 'z' || source[offset] >= 'A' && source[offset] <= 'Z' || source[offset] >= '0' && source[offset] <= '9') {
				offset++
			}
			return start, offset, true
		}
		offset++
	}
	return 0, 0, false
}
