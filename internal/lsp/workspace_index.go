package lsp

import (
	"sort"
	"sync"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"
)

type IndexedSymbol struct {
	URI    string
	Symbol goxcompiler.SourceSymbol
}

type indexedDocument struct {
	document        Document
	symbols         []IndexedSymbol
	references      []goxcompiler.SourceReference
	locals          []goxcompiler.LocalSymbol
	selectors       []goxcompiler.SelectorReference
	unresolved      []goxcompiler.UnresolvedIdentifier
	imports         []goxcompiler.GoxImport
	goImports       []goxcompiler.ImportDecl
	clientSemantics []goxcompiler.ClientSemantic
	templateRanges  []goxmap.Range
}

// WorkspaceIndex keeps compiler-owned declarations for the documents known by
// the language server. LSP features query this index instead of reparsing GOX.
type WorkspaceIndex struct {
	mu              sync.RWMutex
	documents       map[string][]IndexedSymbol
	sources         map[string]Document
	disk            map[string]indexedDocument
	open            map[string]bool
	references      map[string][]goxcompiler.SourceReference
	locals          map[string][]goxcompiler.LocalSymbol
	selectors       map[string][]goxcompiler.SelectorReference
	unresolved      map[string][]goxcompiler.UnresolvedIdentifier
	imports         map[string][]goxcompiler.GoxImport
	goImports       map[string][]goxcompiler.ImportDecl
	clientSemantics map[string][]goxcompiler.ClientSemantic
	templateRanges  map[string][]goxmap.Range
	byName          map[string][]IndexedSymbol
}

func NewWorkspaceIndex() *WorkspaceIndex {
	return &WorkspaceIndex{
		documents:       make(map[string][]IndexedSymbol),
		sources:         make(map[string]Document),
		disk:            make(map[string]indexedDocument),
		open:            make(map[string]bool),
		references:      make(map[string][]goxcompiler.SourceReference),
		locals:          make(map[string][]goxcompiler.LocalSymbol),
		selectors:       make(map[string][]goxcompiler.SelectorReference),
		unresolved:      make(map[string][]goxcompiler.UnresolvedIdentifier),
		imports:         make(map[string][]goxcompiler.GoxImport),
		goImports:       make(map[string][]goxcompiler.ImportDecl),
		clientSemantics: make(map[string][]goxcompiler.ClientSemantic),
		templateRanges:  make(map[string][]goxmap.Range),
		byName:          make(map[string][]IndexedSymbol),
	}
}

func (i *WorkspaceIndex) Update(document Document) error {
	return i.UpdateOpen(document)
}

func (i *WorkspaceIndex) UpdateOpen(document Document) error {
	module, err := goxcompiler.ParseModule(document.Path, document.Text)
	if err != nil {
		i.markOpen(document.URI)
		return err
	}
	return i.UpdateOpenModule(document, module)
}

// UpdateOpenModule updates the compatibility index from the canonical analysis
// already owned by AnalysisSnapshot. It prevents didOpen/didChange from
// reparsing the same document solely to refresh legacy consumers.
func (i *WorkspaceIndex) UpdateOpenModule(document Document, module *goxcompiler.Module) error {
	if module == nil {
		i.markOpen(document.URI)
		return nil
	}
	indexed := indexModule(document, module)
	i.mu.Lock()
	i.open[document.URI] = true
	i.removeEffectiveSymbolsLocked(document.URI)
	i.setEffectiveLocked(indexed)
	i.addEffectiveSymbolsLocked(indexed.symbols)
	i.mu.Unlock()
	return nil
}

func (i *WorkspaceIndex) markOpen(uri string) {
	i.mu.Lock()
	i.open[uri] = true
	i.mu.Unlock()
}

func (i *WorkspaceIndex) UpdateDisk(document Document) error {
	indexed, err := indexDocument(document)
	if err != nil {
		return err
	}
	i.mu.Lock()
	i.disk[document.URI] = indexed
	if !i.open[document.URI] {
		i.removeEffectiveSymbolsLocked(document.URI)
		i.setEffectiveLocked(indexed)
		i.addEffectiveSymbolsLocked(indexed.symbols)
	}
	i.mu.Unlock()
	return nil
}

func indexDocument(document Document) (indexedDocument, error) {
	module, err := goxcompiler.ParseModule(document.Path, document.Text)
	if err != nil {
		return indexedDocument{}, err
	}
	return indexModule(document, module), nil
}

