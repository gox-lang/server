package lsp

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type CompletionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type CompletionItem struct {
	Label               string                        `json:"label"`
	LabelDetails        *CompletionItemLabelDetails   `json:"labelDetails,omitempty"`
	Kind                int                           `json:"kind,omitempty"`
	Detail              string                        `json:"detail,omitempty"`
	InsertText          string                        `json:"insertText,omitempty"`
	InsertTextFormat    int                           `json:"insertTextFormat,omitempty"`
	FilterText          string                        `json:"filterText,omitempty"`
	SortText            string                        `json:"sortText,omitempty"`
	TextEdit            *CompletionTextEdit           `json:"textEdit,omitempty"`
	AdditionalTextEdits []CompletionTextEdit          `json:"additionalTextEdits,omitempty"`
	CandidateType       string                        `json:"-"`
	CandidateUnderlying string                        `json:"-"`
	CandidateAlias      bool                          `json:"-"`
	CandidateValue      string                        `json:"-"`
	CandidateResults    []string                      `json:"-"`
	CandidateParameters []goxcompiler.SourceParameter `json:"-"`
}

// CompletionItemLabelDetails uses the LSP label-details presentation so the
// completion list can show where an otherwise identical symbol comes from.
// Detail is appended to the label; Description is shown as its provenance.
type CompletionItemLabelDetails struct {
	Detail      string `json:"detail,omitempty"`
	Description string `json:"description,omitempty"`
}

type CompletionTextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type qualifiedCompletionContext struct {
	Qualifier string
	Prefix    string
}

type lexicalCompletionContext struct {
	Prefix string
}

func (s *Server) completions(document Document, position Position) CompletionList {
	if result, ok := s.fastClientAttributeCompletion(document, position); ok {
		return result
	}
	offset, err := document.Offset(position)
	if err != nil {
		return CompletionList{Items: []CompletionItem{}}
	}
	cursor := s.contextAt(document, offset)
	if cursor.passiveEmbedded() {
		return CompletionList{Items: []CompletionItem{}}
	}
	complete := func(items []CompletionItem) CompletionList {
		items = s.rankTemplateExpressionCompletions(document, offset, cursor, items)
		return completionList(applyCallableCompletionSnippets(document.Text, offset, items))
	}
	if items, handled := s.clientSourcePathCompletions(document, offset, cursor); handled {
		return completionList(items)
	}
	if items, handled := s.importPathCompletions(document, offset, cursor); handled {
		return incompleteCompletionList(items)
	}
	if items, handled := s.templateSigilCompletions(document, offset, cursor); handled {
		return complete(items)
	}
	if items, handled := s.templateAttributeCompletions(document, offset, cursor); handled {
		return complete(items)
	}
	if cursor.goCode() && !cursor.HasQualified {
		prefix := ""
		if cursor.HasLexical {
			prefix = cursor.Lexical.Prefix
		}
		if items, handled := s.structLiteralCompletions(document, offset, prefix); handled {
			return completionList(items)
		}
	}
	if cursor.goCode() && cursor.HasQualified {
		context := cursor.Qualified
		if items, handled := s.semanticCompletionItems(document, offset, context.Prefix); handled {
			return complete(ordinaryCompletionItems(items))
		}
		if items, handled := s.typedValueCompletions(document, context, offset); handled {
			return complete(items)
		}
		if items := s.importedGoxModuleCompletions(document, context); len(items) > 0 {
			return complete(items)
		}
		if items := s.importedGoPackageCompletions(document, context); len(items) > 0 {
			return complete(items)
		}
		if items := s.autoImportGoxMemberCompletions(document, context, offset); len(items) > 0 {
			return complete(items)
		}
		if items := s.autoImportGoPackageMemberCompletions(document, context, offset); len(items) > 0 {
			return complete(items)
		}
		return CompletionList{Items: []CompletionItem{}}
	}
	templatePrefix, templatePrefixStart := templateCompletionPrefixAt(document.Text, offset)
	templateItems, templateContext := s.templateNodeCompletions(document, templatePrefix, templatePrefixStart, offset)
	context, lexical := cursor.Lexical, cursor.goCode() && cursor.HasLexical
	if templateContext {
		if lexical {
			module, semanticOffset, _ := s.completionModule(document, offset)
			if completionGoContext(document.Text, offset, module, semanticOffset, context.Prefix) {
				templateItems = append(templateItems, s.lexicalCompletions(document, context, offset)...)
			}
		}
		return complete(templateItems)
	}
	if !lexical {
		return CompletionList{Items: []CompletionItem{}}
	}
	return complete(s.lexicalCompletions(document, context, offset))
}

