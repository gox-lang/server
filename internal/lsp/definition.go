package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type LocationLink struct {
	OriginSelectionRange Range  `json:"originSelectionRange"`
	TargetURI            string `json:"targetUri"`
	TargetRange          Range  `json:"targetRange"`
	TargetSelectionRange Range  `json:"targetSelectionRange"`
}

func (s *Server) definitionResult(document Document, position Position) any {
	if offset, err := document.Offset(position); err == nil && s.contextAt(document, offset).passiveEmbedded() {
		return []Location{}
	}
	if links := s.clientSourceDefinitionLinks(document, position); len(links) > 0 {
		return links
	}
	if links := s.importDefinitionLinks(document, position); len(links) > 0 {
		return links
	}
	return s.definitionLocations(document, position)
}

func (s *Server) definitionLocations(document Document, position Position) []Location {
	if offset, err := document.Offset(position); err == nil && s.contextAt(document, offset).passiveEmbedded() {
		return []Location{}
	}
	if locations, handled := s.semanticDefinitionLocations(document, position); handled {
		return locations
	}
	if locations := s.semanticGoDefinitionLocations(document, position); len(locations) > 0 {
		return locations
	}
	if locations := s.importedGoDefinitionLocations(document, position); len(locations) > 0 {
		return locations
	}
	definitions, err := s.definitionsAt(document, position)
	if err != nil {
		return nil
	}
	return s.locationsForDefinitions(definitions)
}

func (s *Server) definitionsAt(document Document, position Position) ([]IndexedSymbol, error) {
	name, prefix, err := identifierAtPosition(document, position)
	if err != nil || name == "" {
		return nil, err
	}
	offset, err := document.Offset(position)
	if err != nil {
		return nil, err
	}
	if s.contextAt(document, offset).passiveEmbedded() {
		return nil, nil
	}
	selector, hasSelector := selectorAtOffset(document.Text, offset)
	if hasSelector && selector.cursorOnMember {
		if imported := s.importedMemberDefinitions(document, selector.qualifier, selector.member); len(imported) > 0 {
			return imported, nil
		}
		if reference, ok := s.symbols.SelectorReferenceAt(document.URI, offset); ok {
			return s.selectorDefinitions(document.URI, reference), nil
		}
		if local, ok := s.symbols.LocalSymbolAt(document.URI, selector.qualifier, offset); ok {
			if members := s.importedValueMemberDefinitions(document, local.Type, selector.member); len(members) > 0 {
				return members, nil
			}
		}
	}
	if local, ok := s.symbols.LocalSymbolAt(document.URI, name, offset); ok {
		return []IndexedSymbol{indexedLocalSymbol(document.URI, local)}, nil
	}
	if hasSelector && !selector.cursorOnMember {
		return s.importedModuleDefinitions(document, selector.qualifier), nil
	}
	if imported := s.importedModuleDefinitions(document, name); len(imported) > 0 {
		return imported, nil
	}
	definitions := s.symbols.Definitions(name)
	if reference, ok := s.symbols.ReferenceAt(document.URI, offset); ok {
		definitions = s.referenceDefinitions(document, reference)
	} else {
		definitions = filterDefinitions(definitions, func(candidate IndexedSymbol) bool {
			if candidate.URI != document.URI {
				return false
			}
			if !definitionMatchesPrefix(candidate.Symbol.Kind, prefix) {
				return false
			}
			if prefix == 0 && candidate.Symbol.Kind == goxcompiler.SymbolModule {
				return candidate.URI == document.URI && (candidate.Symbol.Source.Range.Contains(offset) || offset == candidate.Symbol.Source.Range.End)
			}
			return true
		})
	}
	return preferCurrentDeclaration(document.URI, offset, definitions), nil
}

func (s *Server) importedValueMemberDefinitions(document Document, typeName, member string) []IndexedSymbol {
	qualifier, container, ok := importedNamedType(typeName)
	if !ok {
		return nil
	}
	targetURI, ok := s.importedModuleURI(document, qualifier)
	if !ok {
		return nil
	}
	return filterDefinitions(s.symbols.Definitions(member), func(candidate IndexedSymbol) bool {
		return candidate.URI == targetURI && candidate.Symbol.Container == container &&
			candidate.Symbol.Exported && (candidate.Symbol.Kind == goxcompiler.SymbolField || candidate.Symbol.Kind == goxcompiler.SymbolMethod)
	})
}

