package lsp

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type templateSigilContext struct {
	Sigil        byte
	Prefix       string
	ReplaceStart int
	ReplaceEnd   int
}

func (s *Server) templateSigilCompletions(document Document, offset int, cursor cursorContext) ([]CompletionItem, bool) {
	if cursor.Kind != cursorContextTemplateSigil || !cursor.HasSigil {
		return nil, false
	}
	context := cursor.Sigil
	switch context.Sigil {
	case ':':
		return directiveSigilCompletions(document, context, offset), true
	case '&':
		return s.componentSigilCompletions(document, context, offset), true
	case '@':
		return s.clientSigilCompletions(document, context, offset), true
	}
	return nil, false
}

func rawTemplateSigilContextAt(source string, offset int) (templateSigilContext, bool) {
	if offset < 0 || offset > len(source) {
		return templateSigilContext{}, false
	}
	start := offset
	for start > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:start])
		if value != '.' && value != '-' && value != '_' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
			break
		}
		start -= size
	}
	if start == 0 || !strings.ContainsRune(":&@", rune(source[start-1])) {
		return templateSigilContext{}, false
	}
	sigilOffset := start - 1
	replaceStart := sigilOffset
	if replaceStart > 0 && source[replaceStart-1] == '<' {
		replaceStart--
	}
	replaceEnd := offset
	for replaceEnd < len(source) {
		value, size := utf8.DecodeRuneInString(source[replaceEnd:])
		if value != '.' && value != '-' && value != '_' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
			break
		}
		replaceEnd += size
	}
	return templateSigilContext{
		Sigil: source[sigilOffset], Prefix: source[start:offset], ReplaceStart: replaceStart, ReplaceEnd: replaceEnd,
	}, true
}

func rawTemplateSigilValueContext(source string, context templateSigilContext, offset int) bool {
	if rawInsideTemplateExpression(source, offset) {
		return false
	}
	hasOpeningBracket := context.ReplaceStart < len(source) && source[context.ReplaceStart] == '<'
	if hasOpeningBracket && templateTagOpenAt(source, offset) {
		return true
	}
	if !hasOpeningBracket && templateTagOpenAt(source, context.ReplaceStart) {
		return false
	}
	return insideOpenTemplateMarkup(source, context.ReplaceStart) || expectedTemplateValue(source, context.ReplaceStart)
}

func directiveSigilCompletions(document Document, context templateSigilContext, offset int) []CompletionItem {
	snippets := directiveSnippetsForContext(document.Text, context.ReplaceStart)
	items := make([]CompletionItem, 0, len(snippets))
	for _, snippet := range snippets {
		if !strings.HasPrefix(snippet.label, context.Prefix) {
			continue
		}
		if item, ok := sigilSnippetItem(document, context, offset, ":"+snippet.label, snippet.detail, snippet.text, "0_"+snippet.label); ok {
			items = append(items, item)
		}
	}
	return items
}