func (s *Server) lexicalCompletions(document Document, context lexicalCompletionContext, offset int) []CompletionItem {
	module, semanticOffset, live := s.completionModule(document, offset)
	if !completionGoContext(document.Text, offset, module, semanticOffset, context.Prefix) {
		return nil
	}
	items := make(map[string]CompletionItem)
	semanticHandled := false
	if live {
		if semanticItems, handled := s.semanticVisibleCompletionItems(document, module, semanticOffset, context.Prefix); handled {
			semanticHandled = true
			for _, item := range semanticItems {
				items[item.Label] = item
			}
		}
	}

	// go/types owns lexical completion whenever the semantic snapshot can
	// identify the cursor scope. For malformed editor text with no semantic
	// position, the fallback below uses indexed source metadata only; Phase 13
	// deliberately removed recursive type inference from this path.
	if !semanticHandled {
		locals := s.symbols.VisibleLocalSymbols(document.URI, offset)
		symbols := indexedSourceSymbols(s.symbols.DocumentSymbols(document.URI))
		imports := s.symbols.Imports(document.URI)
		goImports := s.symbols.GoImports(document.URI)
		if live {
			locals = visibleLocalSymbols(module.LocalSymbols, semanticOffset)
			symbols = module.Symbols
			imports = module.GoxImports
			goImports = module.GoImports
		}

		componentLocals := make(map[string]bool)
		for _, symbol := range symbols {
			if symbol.Kind == goxcompiler.SymbolVariable && completionTypeIsComponent(symbol.Type) {
				componentLocals[symbol.Name] = true
			}
		}
		for _, local := range locals {
			if !strings.HasPrefix(local.Name, context.Prefix) {
				continue
			}
			if completionTypeIsComponent(local.Type) || componentLocals[local.Name] {
				continue
			}
			detail := local.Detail
			if detail == "" {
				detail = string(local.Kind) + " " + local.Name
			}
			items[local.Name] = CompletionItem{Label: local.Name, Kind: 6, Detail: detail, InsertText: local.Name, SortText: "0_" + local.Name, CandidateType: local.Type}
		}
		for _, symbol := range symbols {
			if symbol.Container != "" || !moduleMemberKind(symbol.Kind) || ordinarySourceComponent(symbol) || !strings.HasPrefix(symbol.Name, context.Prefix) {
				continue
			}
			if _, shadowed := items[symbol.Name]; shadowed {
				continue
			}
			items[symbol.Name] = sourceSymbolCompletionItem(symbol, symbol.Detail, symbol.Name, "1_"+symbol.Name)
		}
		for _, declaration := range imports {
			alias := declaration.Name
			if alias == "" {
				alias, _, _ = s.inferredGoxImportAlias(document, declaration)
			}
			appendImportCompletion(items, alias, context.Prefix, "GOX module from "+declaration.Path)
		}
		root := s.projectRoot(document.Path)
		for _, declaration := range goImports {
			alias := declaration.Alias
			if alias == "" {
				if packageName, err := s.goPackages.PackageName(root, declaration.Path); err == nil {
					alias = packageName
				}
			}
			appendImportCompletion(items, alias, context.Prefix, "Go package from "+declaration.Path)
		}
	}
	for _, item := range goBuiltinCompletionItems(context.Prefix) {
		if _, shadowed := items[item.Label]; shadowed {
			continue
		}
		items[item.Label] = item
	}
	result := make([]CompletionItem, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	result = append(result, goStatementSnippetCompletions(context.Prefix)...)
	// Preserve the pre-Phase-8 completion contract for Go's unary address
	// operator. ContextAt correctly classifies `&name` as Go outside template
	// markup, but an incomplete address expression must only complete symbols
	// already visible in the current lexical scope. Treating the prefix as an
	// ordinary lexical prefix here would fan out into global auto-imports (for
	// example runtime.Caller for `&Ca`), which changes Completion behavior and
	// conflates Phase 8 context ownership with the Phase 9 completion policy.
	if unaryAddressCompletionContext(document.Text, offset, context.Prefix) {
		return result
	}
	autoImports := append([]CompletionItem(nil), s.autoImportGoxModuleCompletions(document, context, offset)...)
	if context.Prefix != "" {
		goAutoImports := s.newGoAutoImportCompletionState(document, offset)
		autoImports = append(autoImports, s.goxSDKExportCompletions(document, context, goAutoImports, offset)...)
		autoImports = append(autoImports, s.autoImportGoPackageCompletions(document, context, goAutoImports)...)
		autoImports = append(autoImports, s.autoImportWorkspaceGoExportCompletions(document, context, goAutoImports)...)
		autoImports = append(autoImports, s.autoImportDependencyGoExportCompletions(document, context, goAutoImports)...)
	}
	for _, item := range autoImports {
		// A name already resolved in the current lexical scope is always the
		// user's intended symbol. Keep genuine auto-import alternatives with
		// the same label (for example bytes.Split and strings.Split), but never
		// place one above or beside a local/module/imported identifier.
		if _, occupied := items[item.Label]; occupied {
			continue
		}
		result = append(result, item)
	}
	return result
}

func sourceSymbolCompletionItem(symbol goxcompiler.SourceSymbol, detail, insertText, sortText string) CompletionItem {
	return CompletionItem{
		Label: symbol.Name, Kind: sourceSymbolCompletionKind(symbol.Kind), Detail: detail,
		InsertText: insertText, SortText: sortText, CandidateType: symbol.Type,
		CandidateUnderlying: symbol.Underlying, CandidateAlias: symbol.Alias, CandidateValue: symbol.Value,
		CandidateResults: symbol.Results, CandidateParameters: symbol.Parameters,
	}
}

func applyCallableCompletionSnippets(source string, offset int, items []CompletionItem) []CompletionItem {
	if completionFollowedByCall(source, offset) {
		return items
	}
	for index := range items {
		item := &items[index]
		if item.Kind != 2 && item.Kind != 3 || item.TextEdit != nil || completionTypeIsComponent(completionItemValueType(*item)) {
			continue
		}
		arguments := make([]string, 0, len(item.CandidateParameters))
		for parameterIndex, parameter := range item.CandidateParameters {
			placeholder := parameter.Name
			if placeholder == "" {
				placeholder = parameter.Type
			}
			if placeholder == "" {
				placeholder = "value"
			}
			arguments = append(arguments, "${"+completionInt(parameterIndex+1)+":"+escapeCompletionSnippetPlaceholder(placeholder)+"}")
		}
		insertText := item.InsertText
		if insertText == "" {
			insertText = item.Label
		}
		item.InsertText = insertText + "(" + strings.Join(arguments, ", ") + ")$0"
		item.InsertTextFormat = 2
	}
	return items
}

func completionFollowedByCall(source string, offset int) bool {
	if offset < 0 || offset > len(source) {
		return false
	}
	for offset < len(source) {
		value, size := utf8.DecodeRuneInString(source[offset:])
		if !unicode.IsSpace(value) {
			return value == '('
		}
		offset += size
	}
	return false
}

func escapeCompletionSnippetPlaceholder(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `$`, `\$`)
	return strings.ReplaceAll(value, `}`, `\}`)
}