func indexModule(document Document, module *goxcompiler.Module) indexedDocument {
	symbols := module.Symbols
	indexed := make([]IndexedSymbol, 0, len(symbols))
	for _, symbol := range symbols {
		indexed = append(indexed, IndexedSymbol{URI: document.URI, Symbol: symbol})
	}
	templateRanges := make([]goxmap.Range, 0, len(module.Templates))
	for _, template := range module.Templates {
		templateRanges = append(templateRanges, goxmap.Range{Start: template.Start, End: template.End})
	}
	return indexedDocument{
		document:        document,
		symbols:         indexed,
		references:      append([]goxcompiler.SourceReference(nil), module.References...),
		locals:          append([]goxcompiler.LocalSymbol(nil), module.LocalSymbols...),
		selectors:       append([]goxcompiler.SelectorReference(nil), module.SelectorReferences...),
		unresolved:      append([]goxcompiler.UnresolvedIdentifier(nil), module.UnresolvedIdentifiers...),
		imports:         append([]goxcompiler.GoxImport(nil), module.GoxImports...),
		goImports:       append([]goxcompiler.ImportDecl(nil), module.GoImports...),
		clientSemantics: append([]goxcompiler.ClientSemantic(nil), module.ClientSemantics...),
		templateRanges:  templateRanges,
	}
}

func (i *WorkspaceIndex) setEffectiveLocked(indexed indexedDocument) {
	i.documents[indexed.document.URI] = indexed.symbols
	i.sources[indexed.document.URI] = indexed.document
	i.references[indexed.document.URI] = indexed.references
	i.locals[indexed.document.URI] = indexed.locals
	i.selectors[indexed.document.URI] = indexed.selectors
	i.unresolved[indexed.document.URI] = indexed.unresolved
	i.imports[indexed.document.URI] = indexed.imports
	i.goImports[indexed.document.URI] = indexed.goImports
	i.clientSemantics[indexed.document.URI] = indexed.clientSemantics
	i.templateRanges[indexed.document.URI] = indexed.templateRanges
}

func (i *WorkspaceIndex) CloseOpen(uri string) {
	i.mu.Lock()
	delete(i.open, uri)
	i.removeEffectiveSymbolsLocked(uri)
	if indexed, ok := i.disk[uri]; ok {
		i.setEffectiveLocked(indexed)
		i.addEffectiveSymbolsLocked(indexed.symbols)
	} else {
		i.deleteEffectiveLocked(uri)
	}
	i.mu.Unlock()
}

func (i *WorkspaceIndex) Remove(uri string) {
	i.mu.Lock()
	i.removeEffectiveSymbolsLocked(uri)
	delete(i.open, uri)
	delete(i.disk, uri)
	i.deleteEffectiveLocked(uri)
	i.mu.Unlock()
}

func (i *WorkspaceIndex) RemoveDisk(uri string) {
	i.mu.Lock()
	delete(i.disk, uri)
	if !i.open[uri] {
		i.removeEffectiveSymbolsLocked(uri)
		i.deleteEffectiveLocked(uri)
	}
	i.mu.Unlock()
}

func (i *WorkspaceIndex) LocalSymbolAt(uri, name string, offset int) (goxcompiler.LocalSymbol, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	var best goxcompiler.LocalSymbol
	found := false
	for _, symbol := range i.locals[uri] {
		atDeclaration := symbol.Source.Range.Contains(offset) || offset == symbol.Source.Range.End
		inScope := symbol.Scope.Range.Contains(offset) && symbol.Source.Range.Start <= offset
		if symbol.Name != name || (!atDeclaration && !inScope) {
			continue
		}
		if !found || symbol.Scope.Range.Start > best.Scope.Range.Start || (symbol.Scope.Range.Start == best.Scope.Range.Start && symbol.Source.Range.Start > best.Source.Range.Start) {
			best, found = symbol, true
		}
	}
	return best, found
}

func (i *WorkspaceIndex) LocalDeclarationAt(uri string, offset int) (goxcompiler.LocalSymbol, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	for _, symbol := range i.locals[uri] {
		if symbol.Source.Range.Contains(offset) || offset == symbol.Source.Range.End {
			return symbol, true
		}
	}
	return goxcompiler.LocalSymbol{}, false
}

func (i *WorkspaceIndex) VisibleLocalSymbols(uri string, offset int) []goxcompiler.LocalSymbol {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return visibleLocalSymbols(i.locals[uri], offset)
}

func (i *WorkspaceIndex) LocalSymbols(uri string) []goxcompiler.LocalSymbol {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]goxcompiler.LocalSymbol(nil), i.locals[uri]...)
}

func visibleLocalSymbols(symbols []goxcompiler.LocalSymbol, offset int) []goxcompiler.LocalSymbol {
	visible := make(map[string]goxcompiler.LocalSymbol)
	for _, symbol := range symbols {
		if symbol.Name == "" || symbol.Name == "_" || symbol.Source.Range.Start > offset || !symbol.Scope.Range.Contains(offset) {
			continue
		}
		current, found := visible[symbol.Name]
		if !found || symbol.Scope.Range.Start > current.Scope.Range.Start ||
			(symbol.Scope.Range.Start == current.Scope.Range.Start && symbol.Source.Range.Start > current.Source.Range.Start) {
			visible[symbol.Name] = symbol
		}
	}
	result := make([]goxcompiler.LocalSymbol, 0, len(visible))
	for _, symbol := range visible {
		result = append(result, symbol)
	}
	return result
}