func importedNamedType(typeName string) (string, string, bool) {
	typeName = strings.TrimSpace(typeName)
	for strings.HasPrefix(typeName, "*") {
		typeName = strings.TrimSpace(strings.TrimPrefix(typeName, "*"))
	}
	separator := strings.IndexByte(typeName, '.')
	if separator <= 0 || separator == len(typeName)-1 || strings.Contains(typeName[separator+1:], ".") {
		return "", "", false
	}
	return typeName[:separator], typeName[separator+1:], true
}

func (s *Server) selectorDefinitions(uri string, reference goxcompiler.SelectorReference) []IndexedSymbol {
	return filterDefinitions(s.symbols.DocumentSymbols(uri), func(candidate IndexedSymbol) bool {
		return candidate.Symbol.Name == reference.Name &&
			candidate.Symbol.Kind == reference.Kind &&
			candidate.Symbol.Container == reference.Container &&
			candidate.Symbol.Source.Range == reference.Target.Range
	})
}

func indexedLocalSymbol(uri string, local goxcompiler.LocalSymbol) IndexedSymbol {
	return IndexedSymbol{
		URI: uri,
		Symbol: goxcompiler.SourceSymbol{
			Name:   local.Name,
			Kind:   local.Kind,
			Source: local.Source,
			Type:   local.Type,
			Detail: local.Detail,
		},
	}
}

type sourceSelector struct {
	qualifier      string
	member         string
	cursorOnMember bool
}

func selectorAtOffset(source string, offset int) (sourceSelector, bool) {
	start, end, ok := identifierBoundsAtOffset(source, offset)
	if !ok {
		return sourceSelector{}, false
	}
	left := skipSourceSpacesBackward(source, start)
	if left > 0 && source[left-1] == '.' {
		qualifierEnd := skipSourceSpacesBackward(source, left-1)
		qualifierStart := identifierStartBefore(source, qualifierEnd)
		if qualifierStart < qualifierEnd {
			return sourceSelector{qualifier: source[qualifierStart:qualifierEnd], member: source[start:end], cursorOnMember: true}, true
		}
	}
	right := skipSourceSpacesForward(source, end)
	if right < len(source) && source[right] == '.' {
		memberStart := skipSourceSpacesForward(source, right+1)
		memberEnd := identifierEndAfter(source, memberStart)
		if memberStart < memberEnd {
			return sourceSelector{qualifier: source[start:end], member: source[memberStart:memberEnd]}, true
		}
	}
	return sourceSelector{}, false
}

func identifierBoundsAtOffset(source string, offset int) (int, int, bool) {
	if offset < 0 || offset > len(source) {
		return 0, 0, false
	}
	probe := offset
	if probe == len(source) || !identifierRuneAt(source, probe) {
		if probe == 0 {
			return 0, 0, false
		}
		value, size := utf8.DecodeLastRuneInString(source[:probe])
		if !isIdentifierRune(value) {
			return 0, 0, false
		}
		probe -= size
	}
	start := probe
	for start > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:start])
		if !isIdentifierRune(value) {
			break
		}
		start -= size
	}
	end := probe
	for end < len(source) {
		value, size := utf8.DecodeRuneInString(source[end:])
		if !isIdentifierRune(value) {
			break
		}
		end += size
	}
	return start, end, start < end
}

func skipSourceSpacesBackward(source string, offset int) int {
	for offset > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:offset])
		if !unicode.IsSpace(value) {
			break
		}
		offset -= size
	}
	return offset
}

func skipSourceSpacesForward(source string, offset int) int {
	for offset < len(source) {
		value, size := utf8.DecodeRuneInString(source[offset:])
		if !unicode.IsSpace(value) {
			break
		}
		offset += size
	}
	return offset
}

func identifierStartBefore(source string, end int) int {
	start := end
	for start > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:start])
		if !isIdentifierRune(value) {
			break
		}
		start -= size
	}
	return start
}

func identifierEndAfter(source string, start int) int {
	end := start
	for end < len(source) {
		value, size := utf8.DecodeRuneInString(source[end:])
		if !isIdentifierRune(value) {
			break
		}
		end += size
	}
	return end
}