func ordinarySourceComponent(symbol goxcompiler.SourceSymbol) bool {
	if symbol.Kind != goxcompiler.SymbolFunction && symbol.Kind != goxcompiler.SymbolMethod && symbol.Kind != goxcompiler.SymbolVariable && symbol.Kind != goxcompiler.SymbolField {
		return false
	}
	return completionTypeIsComponent(symbol.Type) || len(symbol.Results) == 1 && completionTypeIsComponent(symbol.Results[0])
}

func completionTypeIsComponent(typeName string) bool {
	return normalizeCompletionType(typeName) == "gox.Component"
}

func ordinaryCompletionItems(items []CompletionItem) []CompletionItem {
	result := items[:0]
	for _, item := range items {
		if completionTypeIsComponent(completionItemValueType(item)) {
			continue
		}
		result = append(result, item)
	}
	return result
}

func (s *Server) completionModule(document Document, offset int) (*goxcompiler.Module, int, bool) {
	repaired, repairedOffset := repairIncompleteSelectors(document.Text, offset)
	if repaired == document.Text {
		if module, err := s.analysisModule(document); err == nil && module != nil {
			return module, repairedOffset, true
		}
	}
	module, err := s.parseTransientModule(document, repaired)
	if err == nil {
		return module, repairedOffset, true
	}
	repaired = repaired[:repairedOffset] + "__gox_completion" + repaired[repairedOffset:]
	module, err = s.parseTransientModule(document, repaired)
	return module, repairedOffset, err == nil
}

