package lsp

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"
)

type autoImportGoxModule struct {
	Alias      string
	ImportPath string
	URI        string
	Edit       CompletionTextEdit
	Symbols    []goxcompiler.SourceSymbol
}

func (s *Server) autoImportGoxModuleCompletions(document Document, context lexicalCompletionContext, offset int) []CompletionItem {
	modules := s.autoImportGoxModules(document, offset)
	items := make([]CompletionItem, 0, len(modules))
	for _, module := range modules {
		if !strings.HasPrefix(module.Alias, context.Prefix) {
			continue
		}
		items = append(items, CompletionItem{
			Label: module.Alias, Kind: 9, Detail: "Auto-import GOX module from " + module.ImportPath,
			LabelDetails: &CompletionItemLabelDetails{Description: module.ImportPath},
			InsertText:   module.Alias, SortText: "3_" + module.Alias + "_" + module.ImportPath,
			AdditionalTextEdits: []CompletionTextEdit{module.Edit},
		})
	}
	return items
}

func (s *Server) autoImportGoxMemberCompletions(document Document, context qualifiedCompletionContext, offset int) []CompletionItem {
	var items []CompletionItem
	for _, module := range s.autoImportGoxModules(document, offset) {
		if !moduleQualifierMatches(context.Qualifier, module.Alias) {
			continue
		}
		for _, symbol := range module.Symbols {
			if !symbol.Exported || symbol.Container != "" || !moduleMemberKind(symbol.Kind) ||
				ordinarySourceComponent(symbol) || !strings.HasPrefix(symbol.Name, context.Prefix) {
				continue
			}
			item := sourceSymbolCompletionItem(symbol, importedExportDetail(symbol)+" — auto-import "+module.ImportPath, symbol.Name, "3_"+symbol.Name+"_"+module.ImportPath)
			item.LabelDetails = &CompletionItemLabelDetails{Description: module.ImportPath}
			item.CandidateType = s.qualifyImportedType(module.URI, module.Alias, symbol.Type)
			item.CandidateUnderlying = s.qualifyImportedType(module.URI, module.Alias, symbol.Underlying)
			item.CandidateResults = qualifyCompletionTypes(s, module.URI, module.Alias, symbol.Results)
			item.AdditionalTextEdits = []CompletionTextEdit{module.Edit}
			items = append(items, item)
		}
	}
	return items
}

func (s *Server) autoImportGoxComponentCompletions(document Document, context templateSigilContext, offset int) []CompletionItem {
	var items []CompletionItem
	for _, module := range s.autoImportGoxModules(document, offset) {
		for _, symbol := range module.Symbols {
			if !symbol.Exported || symbol.Container != "" || !sourceComponentSymbol(symbol) {
				continue
			}
			qualified := module.Alias + "." + symbol.Name
			if !strings.HasPrefix(qualified, context.Prefix) && !strings.HasPrefix(symbol.Name, context.Prefix) {
				continue
			}
			item, ok := sigilTagCompletionItem(document, context, offset, "&"+qualified,
				symbol.Detail+" — auto-import "+module.ImportPath, componentTagSnippet(qualified, symbol.Parameters), qualified,
				"3_"+qualified+"_"+module.ImportPath)
			if !ok {
				continue
			}
			item.Kind = 3
			item.AdditionalTextEdits = []CompletionTextEdit{module.Edit}
			items = append(items, item)
		}
	}
	return items
}

