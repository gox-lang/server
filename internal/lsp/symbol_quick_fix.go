package lsp

import (
	"fmt"
	"path/filepath"
	"sort"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type goxSymbolQuickFixCandidate struct {
	alias      string
	importPath string
	symbol     string
	importEdit *CompletionTextEdit
}

func (s *Server) symbolQualificationCodeActions(document Document, diagnostic Diagnostic) []CodeAction {
	name, _, err := identifierAtPosition(document, diagnostic.Range.Start)
	if err != nil || name == "" {
		return nil
	}
	offset, err := document.Offset(diagnostic.Range.Start)
	if err != nil {
		return nil
	}

	if occurrence, ok := selectorOccurrenceAtQualifier(document.Text, offset, name); ok {
		return s.wrongQualifierCodeActions(document, diagnostic, occurrence)
	}
	return s.unqualifiedSymbolCodeActions(document, diagnostic, name, offset)
}

func (s *Server) selectorQualificationCodeActions(document Document, diagnostic Diagnostic) []CodeAction {
	offset, err := document.Offset(diagnostic.Range.Start)
	if err != nil {
		return nil
	}
	for _, occurrence := range selectorOccurrences(document.Text) {
		if offset < occurrence.memberStart || offset > occurrence.memberEnd {
			continue
		}
		start, startErr := document.Position(occurrence.qualifierStart)
		end, endErr := document.Position(occurrence.qualifierEnd)
		if startErr != nil || endErr != nil {
			return nil
		}
		return s.importedMemberQualifierCodeActions(document, diagnostic, occurrence, Range{Start: start, End: end})
	}
	return nil
}

func (s *Server) componentQualificationCodeActions(document Document, diagnostic Diagnostic) []CodeAction {
	name, _, err := identifierAtPosition(document, diagnostic.Range.Start)
	if err != nil || name == "" {
		return nil
	}
	offset, err := document.Offset(diagnostic.Range.Start)
	if err != nil {
		return nil
	}
	imports, importRanges := s.completionGoxImportState(document)
	usedAliases := s.quickFixUsedAliases(document, imports, offset)
	root := s.projectRoot(document.Path)
	imported := make(map[string]bool)
	for _, declaration := range imports {
		if resolved, resolveErr := goxcompiler.ResolveGoxImport(root, document.Path, declaration.Path); resolveErr == nil {
			imported[FileURI(resolved)] = true
		}
	}
	var candidates []goxSymbolQuickFixCandidate
	seenModules := make(map[string]bool)
	for _, indexed := range s.symbols.Symbols() {
		if indexed.URI == document.URI || imported[indexed.URI] || seenModules[indexed.URI] {
			continue
		}
		symbol := indexed.Symbol
		if symbol.Name != name || !symbol.Exported || symbol.Container != "" || !sourceComponentSymbol(symbol) {
			continue
		}
		target, ok := s.symbols.Document(indexed.URI)
		if !ok {
			continue
		}
		alias := uniqueQuickFixAlias(implicitGoxModuleAlias(indexedModuleName(s.symbols.DocumentSymbols(indexed.URI))), usedAliases)
		path, pathErr := filepath.Rel(filepath.Dir(document.Path), target.Path)
		if alias == "" || pathErr != nil || path == "." {
			continue
		}
		path = filepath.ToSlash(path)
		edit, ok := goxAutoImportEdit(document, importRanges, path, alias, true)
		if !ok {
			continue
		}
		seenModules[indexed.URI] = true
		usedAliases[alias] = true
		candidates = append(candidates, goxSymbolQuickFixCandidate{alias: alias, importPath: path, symbol: name, importEdit: &edit})
	}
	return symbolQuickFixActions(document, diagnostic, candidates, func(candidate goxSymbolQuickFixCandidate) (string, []CompletionTextEdit) {
		edits := []CompletionTextEdit{{Range: diagnostic.Range, NewText: candidate.alias + "." + candidate.symbol}, *candidate.importEdit}
		return fmt.Sprintf("Qualify component %s as %s.%s and import %s", name, candidate.alias, name, candidate.importPath), edits
	})
}

func (s *Server) wrongQualifierCodeActions(document Document, diagnostic Diagnostic, occurrence selectorOccurrence) []CodeAction {
	return s.importedMemberQualifierCodeActions(document, diagnostic, occurrence, diagnostic.Range)
}

func (s *Server) importedMemberQualifierCodeActions(document Document, diagnostic Diagnostic, occurrence selectorOccurrence, replacement Range) []CodeAction {
	var candidates []goxSymbolQuickFixCandidate
	for _, declaration := range s.symbols.Imports(document.URI) {
		alias := declaration.Name
		if alias == "" {
			alias, _, _ = s.inferredGoxImportAlias(document, declaration)
		}
		if alias == "" || alias == occurrence.qualifier {
			continue
		}
		_, targetURI, ok := s.resolveImportedModule(document, alias)
		if !ok || !exportedModuleSymbolExists(s.symbols.DocumentSymbols(targetURI), occurrence.member) {
			continue
		}
		candidates = append(candidates, goxSymbolQuickFixCandidate{alias: alias, symbol: occurrence.member})
	}
	root := s.projectRoot(document.Path)
	for _, declaration := range s.symbols.GoImports(document.URI) {
		packageInfo, err := s.goPackages.Load(root, declaration.Path)
		if err != nil {
			continue
		}
		alias := declaration.Alias
		if alias == "" {
			alias = packageInfo.Name
		}
		if alias == "" || alias == occurrence.qualifier || !goPackageExportExists(packageInfo, occurrence.member) {
			continue
		}
		candidates = append(candidates, goxSymbolQuickFixCandidate{alias: alias, importPath: declaration.Path, symbol: occurrence.member})
	}
	return symbolQuickFixActions(document, diagnostic, candidates, func(candidate goxSymbolQuickFixCandidate) (string, []CompletionTextEdit) {
		return fmt.Sprintf("Change %s to %s", occurrence.qualifier, candidate.alias), []CompletionTextEdit{{Range: replacement, NewText: candidate.alias}}
	})
}

func goPackageExportExists(packageInfo GoPackage, name string) bool {
	for _, exported := range packageInfo.Exports {
		if exported.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) unqualifiedSymbolCodeActions(document Document, diagnostic Diagnostic, name string, offset int) []CodeAction {
	imports, importRanges := s.completionGoxImportState(document)
	imported := make(map[string]bool)
	root := s.projectRoot(document.Path)
	for _, declaration := range imports {
		if resolved, err := goxcompiler.ResolveGoxImport(root, document.Path, declaration.Path); err == nil {
			imported[FileURI(resolved)] = true
		}
	}

	usedAliases := s.quickFixUsedAliases(document, imports, offset)
	var candidates []goxSymbolQuickFixCandidate
	seenModules := make(map[string]bool)
	for _, indexed := range s.symbols.Symbols() {
		if indexed.URI == document.URI || imported[indexed.URI] || seenModules[indexed.URI] {
			continue
		}
		if indexed.Symbol.Name != name || !indexed.Symbol.Exported || indexed.Symbol.Container != "" || !moduleMemberKind(indexed.Symbol.Kind) || sourceComponentSymbol(indexed.Symbol) {
			continue
		}
		target, ok := s.symbols.Document(indexed.URI)
		if !ok {
			continue
		}
		moduleName := indexedModuleName(s.symbols.DocumentSymbols(indexed.URI))
		alias := uniqueQuickFixAlias(implicitGoxModuleAlias(moduleName), usedAliases)
		if alias == "" {
			continue
		}
		path, err := filepath.Rel(filepath.Dir(document.Path), target.Path)
		if err != nil || path == "." {
			continue
		}
		path = filepath.ToSlash(path)
		edit, ok := goxAutoImportEdit(document, importRanges, path, alias, true)
		if !ok {
			continue
		}
		seenModules[indexed.URI] = true
		usedAliases[alias] = true
		candidates = append(candidates, goxSymbolQuickFixCandidate{alias: alias, importPath: path, symbol: name, importEdit: &edit})
	}
	return symbolQuickFixActions(document, diagnostic, candidates, func(candidate goxSymbolQuickFixCandidate) (string, []CompletionTextEdit) {
		edits := []CompletionTextEdit{{Range: diagnostic.Range, NewText: candidate.alias + "." + candidate.symbol}}
		if candidate.importEdit != nil {
			edits = append(edits, *candidate.importEdit)
		}
		return fmt.Sprintf("Qualify %s as %s.%s and import %s", name, candidate.alias, name, candidate.importPath), edits
	})
}

func symbolQuickFixActions(document Document, diagnostic Diagnostic, candidates []goxSymbolQuickFixCandidate, build func(goxSymbolQuickFixCandidate) (string, []CompletionTextEdit)) []CodeAction {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].alias != candidates[j].alias {
			return candidates[i].alias < candidates[j].alias
		}
		return candidates[i].importPath < candidates[j].importPath
	})
	actions := make([]CodeAction, 0, len(candidates))
	for _, candidate := range candidates {
		title, edits := build(candidate)
		actions = append(actions, CodeAction{Title: title, Kind: "quickfix", Diagnostics: []Diagnostic{diagnostic}, Edit: &WorkspaceEdit{Changes: map[string][]CompletionTextEdit{document.URI: edits}}, IsPreferred: len(candidates) == 1})
	}
	return actions
}