func directiveSnippetsForContext(source string, offset int) []templateSnippet {
	ifAssign := templateSnippet{label: "if-assign", detail: "GOX if with assignment", text: "<:if ${1} :assign={{ ${2} }} condition={{ ${3} }}>\n\t$0\n</:if>"}
	base := []templateSnippet{
		ifAssign,
		{label: "if", detail: "GOX conditional directive", text: "<:if condition={{ ${1} }}>\n\t${2}\n</:if>"},
		{label: "if-group", detail: "GOX conditional group", text: "<:if-group>\n\t<:if condition={{ ${1} }}>\n\t\t${2}\n\t</:if>\n\t<:else>\n\t\t${3}\n\t</:else>\n</:if-group>"},
		{label: "for", detail: "GOX range directive", text: "<:for ${1:index} ${2:item} range={{ ${3:items} }}>\n\t${4}\n</:for>"},
		{label: "repeat", detail: "GOX repeat directive", text: "<:repeat ${1:index} times={{ ${2} }}>\n\t${3}\n</:repeat>"},
		{label: "switch", detail: "GOX switch directive", text: "<:switch value={{ ${1} }}>\n\t<:case is={{ ${2} }}>\n\t\t${3}\n\t</:case>\n\t<:default>\n\t\t${4}\n\t</:default>\n</:switch>"},
	}
	stack := openTemplateTags(source, offset)
	if len(stack) > 0 {
		switch stack[len(stack)-1] {
		case ":switch":
			return []templateSnippet{
				{label: "case", detail: "GOX switch case", text: "<:case is={{ ${1} }}>\n\t${2}\n</:case>"},
				{label: "default", detail: "GOX switch default", text: "<:default>\n\t${1}\n</:default>"},
			}
		case ":if-group":
			return []templateSnippet{
				ifAssign,
				{label: "else-if-assign", detail: "GOX else-if with assignment", text: "<:else-if ${1} :assign={{ ${2} }} condition={{ ${3} }}>\n\t$0\n</:else-if>"},
				{label: "if", detail: "GOX initial conditional branch", text: "<:if condition={{ ${1} }}>\n\t${2}\n</:if>"},
				{label: "else-if", detail: "GOX conditional branch", text: "<:else-if condition={{ ${1} }}>\n\t${2}\n</:else-if>"},
				{label: "else", detail: "GOX fallback branch", text: "<:else>\n\t${1}\n</:else>"},
			}
		case ":for":
			base = append(base,
				templateSnippet{label: "empty", detail: "GOX empty range block", text: "<:empty>\n\t${1}\n</:empty>"},
				templateSnippet{label: "each", detail: "GOX non-empty range block", text: "<:each>\n\t${1}\n</:each>"},
			)
		}
	}
	if tagStackContains(stack, ":for") || tagStackContains(stack, ":repeat") {
		base = append(base,
			templateSnippet{label: "break", detail: "GOX loop break directive", text: "<:break />"},
			templateSnippet{label: "continue", detail: "GOX loop continue directive", text: "<:continue />"},
		)
	}
	return base
}

func openTemplateTags(source string, offset int) []string {
	var stack []string
	for index := 0; index < offset; index++ {
		if source[index] != '<' {
			continue
		}
		end := templateTagEnd(source, index+1, offset)
		if end < 0 {
			break
		}
		body := strings.TrimSpace(source[index+1 : end])
		if body == "" {
			stack = append(stack, "fragment")
			index = end
			continue
		}
		if body == "/" {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			index = end
			continue
		}
		closing := strings.HasPrefix(body, "/")
		body = strings.TrimPrefix(body, "/")
		prefix := ""
		if len(body) > 0 && strings.ContainsRune(":&@", rune(body[0])) {
			prefix, body = body[:1], body[1:]
		}
		name := templateMarkupTagName(body)
		if name == "" {
			index = end
			continue
		}
		tag := prefix + name
		if closing {
			for position := len(stack) - 1; position >= 0; position-- {
				if stack[position] == tag {
					stack = stack[:position]
					break
				}
			}
		} else if !strings.HasSuffix(body, "/") && !htmlVoidElements[name] {
			stack = append(stack, tag)
		}
		index = end
	}
	return stack
}

func templateTagEnd(source string, start, limit int) int {
	quote := byte(0)
	expressionDepth := 0
	for index := start; index < limit; index++ {
		if quote != 0 {
			if source[index] == '\\' {
				index++
			} else if source[index] == quote {
				quote = 0
			}
			continue
		}
		if source[index] == '\'' || source[index] == '"' {
			quote = source[index]
			continue
		}
		if index+1 < limit && source[index:index+2] == "{{" {
			expressionDepth++
			index++
			continue
		}
		if expressionDepth > 0 && index+1 < limit && source[index:index+2] == "}}" {
			expressionDepth--
			index++
			continue
		}
		if source[index] == '>' && expressionDepth == 0 {
			return index
		}
	}
	return -1
}

func tagStackContains(stack []string, target string) bool {
	for _, tag := range stack {
		if tag == target {
			return true
		}
	}
	return false
}

