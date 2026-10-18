package lsp

import (
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

type PrepareRenameResult struct {
	Range       Range  `json:"range"`
	Placeholder string `json:"placeholder"`
}

type WorkspaceEdit struct {
	Changes map[string][]CompletionTextEdit `json:"changes"`
}

func (s *Server) prepareRename(document Document, position Position) *PrepareRenameResult {
	if offset, err := document.Offset(position); err == nil && s.contextAt(document, offset).passiveEmbedded() {
		return nil
	}
	if target, ok := s.importAliasRenameTargetAt(document, position); ok {
		return &PrepareRenameResult{Range: target.Range, Placeholder: target.Name}
	}
	if target, ok := s.moduleRenameTargetAt(document, position); ok {
		return &PrepareRenameResult{Range: target.Range, Placeholder: target.Name}
	}
	name, _, err := identifierAtPosition(document, position)
	if err != nil || name == "" || !s.renamableSymbolAt(document, position) {
		return nil
	}
	rangeValue, ok := identifierRangeAtPosition(document, position)
	if !ok {
		return nil
	}
	return &PrepareRenameResult{Range: rangeValue, Placeholder: name}
}

func (s *Server) rename(document Document, position Position, newName string) *WorkspaceEdit {
	edit, _ := s.renameResult(document, position, newName)
	return edit
}

func (s *Server) renameResult(document Document, position Position, newName string) (*WorkspaceEdit, string) {
	oldName := identifierNameAt(document, position)
	if !validRenameIdentifier(newName) {
		return nil, "cannot rename to \"" + newName + "\": invalid identifier"
	}
	if target, ok := s.importAliasRenameTargetAt(document, position); ok {
		return s.renameImportAlias(document, target, newName)
	}
	if target, ok := s.moduleRenameTargetAt(document, position); ok {
		return s.renameModuleDeclaration(document, target, newName)
	}
	if s.prepareRename(document, position) == nil {
		return nil, "symbol cannot be renamed"
	}
	locations := s.references(document, position, true)
	if len(locations) == 0 {
		return nil, "cannot rename \"" + oldName + "\": no references were found"
	}
	if conflict := s.renameConflict(document, position, oldName, newName, locations); conflict != "" {
		return nil, conflict
	}
	changes := make(map[string][]CompletionTextEdit)
	for _, location := range locations {
		changes[location.URI] = append(changes[location.URI], CompletionTextEdit{Range: location.Range, NewText: newName})
	}
	for uri := range changes {
		sort.SliceStable(changes[uri], func(left, right int) bool {
			if changes[uri][left].Range.Start.Line != changes[uri][right].Range.Start.Line {
				return changes[uri][left].Range.Start.Line > changes[uri][right].Range.Start.Line
			}
			return changes[uri][left].Range.Start.Character > changes[uri][right].Range.Start.Character
		})
	}
	return &WorkspaceEdit{Changes: changes}, ""
}

func (s *Server) renameConflict(document Document, position Position, oldName, newName string, locations []Location) string {
	offset, err := document.Offset(position)
	if err != nil {
		return "cannot rename \"" + oldName + "\": invalid source position"
	}
	if target, ok := s.symbols.LocalSymbolAt(document.URI, identifierNameAt(document, position), offset); ok {
		return s.localRenameConflict(document.URI, target, oldName, newName, locations)
	}
	definitions, err := s.definitionsAt(document, position)
	if err != nil || len(definitions) == 0 {
		return "cannot rename \"" + oldName + "\": declaration could not be resolved"
	}
	for _, target := range definitions {
		for _, candidate := range s.symbols.DocumentSymbols(target.URI) {
			if candidate.Symbol.Name != newName || sameIndexedDeclaration(target, candidate) {
				continue
			}
			if sourceSymbolsShareNamespace(target.Symbol, candidate.Symbol) {
				if target.Symbol.Container != "" {
					return "cannot rename \"" + oldName + "\" to \"" + newName + "\": \"" + newName + "\" is already declared on type " + target.Symbol.Container
				}
				return "cannot rename \"" + oldName + "\" to \"" + newName + "\": \"" + newName + "\" is already declared in this module"
			}
		}
		if s.documentImportNameExists(target.URI, newName) {
			return "cannot rename \"" + oldName + "\" to \"" + newName + "\": the new name conflicts with an import"
		}
		for _, local := range s.symbols.LocalSymbols(target.URI) {
			if local.Name == newName {
				return "cannot rename \"" + oldName + "\" to \"" + newName + "\": the new name would be shadowed by a local declaration"
			}
		}
	}
	if s.renameLocationsCapturedByLocal(newName, locations, nil) {
		return "cannot rename \"" + oldName + "\" to \"" + newName + "\": the new name would change symbol resolution due to shadowing"
	}
	return ""
}

func identifierNameAt(document Document, position Position) string {
	name, _, err := identifierAtPosition(document, position)
	if err != nil {
		return ""
	}
	return name
}

func (s *Server) localRenameConflict(uri string, target goxcompiler.LocalSymbol, oldName, newName string, locations []Location) string {
	for _, candidate := range s.symbols.LocalSymbols(uri) {
		if candidate.Name != newName || sameLocalDeclaration(target, candidate) {
			continue
		}
		if localScopesIntersect(target, candidate) {
			if target.Scope.Range == candidate.Scope.Range {
				return "cannot rename \"" + oldName + "\" to \"" + newName + "\": \"" + newName + "\" is already declared in this scope"
			}
			return "cannot rename \"" + oldName + "\" to \"" + newName + "\": the new name would introduce shadowing in a nested scope"
		}
	}
	for _, candidate := range s.symbols.DocumentSymbols(uri) {
		if candidate.Symbol.Name == newName && candidate.Symbol.Container == "" {
			return "cannot rename \"" + oldName + "\" to \"" + newName + "\": the new name would shadow a module declaration"
		}
	}
	if s.documentImportNameExists(uri, newName) {
		return "cannot rename \"" + oldName + "\" to \"" + newName + "\": the new name would shadow an import"
	}
	if s.renameLocationsCapturedByLocal(newName, locations, &target) {
		return "cannot rename \"" + oldName + "\" to \"" + newName + "\": the new name would change symbol resolution due to shadowing"
	}
	return ""
}

func (s *Server) renameLocationsCapturedByLocal(newName string, locations []Location, target *goxcompiler.LocalSymbol) bool {
	for _, location := range locations {
		document, ok := s.symbols.Document(location.URI)
		if !ok {
			continue
		}
		offset, err := document.Offset(location.Range.Start)
		if err != nil {
			return true
		}
		candidate, ok := s.symbols.LocalSymbolAt(location.URI, newName, offset)
		if !ok {
			continue
		}
		if target == nil || !sameLocalDeclaration(*target, candidate) {
			return true
		}
	}
	return false
}

func (s *Server) documentImportNameExists(uri, name string) bool {
	for _, declaration := range s.symbols.Imports(uri) {
		if declaration.Name == name {
			return true
		}
	}
	for _, declaration := range s.symbols.GoImports(uri) {
		alias := declaration.Alias
		if alias == "" {
			alias = filepath.Base(declaration.Path)
		}
		if alias == name {
			return true
		}
	}
	return false
}

func sameLocalDeclaration(left, right goxcompiler.LocalSymbol) bool {
	return left.Source.URI == right.Source.URI && left.Source.Range == right.Source.Range
}

func localScopesIntersect(left, right goxcompiler.LocalSymbol) bool {
	return left.Scope.Range == right.Scope.Range ||
		left.Scope.Range.Contains(right.Source.Range.Start) ||
		right.Scope.Range.Contains(left.Source.Range.Start)
}

func sameIndexedDeclaration(left, right IndexedSymbol) bool {
	return left.URI == right.URI && left.Symbol.Source.URI == right.Symbol.Source.URI &&
		left.Symbol.Source.Range == right.Symbol.Source.Range
}

func sourceSymbolsShareNamespace(left, right goxcompiler.SourceSymbol) bool {
	if left.Container == "" || right.Container == "" {
		return left.Container == "" && right.Container == ""
	}
	return strings.TrimPrefix(left.Container, "*") == strings.TrimPrefix(right.Container, "*")
}

func (s *Server) renamableSymbolAt(document Document, position Position) bool {
	if renamable, handled := s.semanticRenamableAt(document, position); handled {
		return renamable
	}
	offset, err := document.Offset(position)
	if err != nil {
		return false
	}
	name, _, err := identifierAtPosition(document, position)
	if err != nil || name == "" {
		return false
	}
	if _, ok := s.symbols.LocalSymbolAt(document.URI, name, offset); ok {
		return true
	}
	if len(s.importedGoDefinitionLocations(document, position)) > 0 {
		return false
	}
	definitions, err := s.definitionsAt(document, position)
	if err != nil || len(definitions) == 0 {
		return false
	}
	for _, definition := range definitions {
		if definition.Symbol.Kind == goxcompiler.SymbolModule {
			return false
		}
	}
	return true
}

func identifierRangeAtPosition(document Document, position Position) (Range, bool) {
	offset, err := document.Offset(position)
	if err != nil {
		return Range{}, false
	}
	start, end, ok := identifierOffsetsAt(document.Text, offset)
	if !ok {
		return Range{}, false
	}
	startPosition, startErr := document.Position(start)
	endPosition, endErr := document.Position(end)
	if startErr != nil || endErr != nil {
		return Range{}, false
	}
	return Range{Start: startPosition, End: endPosition}, true
}

func identifierOffsetsAt(source string, offset int) (int, int, bool) {
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

func validRenameIdentifier(name string) bool {
	if name == "" || name == "_" || token.Lookup(name).IsKeyword() {
		return false
	}
	for index, value := range name {
		if index == 0 {
			if value != '_' && !unicode.IsLetter(value) {
				return false
			}
			continue
		}
		if value != '_' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
			return false
		}
	}
	return true
}