func indexedSourceSymbols(indexed []IndexedSymbol) []goxcompiler.SourceSymbol {
	symbols := make([]goxcompiler.SourceSymbol, 0, len(indexed))
	for _, candidate := range indexed {
		symbols = append(symbols, candidate.Symbol)
	}
	return symbols
}

func appendImportCompletion(items map[string]CompletionItem, alias, prefix, detail string) {
	if alias == "" || alias == "_" || alias == "." || !strings.HasPrefix(alias, prefix) {
		return
	}
	if _, shadowed := items[alias]; shadowed {
		return
	}
	items[alias] = CompletionItem{Label: alias, Kind: 9, Detail: detail, InsertText: alias, SortText: "2_" + alias}
}

func rawLexicalCompletionContextAt(source string, offset int) (lexicalCompletionContext, bool) {
	if offset < 0 || offset > len(source) {
		return lexicalCompletionContext{}, false
	}
	start := offset
	for start > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:start])
		if !completionIdentifierRune(value) {
			break
		}
		start -= size
	}
	if start > 0 && source[start-1] == '.' {
		return lexicalCompletionContext{}, false
	}
	return lexicalCompletionContext{Prefix: source[start:offset]}, true
}

func completionGoContext(source string, offset int, module *goxcompiler.Module, semanticOffset int, prefix string) bool {
	return completionCodeContext(source, offset, module, semanticOffset) ||
		semanticIdentifierCompletionContext(module, semanticOffset, prefix) ||
		unaryAddressCompletionContext(source, offset, prefix)
}

// unaryAddressCompletionContext keeps an incomplete identifier after Go's
// address operator in lexical completion. GOX components use the distinct
// <& sigil, so that sequence must remain template/component context.
func unaryAddressCompletionContext(source string, offset int, prefix string) bool {
	start := offset - len(prefix)
	if prefix == "" || start <= 0 || start > len(source) || source[start-1] != '&' {
		return false
	}
	ampersand := start - 1
	return ampersand == 0 || source[ampersand-1] != '<'
}

func completionCodeContext(source string, offset int, module *goxcompiler.Module, semanticOffset int) bool {
	if module != nil {
		for _, template := range module.Templates {
			if semanticOffset < template.Start || semanticOffset > template.End {
				continue
			}
			return classifyCursorContext(source, offset, nil).Kind == cursorContextTemplateExpression
		}
	}
	return classifyCursorContext(source, offset, nil).goCode()
}

func rawInsideTemplateExpression(source string, offset int) bool {
	open := strings.LastIndex(source[:offset], "{{")
	close := strings.LastIndex(source[:offset], "}}")
	return open >= 0 && open > close
}

func rawGoLexicalContext(source string, offset int) bool {
	const (
		code = iota
		lineComment
		blockComment
		quotedString
		rawString
		runeString
	)
	state := code
	escaped := false
	for index := 0; index < offset; index++ {
		value := source[index]
		next := byte(0)
		if index+1 < offset {
			next = source[index+1]
		}
		switch state {
		case code:
			switch {
			case value == '/' && next == '/':
				state, index = lineComment, index+1
			case value == '/' && next == '*':
				state, index = blockComment, index+1
			case value == '"':
				state = quotedString
			case value == '`':
				state = rawString
			case value == '\'':
				state = runeString
			}
		case lineComment:
			if value == '\n' {
				state = code
			}
		case blockComment:
			if value == '*' && next == '/' {
				state, index = code, index+1
			}
		case rawString:
			if value == '`' {
				state = code
			}
		case quotedString, runeString:
			if escaped {
				escaped = false
			} else if value == '\\' {
				escaped = true
			} else if state == quotedString && value == '"' || state == runeString && value == '\'' {
				state = code
			}
		}
	}
	return state == code
}