func (s *Server) importedModuleDefinitions(document Document, qualifier string) []IndexedSymbol {
	targetURI, ok := s.importedModuleURI(document, qualifier)
	if !ok {
		return nil
	}
	return filterDefinitions(s.symbols.DocumentSymbols(targetURI), func(candidate IndexedSymbol) bool {
		return candidate.Symbol.Kind == goxcompiler.SymbolModule
	})
}

func (s *Server) importedMemberDefinitions(document Document, qualifier, member string) []IndexedSymbol {
	targetURI, ok := s.importedModuleURI(document, qualifier)
	if !ok {
		return nil
	}
	return filterDefinitions(s.symbols.Definitions(member), func(candidate IndexedSymbol) bool {
		return candidate.URI == targetURI && candidate.Symbol.Exported && moduleMemberKind(candidate.Symbol.Kind)
	})
}

func (s *Server) importedModuleURI(document Document, qualifier string) (string, bool) {
	_, targetURI, ok := s.resolveImportedModule(document, qualifier)
	return targetURI, ok
}

func (s *Server) resolveImportedModule(document Document, qualifier string) (goxcompiler.GoxImport, string, bool) {
	root := s.projectRoot(document.Path)
	for _, importDeclaration := range s.symbols.Imports(document.URI) {
		if importDeclaration.Name == "_" || importDeclaration.Name == "." {
			continue
		}
		resolved, err := goxcompiler.ResolveGoxImport(root, document.Path, importDeclaration.Path)
		if err != nil {
			continue
		}
		targetURI := FileURI(resolved)
		if importDeclaration.Name != "" {
			if moduleQualifierMatches(qualifier, importDeclaration.Name) {
				return importDeclaration, targetURI, true
			}
			continue
		}
		if s.targetModuleMatchesQualifier(targetURI, qualifier) {
			return importDeclaration, targetURI, true
		}
	}
	return goxcompiler.GoxImport{}, "", false
}

func moduleMemberKind(kind goxcompiler.SourceSymbolKind) bool {
	switch kind {
	case goxcompiler.SymbolFunction, goxcompiler.SymbolType, goxcompiler.SymbolStruct, goxcompiler.SymbolInterface, goxcompiler.SymbolVariable, goxcompiler.SymbolConstant:
		return true
	}
	return false
}

func preferCurrentDeclaration(uri string, offset int, definitions []IndexedSymbol) []IndexedSymbol {
	for _, definition := range definitions {
		if definition.URI == uri && (definition.Symbol.Source.Range.Contains(offset) || offset == definition.Symbol.Source.Range.End) {
			return []IndexedSymbol{definition}
		}
	}
	return definitions
}

func (s *Server) locationsForDefinitions(definitions []IndexedSymbol) []Location {
	locations := make([]Location, 0, len(definitions))
	for _, definition := range definitions {
		target, ok := s.symbols.Document(definition.URI)
		if !ok {
			continue
		}
		start, startErr := target.Position(definition.Symbol.Source.Range.Start)
		end, endErr := target.Position(definition.Symbol.Source.Range.End)
		if startErr != nil || endErr != nil {
			continue
		}
		locations = append(locations, Location{URI: definition.URI, Range: Range{Start: start, End: end}})
	}
	return locations
}

func (s *Server) referenceDefinitions(document Document, reference goxcompiler.SourceReference) []IndexedSymbol {
	candidates := s.symbols.Definitions(reference.Name)
	if reference.Kind == goxcompiler.ReferenceClient {
		return filterDefinitions(candidates, func(candidate IndexedSymbol) bool {
			return candidate.URI == document.URI && candidate.Symbol.Kind == goxcompiler.SymbolClient
		})
	}
	if reference.Qualifier != "" {
		return s.importedDefinitions(document, reference, candidates)
	}

	locals := filterDefinitions(candidates, func(candidate IndexedSymbol) bool {
		return candidate.URI == document.URI && candidate.Symbol.Kind == goxcompiler.SymbolVariable && candidate.Symbol.Container == reference.Container && candidate.Symbol.Source.Range.Start <= reference.Source.Range.Start
	})
	if len(locals) > 0 {
		best := locals[:1]
		for _, candidate := range locals[1:] {
			if candidate.Symbol.Source.Range.Start > best[0].Symbol.Source.Range.Start {
				best[0] = candidate
			}
		}
		return best
	}
	local := filterDefinitions(candidates, func(candidate IndexedSymbol) bool {
		return candidate.URI == document.URI && componentDefinitionKind(candidate.Symbol.Kind)
	})
	if len(local) > 0 {
		return local
	}
	return nil
}