func (s *Server) componentSigilCompletions(document Document, context templateSigilContext, offset int) []CompletionItem {
	module, semanticOffset, live := s.completionModuleForSigil(document, context, offset)
	locals := s.symbols.VisibleLocalSymbols(document.URI, offset)
	symbols := indexedSourceSymbols(s.symbols.DocumentSymbols(document.URI))
	imports := s.symbols.Imports(document.URI)
	if live {
		locals = visibleLocalSymbols(module.LocalSymbols, semanticOffset)
		symbols = module.Symbols
		imports = module.GoxImports
	}
	componentLocals := make(map[string]bool)
	for _, symbol := range symbols {
		if symbol.Kind == goxcompiler.SymbolVariable && symbol.Type == "gox.Component" {
			componentLocals[symbol.Name] = true
		}
	}
	candidates := make(map[string]goxcompiler.SourceSymbol)
	for _, local := range locals {
		if local.Type == "gox.Component" || componentLocals[local.Name] {
			candidates[local.Name] = goxcompiler.SourceSymbol{Name: local.Name, Kind: goxcompiler.SymbolVariable, Type: "gox.Component", Detail: local.Detail}
		}
	}
	for _, symbol := range symbols {
		if symbol.Container == "" && sourceComponentSymbol(symbol) {
			if _, shadowed := candidates[symbol.Name]; !shadowed {
				candidates[symbol.Name] = symbol
			}
		}
	}
	for _, declaration := range imports {
		alias := declaration.Name
		if alias == "" {
			alias, _, _ = s.inferredGoxImportAlias(document, declaration)
		}
		if alias == "" || alias == "_" || alias == "." {
			continue
		}
		resolved, err := goxcompiler.ResolveGoxImport(s.projectRoot(document.Path), document.Path, declaration.Path)
		if err != nil {
			continue
		}
		targetURI := FileURI(resolved)
		for _, indexed := range s.symbols.DocumentSymbols(targetURI) {
			if indexed.Symbol.Exported && indexed.Symbol.Container == "" && sourceComponentSymbol(indexed.Symbol) {
				candidates[alias+"."+indexed.Symbol.Name] = indexed.Symbol
			}
		}
	}
	labels := make([]string, 0, len(candidates))
	for label := range candidates {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	items := make([]CompletionItem, 0, len(labels))
	for _, label := range labels {
		if !strings.HasPrefix(label, context.Prefix) {
			continue
		}
		symbol := candidates[label]
		snippet := componentTagSnippet(label, symbol.Parameters)
		item, ok := sigilTagCompletionItem(document, context, offset, "&"+label, symbol.Detail, snippet, label, "0_"+label)
		if ok {
			item.Kind = 3
			items = append(items, item)
		}
	}
	items = append(items, s.autoImportGoxComponentCompletions(document, context, offset)...)
	return preferImportedComponentCompletions(items)
}

func preferImportedComponentCompletions(items []CompletionItem) []CompletionItem {
	preferred := make(map[string]int, len(items))
	result := make([]CompletionItem, 0, len(items))
	for _, item := range items {
		index, exists := preferred[item.Label]
		if !exists {
			preferred[item.Label] = len(result)
			result = append(result, item)
			continue
		}
		if len(result[index].AdditionalTextEdits) > 0 && len(item.AdditionalTextEdits) == 0 {
			result[index] = item
		}
	}
	return result
}

func sourceComponentSymbol(symbol goxcompiler.SourceSymbol) bool {
	if symbol.Kind == goxcompiler.SymbolVariable {
		return symbol.Type == "gox.Component"
	}
	return symbol.Kind == goxcompiler.SymbolFunction && len(symbol.Results) == 1 && symbol.Results[0] == "gox.Component"
}

func componentTagSnippet(name string, parameters []goxcompiler.SourceParameter) string {
	var attributes []string
	hasChildren := false
	placeholder := 1
	for _, parameter := range parameters {
		if parameter.Name == "" || parameter.Name == "_" {
			continue
		}
		if parameter.Name == "children" && parameter.Type == "gox.Component" {
			hasChildren = true
			continue
		}
		attributes = append(attributes, parameter.Name+"={{ ${"+completionInt(placeholder)+"} }}")
		placeholder++
	}
	open := "<&" + name
	if len(attributes) > 0 {
		open += " " + strings.Join(attributes, " ")
	}
	if hasChildren {
		return open + ">${" + completionInt(placeholder) + "}</&" + name + ">"
	}
	return open + " />"
}

func (s *Server) clientSigilCompletions(document Document, context templateSigilContext, offset int) []CompletionItem {
	module, _, live := s.completionModuleForSigil(document, context, offset)
	symbols := indexedSourceSymbols(s.symbols.DocumentSymbols(document.URI))
	if live {
		symbols = module.Symbols
	}
	var items []CompletionItem
	for _, symbol := range symbols {
		if symbol.Kind != goxcompiler.SymbolClient || !strings.HasPrefix(symbol.Name, context.Prefix) {
			continue
		}
		snippet := "<@" + symbol.Name + " hydrate=\"${1|load,idle,visible,hover,interaction|}\" />"
		item, ok := sigilTagCompletionItem(document, context, offset, "@"+symbol.Name, symbol.Detail, snippet, symbol.Name, "0_"+symbol.Name)
		if ok {
			item.Kind = 7
			items = append(items, item)
		}
	}
	return items
}

func sigilTagCompletionItem(document Document, context templateSigilContext, offset int, label, detail, snippet, name, sortText string) (CompletionItem, bool) {
	if context.ReplaceEnd < offset {
		context.ReplaceEnd = offset
	}
	if templateSigilExistingTag(document.Text, context) {
		start, startErr := document.Position(context.ReplaceStart)
		end, endErr := document.Position(context.ReplaceEnd)
		if startErr != nil || endErr != nil {
			return CompletionItem{}, false
		}
		return CompletionItem{
			Label: label, Kind: 15, Detail: detail, FilterText: sigilCompletionFilterText(document.Text, context, label), SortText: sortText,
			TextEdit: &CompletionTextEdit{
				Range: Range{Start: start, End: end}, NewText: "<" + string(context.Sigil) + name,
			},
		}, true
	}
	return sigilSnippetItem(document, context, offset, label, detail, snippet, sortText)
}

func sigilCompletionFilterText(source string, context templateSigilContext, label string) string {
	if context.ReplaceStart >= 0 && context.ReplaceStart < len(source) && source[context.ReplaceStart] == '<' {
		return "<" + label
	}
	return label
}

func templateSigilExistingTag(source string, context templateSigilContext) bool {
	end := skipSourceSpacesForward(source, context.ReplaceEnd)
	if end >= len(source) {
		return false
	}
	value, _ := utf8.DecodeRuneInString(source[end:])
	if value == '/' {
		return end+1 < len(source) && source[end+1] == '>'
	}
	return value == '>' || value == ':' || value == '_' || unicode.IsLetter(value)
}

func (s *Server) completionModuleForSigil(document Document, context templateSigilContext, offset int) (*goxcompiler.Module, int, bool) {
	if module, semanticOffset, live := s.completionModule(document, offset); live {
		return module, semanticOffset, true
	}
	replacement := ""
	if !insideOpenTemplateMarkup(document.Text, context.ReplaceStart) {
		replacement = "<span></span>"
	}
	repaired := document.Text[:context.ReplaceStart] + replacement + document.Text[offset:]
	module, err := s.parseTransientModule(document, repaired)
	return module, context.ReplaceStart, err == nil
}

func sigilSnippetItem(document Document, context templateSigilContext, offset int, label, detail, text, sortText string) (CompletionItem, bool) {
	start, startErr := document.Position(context.ReplaceStart)
	end, endErr := document.Position(offset)
	if startErr != nil || endErr != nil {
		return CompletionItem{}, false
	}
	return CompletionItem{
		Label: label, Kind: 15, Detail: detail, InsertTextFormat: 2,
		FilterText: sigilCompletionFilterText(document.Text, context, label), SortText: sortText,
		TextEdit: &CompletionTextEdit{Range: Range{Start: start, End: end}, NewText: text},
	}, true
}

func completionInt(value int) string {
	if value >= 0 && value <= 9 {
		return string(rune('0' + value))
	}
	return "1"
}
