package lsp

import (
	"path/filepath"
	"sort"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type importAliasRenameTarget struct {
	Name  string
	Path  string
	Range Range
	Start int
	End   int
	Gox   bool
}

type moduleRenameTarget struct {
	Name  string
	Range Range
}

func (s *Server) importAliasRenameTargetAt(document Document, position Position) (importAliasRenameTarget, bool) {
	offset, err := document.Offset(position)
	if err != nil {
		return importAliasRenameTarget{}, false
	}
	name, _, err := identifierAtPosition(document, position)
	if err != nil || name == "" {
		return importAliasRenameTarget{}, false
	}
	for _, declaration := range s.symbols.Imports(document.URI) {
		if declaration.Name != name || declaration.Name == "_" || declaration.Name == "." {
			continue
		}
		if target, ok := explicitAliasTarget(document, declaration.Source.Range.Start, name, declaration.Path, true); ok &&
			(offset >= target.Start && offset <= target.End || qualifierOccurrence(document.Text, offset, name)) {
			return target, true
		}
	}
	for _, declaration := range s.symbols.GoImports(document.URI) {
		if declaration.Alias != name || declaration.Alias == "_" || declaration.Alias == "." {
			continue
		}
		if target, ok := explicitAliasTarget(document, declaration.Source.Range.Start, name, declaration.Path, false); ok &&
			(offset >= target.Start && offset <= target.End || qualifierOccurrence(document.Text, offset, name)) {
			return target, true
		}
	}
	return importAliasRenameTarget{}, false
}

func explicitAliasTarget(document Document, pathStart int, alias, path string, gox bool) (importAliasRenameTarget, bool) {
	lineStart := strings.LastIndex(document.Text[:pathStart], "\n") + 1
	prefix := document.Text[lineStart:pathStart]
	occurrences := scanIdentifierOccurrences(prefix, alias)
	if len(occurrences) == 0 {
		return importAliasRenameTarget{}, false
	}
	occurrence := occurrences[len(occurrences)-1]
	start, end := lineStart+occurrence.Start, lineStart+occurrence.End
	startPosition, startErr := document.Position(start)
	endPosition, endErr := document.Position(end)
	if startErr != nil || endErr != nil {
		return importAliasRenameTarget{}, false
	}
	return importAliasRenameTarget{Name: alias, Path: path, Range: Range{Start: startPosition, End: endPosition}, Start: start, End: end, Gox: gox}, true
}

func qualifierOccurrence(source string, offset int, name string) bool {
	start, end, ok := identifierOffsetsAt(source, offset)
	return ok && source[start:end] == name && end < len(source) && source[end] == '.'
}

func (s *Server) moduleRenameTargetAt(document Document, position Position) (moduleRenameTarget, bool) {
	offset, err := document.Offset(position)
	if err != nil {
		return moduleRenameTarget{}, false
	}
	for _, indexed := range s.symbols.DocumentSymbols(document.URI) {
		if indexed.Symbol.Kind != goxcompiler.SymbolModule || !indexed.Symbol.Source.Range.Contains(offset) {
			continue
		}
		start, startErr := document.Position(indexed.Symbol.Source.Range.Start)
		end, endErr := document.Position(indexed.Symbol.Source.Range.End)
		if startErr == nil && endErr == nil {
			return moduleRenameTarget{Name: indexed.Symbol.Name, Range: Range{Start: start, End: end}}, true
		}
	}
	return moduleRenameTarget{}, false
}

func (s *Server) renameImportAlias(document Document, target importAliasRenameTarget, newName string) (*WorkspaceEdit, string) {
	if target.Name == newName {
		return &WorkspaceEdit{Changes: map[string][]CompletionTextEdit{}}, ""
	}
	if conflict := s.importAliasRenameConflict(document, target, newName); conflict != "" {
		return nil, conflict
	}
	edits := []CompletionTextEdit{{Range: target.Range, NewText: newName}}
	for _, occurrence := range scanIdentifierOccurrences(document.Text, target.Name) {
		if occurrence.Start == target.Start || occurrence.End >= len(document.Text) || document.Text[occurrence.End] != '.' {
			continue
		}
		start, startErr := document.Position(occurrence.Start)
		end, endErr := document.Position(occurrence.End)
		if startErr == nil && endErr == nil {
			edits = append(edits, CompletionTextEdit{Range: Range{Start: start, End: end}, NewText: newName})
		}
	}
	sortRenameEdits(edits)
	return &WorkspaceEdit{Changes: map[string][]CompletionTextEdit{document.URI: edits}}, ""
}

func (s *Server) importAliasRenameConflict(document Document, target importAliasRenameTarget, newName string) string {
	for _, declaration := range s.symbols.Imports(document.URI) {
		alias := declaration.Name
		if alias == "" {
			alias, _, _ = s.inferredGoxImportAlias(document, declaration)
		}
		if alias == newName && !(target.Gox && declaration.Path == target.Path && declaration.Name == target.Name) {
			return "cannot rename alias \"" + target.Name + "\" to \"" + newName + "\": the new name conflicts with another import"
		}
	}
	for _, declaration := range s.symbols.GoImports(document.URI) {
		alias := declaration.Alias
		if alias == "" {
			alias = filepath.Base(declaration.Path)
		}
		if alias == newName && !(!target.Gox && declaration.Path == target.Path && declaration.Alias == target.Name) {
			return "cannot rename alias \"" + target.Name + "\" to \"" + newName + "\": the new name conflicts with another import"
		}
	}
	for _, symbol := range s.symbols.DocumentSymbols(document.URI) {
		if symbol.Symbol.Container == "" && symbol.Symbol.Name == newName {
			return "cannot rename alias \"" + target.Name + "\" to \"" + newName + "\": the new name conflicts with a module declaration"
		}
	}
	for _, symbol := range s.symbols.LocalSymbols(document.URI) {
		if symbol.Name == newName {
			return "cannot rename alias \"" + target.Name + "\" to \"" + newName + "\": the new name would be shadowed by a local declaration"
		}
	}
	return ""
}

func (s *Server) renameModuleDeclaration(document Document, target moduleRenameTarget, newName string) (*WorkspaceEdit, string) {
	if target.Name == newName {
		return &WorkspaceEdit{Changes: map[string][]CompletionTextEdit{}}, ""
	}
	changes := map[string][]CompletionTextEdit{document.URI: []CompletionTextEdit{{Range: target.Range, NewText: newName}}}
	targetPath := filepath.Clean(document.Path)
	for _, importer := range s.symbols.Documents() {
		for _, declaration := range s.symbols.Imports(importer.URI) {
			if declaration.Name != "" {
				continue
			}
			resolved, err := goxcompiler.ResolveGoxImport(s.projectRoot(importer.Path), importer.Path, declaration.Path)
			if err != nil || filepath.Clean(resolved) != targetPath {
				continue
			}
			if conflict := s.inferredModuleAliasConflict(importer, declaration, target.Name, newName); conflict != "" {
				return nil, conflict
			}
			for _, occurrence := range scanIdentifierOccurrences(importer.Text, target.Name) {
				if occurrence.End >= len(importer.Text) || importer.Text[occurrence.End] != '.' {
					continue
				}
				start, startErr := importer.Position(occurrence.Start)
				end, endErr := importer.Position(occurrence.End)
				if startErr == nil && endErr == nil {
					changes[importer.URI] = append(changes[importer.URI], CompletionTextEdit{Range: Range{Start: start, End: end}, NewText: newName})
				}
			}
		}
	}
	for uri := range changes {
		sortRenameEdits(changes[uri])
	}
	return &WorkspaceEdit{Changes: changes}, ""
}

func (s *Server) inferredModuleAliasConflict(document Document, own goxcompiler.GoxImport, oldName, newName string) string {
	for _, declaration := range s.symbols.Imports(document.URI) {
		if declaration.Path == own.Path && declaration.Source.Range == own.Source.Range {
			continue
		}
		alias := declaration.Name
		if alias == "" {
			alias, _, _ = s.inferredGoxImportAlias(document, declaration)
		}
		if alias == newName {
			return "cannot rename module \"" + oldName + "\" to \"" + newName + "\": inferred alias conflicts with another import in " + filepath.Base(document.Path)
		}
	}
	for _, declaration := range s.symbols.GoImports(document.URI) {
		alias := declaration.Alias
		if alias == "" {
			alias = filepath.Base(declaration.Path)
		}
		if alias == newName {
			return "cannot rename module \"" + oldName + "\" to \"" + newName + "\": inferred alias conflicts with another import in " + filepath.Base(document.Path)
		}
	}
	for _, symbol := range s.symbols.LocalSymbols(document.URI) {
		if symbol.Name == newName {
			return "cannot rename module \"" + oldName + "\" to \"" + newName + "\": inferred alias would be shadowed in " + filepath.Base(document.Path)
		}
	}
	return ""
}

func sortRenameEdits(edits []CompletionTextEdit) {
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].Range.Start.Line != edits[j].Range.Start.Line {
			return edits[i].Range.Start.Line > edits[j].Range.Start.Line
		}
		return edits[i].Range.Start.Character > edits[j].Range.Start.Character
	})
}