func (s *Server) typedValueCompletions(document Document, context qualifiedCompletionContext, offset int) ([]CompletionItem, bool) {
	local, liveSymbols, ok := s.completionLocalSymbol(document, context, offset)
	if !ok {
		return nil, false
	}
	if len(liveSymbols) > 0 && !strings.Contains(completionBaseType(local.Type), ".") {
		return ordinaryCompletionItems(sourceTypeMemberCompletionItems(liveSymbols, completionBaseType(local.Type), context.Prefix, false)), true
	}
	members := s.typeMemberCompletions(document, local.Type, context.Prefix)
	return ordinaryCompletionItems(members), true
}

// completionLocalSymbol resolves against the live editor text when it differs
// from the last successfully indexed snapshot. Selectors ending in a dot are
// not valid Go syntax, so ParseModule cannot normally index the document while
// completion is being requested. Temporarily completing every dangling
// selector makes the snapshot parseable without changing the editor document
// or its diagnostics.
func (s *Server) completionLocalSymbol(document Document, context qualifiedCompletionContext, offset int) (goxcompiler.LocalSymbol, []goxcompiler.SourceSymbol, bool) {
	indexed, indexedOK := s.symbols.Document(document.URI)
	if !indexedOK || indexed.Text != document.Text {
		repaired, repairedOffset := repairIncompleteSelectors(document.Text, offset)
		if module, err := s.parseTransientModule(document, repaired); err == nil {
			if local, ok := localSymbolAt(module.LocalSymbols, context.Qualifier, repairedOffset); ok {
				return local, module.Symbols, true
			}
		}
	}
	local, ok := s.symbols.LocalSymbolAt(document.URI, context.Qualifier, offset)
	return local, nil, ok
}

func repairIncompleteSelectors(source string, offset int) (string, int) {
	const placeholder = "__gox_completion"
	var repaired strings.Builder
	repaired.Grow(len(source) + len(placeholder))
	repairedOffset := offset
	for index := 0; index < len(source); index++ {
		repaired.WriteByte(source[index])
		if source[index] != '.' || !selectorReceiverBeforeDot(source, index) || identifierAfterDot(source, index+1) {
			continue
		}
		repaired.WriteString(placeholder)
		if index+1 <= offset {
			repairedOffset += len(placeholder)
		}
	}
	return repaired.String(), repairedOffset
}

func selectorReceiverBeforeDot(source string, dot int) bool {
	end := skipSourceSpacesBackward(source, dot)
	if end == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(source[:end])
	return completionIdentifierRune(r) || r == ')' || r == ']' || r == '}'
}

func identifierAfterDot(source string, start int) bool {
	if start >= len(source) {
		return false
	}
	value, _ := utf8.DecodeRuneInString(source[start:])
	return completionIdentifierRune(value)
}

func localSymbolAt(symbols []goxcompiler.LocalSymbol, name string, offset int) (goxcompiler.LocalSymbol, bool) {
	var best goxcompiler.LocalSymbol
	found := false
	for _, symbol := range symbols {
		atDeclaration := symbol.Source.Range.Contains(offset) || offset == symbol.Source.Range.End
		inScope := symbol.Scope.Range.Contains(offset) && symbol.Source.Range.Start <= offset
		if symbol.Name != name || (!atDeclaration && !inScope) {
			continue
		}
		if !found || symbol.Scope.Range.Start > best.Scope.Range.Start ||
			(symbol.Scope.Range.Start == best.Scope.Range.Start && symbol.Source.Range.Start > best.Source.Range.Start) {
			best, found = symbol, true
		}
	}
	return best, found
}

func (s *Server) typeMemberCompletions(document Document, typeName, prefix string) []CompletionItem {
	base := completionBaseType(typeName)
	if base == "" {
		return nil
	}
	if qualifier, container, ok := importedNamedType(base); ok {
		if targetURI, found := s.importedModuleURI(document, qualifier); found {
			return s.sourceTypeMemberCompletions(targetURI, container, prefix, true)
		}
		if _, packageInfo, alias, _, found := s.resolveGoPackageImport(document, qualifier); found && alias == qualifier {
			return goPackageTypeMemberCompletions(packageInfo, container, prefix)
		}
		return nil
	}
	return s.sourceTypeMemberCompletions(document.URI, base, prefix, false)
}