func (s *Server) importedDefinitions(document Document, reference goxcompiler.SourceReference, candidates []IndexedSymbol) []IndexedSymbol {
	root := s.projectRoot(document.Path)
	for _, importDeclaration := range s.symbols.Imports(document.URI) {
		if importDeclaration.Name != "" && !moduleQualifierMatches(reference.Qualifier, importDeclaration.Name) {
			continue
		}
		resolved, err := goxcompiler.ResolveGoxImport(root, document.Path, importDeclaration.Path)
		if err != nil {
			continue
		}
		targetURI := FileURI(resolved)
		if importDeclaration.Name == "" && !s.targetModuleMatchesQualifier(targetURI, reference.Qualifier) {
			continue
		}
		return filterDefinitions(candidates, func(candidate IndexedSymbol) bool {
			return candidate.URI == targetURI && candidate.Symbol.Exported && componentDefinitionKind(candidate.Symbol.Kind)
		})
	}
	return nil
}

func (s *Server) targetModuleMatchesQualifier(uri, qualifier string) bool {
	for _, symbol := range s.symbols.DocumentSymbols(uri) {
		if symbol.Symbol.Kind == goxcompiler.SymbolModule && moduleQualifierMatches(qualifier, symbol.Symbol.Name) {
			return true
		}
	}
	return false
}

func (s *Server) projectRoot(path string) string {
	directory := filepath.Dir(path)
	for {
		if info, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil && !info.IsDir() {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return filepath.Dir(path)
}

func moduleQualifierMatches(qualifier, module string) bool {
	if qualifier == module {
		return true
	}
	runes := []rune(module)
	if len(runes) == 0 {
		return false
	}
	runes[0] = unicode.ToLower(runes[0])
	return qualifier == string(runes)
}

func componentDefinitionKind(kind goxcompiler.SourceSymbolKind) bool {
	return kind == goxcompiler.SymbolFunction || kind == goxcompiler.SymbolMethod || kind == goxcompiler.SymbolVariable
}

func filterDefinitions(input []IndexedSymbol, keep func(IndexedSymbol) bool) []IndexedSymbol {
	output := make([]IndexedSymbol, 0, len(input))
	for _, candidate := range input {
		if keep(candidate) {
			output = append(output, candidate)
		}
	}
	return output
}

func definitionMatchesPrefix(kind goxcompiler.SourceSymbolKind, prefix rune) bool {
	switch prefix {
	case '@':
		return kind == goxcompiler.SymbolClient
	case '&':
		return kind == goxcompiler.SymbolFunction || kind == goxcompiler.SymbolMethod || kind == goxcompiler.SymbolVariable
	default:
		return true
	}
}

func identifierAtPosition(document Document, position Position) (string, rune, error) {
	offset, err := document.Offset(position)
	if err != nil {
		return "", 0, err
	}
	text := document.Text
	probe := offset
	if probe == len(text) || !identifierRuneAt(text, probe) {
		if probe == 0 {
			return "", 0, nil
		}
		value, size := utf8.DecodeLastRuneInString(text[:probe])
		if !isIdentifierRune(value) {
			return "", 0, nil
		}
		probe -= size
	}

	start := probe
	for start > 0 {
		value, size := utf8.DecodeLastRuneInString(text[:start])
		if !isIdentifierRune(value) {
			break
		}
		start -= size
	}
	end := probe
	for end < len(text) {
		value, size := utf8.DecodeRuneInString(text[end:])
		if !isIdentifierRune(value) {
			break
		}
		end += size
	}

	prefix := rune(0)
	if start > 0 {
		value, _ := utf8.DecodeLastRuneInString(text[:start])
		if value == '@' || value == '&' || value == ':' {
			prefix = value
		}
	}
	return text[start:end], prefix, nil
}

func identifierRuneAt(text string, offset int) bool {
	if offset < 0 || offset >= len(text) {
		return false
	}
	value, _ := utf8.DecodeRuneInString(text[offset:])
	return isIdentifierRune(value)
}

func isIdentifierRune(value rune) bool {
	return value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}