func (s *Server) autoImportGoxModules(document Document, offset int) []autoImportGoxModule {
	imports, importRanges := s.completionGoxImportState(document)
	imported := make(map[string]bool)
	root := s.projectRoot(document.Path)
	for _, declaration := range imports {
		if resolved, err := goxcompiler.ResolveGoxImport(root, document.Path, declaration.Path); err == nil {
			imported[FileURI(resolved)] = true
		}
	}

	var modules []autoImportGoxModule
	for _, indexed := range s.symbols.Symbols() {
		symbol := indexed.Symbol
		if indexed.URI == document.URI || imported[indexed.URI] || symbol.Kind != goxcompiler.SymbolModule || symbol.Name == "" {
			continue
		}
		alias := implicitGoxModuleAlias(symbol.Name)
		if alias == "" || !s.autoImportAliasAvailable(document, imports, alias, offset) {
			continue
		}
		target, ok := s.symbols.Document(indexed.URI)
		if !ok {
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
		symbols := indexedSourceSymbols(s.symbols.DocumentSymbols(indexed.URI))
		if !autoImportModuleHasExports(symbols) {
			continue
		}
		modules = append(modules, autoImportGoxModule{Alias: alias, ImportPath: path, URI: indexed.URI, Edit: edit, Symbols: symbols})
	}
	sort.SliceStable(modules, func(left, right int) bool {
		if modules[left].Alias != modules[right].Alias {
			return modules[left].Alias < modules[right].Alias
		}
		return modules[left].ImportPath < modules[right].ImportPath
	})
	return modules
}

func (s *Server) completionGoxImportState(document Document) ([]goxcompiler.GoxImport, []goxmap.Range) {
	if module, err := s.analysisModule(document); err == nil {
		return module.GoxImports, goxImportSourceRanges(module.GoxImports, module.GoImports)
	}
	goxImports := s.symbols.Imports(document.URI)
	return goxImports, goxImportSourceRanges(goxImports, s.symbols.GoImports(document.URI))
}

func goxImportSourceRanges(goxImports []goxcompiler.GoxImport, goImports []goxcompiler.ImportDecl) []goxmap.Range {
	ranges := make([]goxmap.Range, 0, len(goxImports)+len(goImports))
	for _, declaration := range goxImports {
		if declaration.Source.URI != "" && declaration.Source.Range.Valid() {
			ranges = append(ranges, declaration.Source.Range)
		}
	}
	for _, declaration := range goImports {
		if declaration.Source.URI != "" && declaration.Source.Range.Valid() {
			ranges = append(ranges, declaration.Source.Range)
		}
	}
	return ranges
}

func (s *Server) autoImportAliasAvailable(document Document, imports []goxcompiler.GoxImport, alias string, offset int) bool {
	for _, local := range s.symbols.VisibleLocalSymbols(document.URI, offset) {
		if local.Name == alias {
			return false
		}
	}
	for _, indexed := range s.symbols.DocumentSymbols(document.URI) {
		symbol := indexed.Symbol
		if symbol.Container == "" && symbol.Kind != goxcompiler.SymbolModule && symbol.Name == alias {
			return false
		}
	}
	for _, declaration := range imports {
		name := declaration.Name
		if name == "" {
			name, _, _ = s.inferredGoxImportAlias(document, declaration)
		}
		if name == alias {
			return false
		}
	}
	root := s.projectRoot(document.Path)
	for _, declaration := range s.symbols.GoImports(document.URI) {
		name := declaration.Alias
		if name == "" {
			if packageInfo, err := s.goPackages.Load(root, declaration.Path); err == nil {
				name = packageInfo.Name
			}
		}
		if name == alias {
			return false
		}
	}
	return true
}

func implicitGoxModuleAlias(name string) string {
	runes := []rune(name)
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func autoImportModuleHasExports(symbols []goxcompiler.SourceSymbol) bool {
	for _, symbol := range symbols {
		if symbol.Exported && symbol.Container == "" && moduleMemberKind(symbol.Kind) {
			return true
		}
	}
	return false
}

func goxAutoImportEdit(document Document, imports []goxmap.Range, importPath, alias string, explicit bool) (CompletionTextEdit, bool) {
	specification := `"` + importPath + `"`
	if explicit {
		specification = alias + " " + specification
	}
	if len(imports) == 0 {
		moduleLine := strings.IndexByte(document.Text, '\n')
		if moduleLine < 0 {
			moduleLine = len(document.Text)
		}
		position, err := document.Position(moduleLine)
		if err != nil {
			return CompletionTextEdit{}, false
		}
		return CompletionTextEdit{Range: Range{Start: position, End: position}, NewText: "\n\nimport " + specification + "\n"}, true
	}

	last := imports[0]
	for _, declaration := range imports[1:] {
		if declaration.Start > last.Start {
			last = declaration
		}
	}
	keyword := strings.LastIndex(document.Text[:last.Start], "import")
	if keyword >= 0 {
		opening := strings.IndexByte(document.Text[keyword:last.Start], '(')
		closingBefore := strings.LastIndexByte(document.Text[keyword:last.Start], ')')
		if opening >= 0 && closingBefore < opening {
			closingRelative := strings.IndexByte(document.Text[last.End:], ')')
			if closingRelative >= 0 {
				closing := last.End + closingRelative
				insert := closing
				newText := "\n\t" + specification + "\n"
				lineStart := sourceLineStart(document.Text, closing)
				if strings.TrimSpace(document.Text[lineStart:closing]) == "" {
					insert = lineStart
					newText = "\t" + specification + "\n"
				}
				position, err := document.Position(insert)
				if err != nil {
					return CompletionTextEdit{}, false
				}
				return CompletionTextEdit{Range: Range{Start: position, End: position}, NewText: newText}, true
			}
		}
		lineStart := sourceLineStart(document.Text, keyword)
		lineEnd := sourceLineEnd(document.Text, last.End)
		existing := strings.TrimSpace(document.Text[keyword+len("import") : lineEnd])
		if existing != "" {
			start, startErr := document.Position(lineStart)
			end, endErr := document.Position(lineEnd)
			if startErr != nil || endErr != nil {
				return CompletionTextEdit{}, false
			}
			return CompletionTextEdit{
				Range:   Range{Start: start, End: end},
				NewText: "import (\n\t" + existing + "\n\t" + specification + "\n)",
			}, true
		}
	}
	insert := sourceLineEnd(document.Text, last.End)
	position, err := document.Position(insert)
	if err != nil {
		return CompletionTextEdit{}, false
	}
	return CompletionTextEdit{Range: Range{Start: position, End: position}, NewText: "\nimport " + specification}, true
}