func (i *WorkspaceIndex) SelectorReferenceAt(uri string, offset int) (goxcompiler.SelectorReference, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	for _, reference := range i.selectors[uri] {
		if reference.Source.Range.Contains(offset) || offset == reference.Source.Range.End {
			return reference, true
		}
	}
	return goxcompiler.SelectorReference{}, false
}

func (i *WorkspaceIndex) UnresolvedIdentifiers(uri string) []goxcompiler.UnresolvedIdentifier {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]goxcompiler.UnresolvedIdentifier(nil), i.unresolved[uri]...)
}

func (i *WorkspaceIndex) Definitions(name string) []IndexedSymbol {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]IndexedSymbol(nil), i.byName[name]...)
}

func (i *WorkspaceIndex) DocumentSymbols(uri string) []IndexedSymbol {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]IndexedSymbol(nil), i.documents[uri]...)
}

func (i *WorkspaceIndex) Symbols() []IndexedSymbol {
	i.mu.RLock()
	defer i.mu.RUnlock()
	var symbols []IndexedSymbol
	for _, documentSymbols := range i.documents {
		symbols = append(symbols, documentSymbols...)
	}
	sort.SliceStable(symbols, func(left, right int) bool {
		if symbols[left].URI != symbols[right].URI {
			return symbols[left].URI < symbols[right].URI
		}
		return symbols[left].Symbol.Source.Range.Start < symbols[right].Symbol.Source.Range.Start
	})
	return symbols
}

func (i *WorkspaceIndex) Document(uri string) (Document, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	document, ok := i.sources[uri]
	return document, ok
}

func (i *WorkspaceIndex) Documents() []Document {
	i.mu.RLock()
	defer i.mu.RUnlock()
	documents := make([]Document, 0, len(i.sources))
	for _, document := range i.sources {
		documents = append(documents, document)
	}
	sort.SliceStable(documents, func(left, right int) bool { return documents[left].URI < documents[right].URI })
	return documents
}

func (i *WorkspaceIndex) TemplateRanges(uri string) []goxmap.Range {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]goxmap.Range(nil), i.templateRanges[uri]...)
}

func (i *WorkspaceIndex) ReferenceAt(uri string, offset int) (goxcompiler.SourceReference, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	for _, reference := range i.references[uri] {
		if reference.Source.Range.Contains(offset) || offset == reference.Source.Range.End {
			return reference, true
		}
	}
	return goxcompiler.SourceReference{}, false
}

func (i *WorkspaceIndex) Imports(uri string) []goxcompiler.GoxImport {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]goxcompiler.GoxImport(nil), i.imports[uri]...)
}

func (i *WorkspaceIndex) GoImports(uri string) []goxcompiler.ImportDecl {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]goxcompiler.ImportDecl(nil), i.goImports[uri]...)
}

func (i *WorkspaceIndex) ClientSemanticAt(uri string, offset int) (goxcompiler.ClientSemantic, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	for _, item := range i.clientSemantics[uri] {
		if item.Source.Range.Contains(offset) || offset == item.Source.Range.End {
			return item, true
		}
	}
	return goxcompiler.ClientSemantic{}, false
}

func (i *WorkspaceIndex) removeEffectiveSymbolsLocked(uri string) {
	for _, symbol := range i.documents[uri] {
		name := symbol.Symbol.Name
		definitions := i.byName[name]
		filtered := definitions[:0]
		for _, definition := range definitions {
			if definition.URI != uri {
				filtered = append(filtered, definition)
			}
		}
		if len(filtered) == 0 {
			delete(i.byName, name)
		} else {
			i.byName[name] = filtered
		}
	}
}

func (i *WorkspaceIndex) addEffectiveSymbolsLocked(symbols []IndexedSymbol) {
	touched := make(map[string]bool)
	for _, symbol := range symbols {
		name := symbol.Symbol.Name
		i.byName[name] = append(i.byName[name], symbol)
		touched[name] = true
	}
	for name := range touched {
		definitions := i.byName[name]
		sort.SliceStable(definitions, func(left, right int) bool {
			if definitions[left].URI != definitions[right].URI {
				return definitions[left].URI < definitions[right].URI
			}
			return definitions[left].Symbol.Source.Range.Start < definitions[right].Symbol.Source.Range.Start
		})
	}
}

func (i *WorkspaceIndex) deleteEffectiveLocked(uri string) {
	delete(i.documents, uri)
	delete(i.sources, uri)
	delete(i.references, uri)
	delete(i.locals, uri)
	delete(i.selectors, uri)
	delete(i.unresolved, uri)
	delete(i.imports, uri)
	delete(i.goImports, uri)
	delete(i.clientSemantics, uri)
	delete(i.templateRanges, uri)
}