func completionBaseType(typeName string) string {
	typeName = strings.TrimSpace(typeName)
	for strings.HasPrefix(typeName, "*") {
		typeName = strings.TrimSpace(strings.TrimPrefix(typeName, "*"))
	}
	return typeName
}

func (s *Server) sourceTypeMemberCompletions(uri, container, prefix string, exportedOnly bool) []CompletionItem {
	symbols := make([]goxcompiler.SourceSymbol, 0)
	for _, candidate := range s.symbols.DocumentSymbols(uri) {
		symbols = append(symbols, candidate.Symbol)
	}
	return sourceTypeMemberCompletionItems(symbols, container, prefix, exportedOnly)
}

func sourceTypeMemberCompletionItems(symbols []goxcompiler.SourceSymbol, container, prefix string, exportedOnly bool) []CompletionItem {
	items := make([]CompletionItem, 0)
	for _, symbol := range symbols {
		if symbol.Container != container || symbol.Kind != goxcompiler.SymbolField && symbol.Kind != goxcompiler.SymbolMethod ||
			exportedOnly && !symbol.Exported || !strings.HasPrefix(symbol.Name, prefix) {
			continue
		}
		items = append(items, CompletionItem{
			Label: symbol.Name, Kind: sourceSymbolCompletionKind(symbol.Kind), Detail: symbol.Detail,
			InsertText: symbol.Name, SortText: symbol.Name, CandidateType: symbol.Type,
			CandidateUnderlying: symbol.Underlying, CandidateAlias: symbol.Alias, CandidateValue: symbol.Value,
			CandidateResults: symbol.Results, CandidateParameters: symbol.Parameters,
		})
	}
	return items
}

func goPackageTypeMemberCompletions(packageInfo GoPackage, container, prefix string) []CompletionItem {
	for _, exported := range packageInfo.Exports {
		if exported.Name != container {
			continue
		}
		items := make([]CompletionItem, 0, len(exported.Members))
		for _, member := range exported.Members {
			if !strings.HasPrefix(member.Name, prefix) {
				continue
			}
			kind := 5
			if member.Kind == "method" {
				kind = 2
			}
			items = append(items, CompletionItem{
				Label: member.Name, Kind: kind, Detail: member.Detail,
				InsertText: member.Name, SortText: member.Name, CandidateType: member.Type,
				CandidateUnderlying: member.Underlying, CandidateAlias: member.Alias,
				CandidateResults: member.Results, CandidateParameters: member.Parameters,
			})
		}
		return items
	}
	return nil
}

func rawQualifiedCompletionContextAt(source string, offset int) (qualifiedCompletionContext, bool) {
	if offset < 0 || offset > len(source) {
		return qualifiedCompletionContext{}, false
	}
	prefixStart := offset
	for prefixStart > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:prefixStart])
		if !completionIdentifierRune(value) {
			break
		}
		prefixStart -= size
	}
	dot := skipSourceSpacesBackward(source, prefixStart)
	if dot == 0 || source[dot-1] != '.' {
		return qualifiedCompletionContext{}, false
	}
	qualifierEnd := skipSourceSpacesBackward(source, dot-1)
	qualifierStart := qualifierEnd
	for qualifierStart > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:qualifierStart])
		if !completionIdentifierRune(value) {
			break
		}
		qualifierStart -= size
	}
	qualifier := ""
	if qualifierStart < qualifierEnd {
		qualifier = source[qualifierStart:qualifierEnd]
	}
	return qualifiedCompletionContext{Qualifier: qualifier, Prefix: source[prefixStart:offset]}, true
}