func (s *Server) quickFixUsedAliases(document Document, imports []goxcompiler.GoxImport, offset int) map[string]bool {
	used := make(map[string]bool)
	for _, local := range s.symbols.VisibleLocalSymbols(document.URI, offset) {
		used[local.Name] = true
	}
	for _, indexed := range s.symbols.DocumentSymbols(document.URI) {
		if indexed.Symbol.Container == "" {
			used[indexed.Symbol.Name] = true
		}
	}
	for _, declaration := range imports {
		alias := declaration.Name
		if alias == "" {
			alias, _, _ = s.inferredGoxImportAlias(document, declaration)
		}
		used[alias] = true
	}
	for _, declaration := range s.symbols.GoImports(document.URI) {
		alias := declaration.Alias
		if alias == "" {
			alias = pathImportDiagnosticAlias(declaration.Path)
		}
		used[alias] = true
	}
	return used
}

func uniqueQuickFixAlias(base string, used map[string]bool) string {
	if base == "" {
		return ""
	}
	if !used[base] {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s%d", base, suffix)
		if !used[candidate] {
			return candidate
		}
	}
}

func indexedModuleName(symbols []IndexedSymbol) string {
	for _, indexed := range symbols {
		if indexed.Symbol.Kind == goxcompiler.SymbolModule {
			return indexed.Symbol.Name
		}
	}
	return ""
}

func exportedModuleSymbolExists(symbols []IndexedSymbol, name string) bool {
	for _, indexed := range symbols {
		symbol := indexed.Symbol
		if symbol.Name == name && symbol.Exported && symbol.Container == "" && moduleMemberKind(symbol.Kind) {
			return true
		}
	}
	return false
}

func selectorOccurrenceAtQualifier(source string, offset int, name string) (selectorOccurrence, bool) {
	for _, occurrence := range selectorOccurrences(source) {
		if occurrence.qualifier == name && offset >= occurrence.qualifierStart && offset <= occurrence.qualifierEnd {
			return occurrence, true
		}
	}
	return selectorOccurrence{}, false
}