func completionIdentifierRune(value rune) bool {
	return value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

func (s *Server) importedGoxModuleCompletions(document Document, context qualifiedCompletionContext) []CompletionItem {
	_, targetURI, ok := s.resolveImportedModule(document, context.Qualifier)
	if !ok {
		return nil
	}
	items := make([]CompletionItem, 0)
	for _, candidate := range s.symbols.DocumentSymbols(targetURI) {
		symbol := candidate.Symbol
		if !symbol.Exported || symbol.Container != "" || !moduleMemberKind(symbol.Kind) || ordinarySourceComponent(symbol) || !strings.HasPrefix(symbol.Name, context.Prefix) {
			continue
		}
		items = append(items, CompletionItem{
			Label: symbol.Name, Kind: sourceSymbolCompletionKind(symbol.Kind),
			Detail: importedExportDetail(symbol), InsertText: symbol.Name, SortText: symbol.Name,
			CandidateType:       s.qualifyImportedType(targetURI, context.Qualifier, symbol.Type),
			CandidateUnderlying: s.qualifyImportedType(targetURI, context.Qualifier, symbol.Underlying),
			CandidateAlias:      symbol.Alias, CandidateValue: symbol.Value,
			CandidateResults: qualifyCompletionTypes(s, targetURI, context.Qualifier, symbol.Results), CandidateParameters: symbol.Parameters,
		})
	}
	return items
}

func (s *Server) importedGoPackageCompletions(document Document, context qualifiedCompletionContext) []CompletionItem {
	_, packageInfo, alias, _, ok := s.resolveGoPackageImport(document, context.Qualifier)
	if !ok || alias != context.Qualifier {
		return nil
	}
	items := make([]CompletionItem, 0, len(packageInfo.Exports))
	for _, exported := range packageInfo.Exports {
		if !strings.HasPrefix(exported.Name, context.Prefix) {
			continue
		}
		detail := exported.Detail
		if isGoxSDKPackage(packageInfo.Path) {
			detail += " — GOX SDK public API"
		}
		item := CompletionItem{
			Label: exported.Name, Kind: goExportCompletionKind(exported.Kind),
			Detail: detail, InsertText: exported.Name, SortText: exported.Name,
			CandidateType:       qualifyGoPackageType(packageInfo, alias, exported.Type),
			CandidateUnderlying: qualifyGoPackageType(packageInfo, alias, exported.Underlying), CandidateAlias: exported.Alias,
			CandidateValue: exported.Value, CandidateResults: qualifyGoPackageTypes(packageInfo, alias, exported.Results), CandidateParameters: exported.Parameters,
		}
		if completionTypeIsComponent(completionItemValueType(item)) {
			continue
		}
		items = append(items, item)
	}
	return items
}

func qualifyGoPackageType(packageInfo GoPackage, qualifier, typeName string) string {
	for _, exported := range packageInfo.Exports {
		if exported.Kind == "type" || exported.Kind == "struct" || exported.Kind == "interface" {
			typeName = qualifyTypeIdentifier(typeName, exported.Name, qualifier+"."+exported.Name)
		}
	}
	return typeName
}

func qualifyGoPackageTypes(packageInfo GoPackage, qualifier string, values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = qualifyGoPackageType(packageInfo, qualifier, value)
	}
	return result
}

func qualifyCompletionTypes(s *Server, targetURI, qualifier string, values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = s.qualifyImportedType(targetURI, qualifier, value)
	}
	return result
}

func completionList(items []CompletionItem) CompletionList {
	items = uniqueCompletionItems(items)
	sort.SliceStable(items, func(i, j int) bool { return items[i].SortText < items[j].SortText })
	return CompletionList{IsIncomplete: false, Items: items}
}

func incompleteCompletionList(items []CompletionItem) CompletionList {
	list := completionList(items)
	list.IsIncomplete = true
	return list
}

func uniqueCompletionItems(items []CompletionItem) []CompletionItem {
	seen := make(map[string]bool, len(items))
	result := items[:0]
	for _, item := range items {
		key := item.Label + "\x00" + item.InsertText
		if item.TextEdit != nil {
			key += "\x00" + item.TextEdit.NewText
		}
		for _, edit := range item.AdditionalTextEdits {
			key += "\x00" + edit.NewText
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, item)
	}
	return result
}

func sourceSymbolCompletionKind(kind goxcompiler.SourceSymbolKind) int {
	switch kind {
	case goxcompiler.SymbolMethod:
		return 2
	case goxcompiler.SymbolFunction:
		return 3
	case goxcompiler.SymbolField:
		return 5
	case goxcompiler.SymbolStruct:
		return 22
	case goxcompiler.SymbolInterface:
		return 8
	case goxcompiler.SymbolType:
		return 7
	case goxcompiler.SymbolVariable:
		return 6
	case goxcompiler.SymbolConstant:
		return 21
	}
	return 18
}

func goExportCompletionKind(kind string) int {
	switch kind {
	case "func":
		return 3
	case "struct":
		return 22
	case "interface":
		return 8
	case "type":
		return 7
	case "var":
		return 6
	case "const":
		return 21
	}
	return 18
}
