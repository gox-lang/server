package lsp

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type templateExpectedValue struct {
	Type       string
	Underlying string
	Alias      bool
	Category   string
	Prefix     string
	Values     []string
}

type templateExpressionAttribute struct {
	Sigil     byte
	Tag       string
	Attribute string
}

func (s *Server) rankTemplateExpressionCompletions(document Document, offset int, cursor cursorContext, items []CompletionItem) []CompletionItem {
	expected, ok := s.templateExpectedValueAt(document, offset, cursor)
	if !ok {
		return items
	}
	for index := range items {
		s.completeCandidateType(document, &items[index])
	}
	items = append(items, expectedValueCompletions(expected)...)
	items = append(items, expectedLiteralCompletions(expected)...)
	for index := range items {
		compatibility := completionTypeCompatibility(expected, items[index])
		original := items[index].SortText
		if original == "" {
			original = items[index].Label
		}
		items[index].SortText = completionCompatibilityPrefix(compatibility) + original
	}
	return items
}

func (s *Server) templateExpectedValueAt(document Document, offset int, cursor cursorContext) (templateExpectedValue, bool) {
	if cursor.Kind != cursorContextTemplateExpression {
		return templateExpectedValue{}, false
	}
	attribute, ok := templateExpressionAttributeAt(document.Text, offset)
	if !ok {
		return templateExpectedValue{}, false
	}
	expected := templateExpectedValue{}
	if cursor.HasLexical {
		expected.Prefix = cursor.Lexical.Prefix
	}
	switch attribute.Sigil {
	case '&':
		symbol, found := s.componentSymbolForTag(document, attribute.Tag, offset)
		if !found {
			return templateExpectedValue{}, false
		}
		for _, parameter := range symbol.Parameters {
			if parameter.Name == attribute.Attribute {
				expected.Type = parameter.Type
				if separator := strings.IndexByte(attribute.Tag, '.'); separator > 0 {
					alias := attribute.Tag[:separator]
					if targetURI, found := s.importedModuleURI(document, alias); found {
						expected.Type = s.qualifyImportedType(targetURI, alias, expected.Type)
					}
				}
				return s.resolveExpectedType(document, expected), expected.Type != ""
			}
		}
	case '@':
		switch attribute.Attribute {
		case "hydrate", "transition":
			expected.Type = "string"
		case "isolate":
			expected.Type = "bool"
		case ":spread":
			expected.Category = "map"
		default:
			return templateExpectedValue{}, false
		}
		if expected.Type == "string" {
			expected.Values = templateExpressionAttributeValues(attribute)
		}
		return s.resolveExpectedType(document, expected), true
	case ':':
		switch attribute.Tag + "." + attribute.Attribute {
		case "if.condition", "else-if.condition":
			expected.Type = "bool"
		case "repeat.times":
			expected.Category = "integer"
		case "for.range":
			expected.Category = "range"
		case "switch.value":
			return templateExpectedValue{}, false
		case "case.is":
			if typeName := s.enclosingSwitchType(document, offset); typeName != "" {
				expected.Type = typeName
			} else {
				return templateExpectedValue{}, false
			}
		default:
			return templateExpectedValue{}, false
		}
		return s.resolveExpectedType(document, expected), true
	default:
		if attribute.Attribute == ":spread" {
			expected.Category = "string-map"
		} else {
			expected.Type = "string"
		}
		expected.Values = templateExpressionAttributeValues(attribute)
		return s.resolveExpectedType(document, expected), true
	}
	return templateExpectedValue{}, false
}

func templateExpressionAttributeAt(source string, offset int) (templateExpressionAttribute, bool) {
	if offset < 0 || offset > len(source) || classifyCursorContext(source, offset, nil).Kind != cursorContextTemplateExpression {
		return templateExpressionAttribute{}, false
	}
	expressionStart := strings.LastIndex(source[:offset], "{{")
	before := skipSourceSpacesBackward(source, expressionStart)
	if before == 0 || source[before-1] != '=' {
		return templateExpressionAttribute{}, false
	}
	nameEnd := skipSourceSpacesBackward(source, before-1)
	nameStart := nameEnd
	for nameStart > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:nameStart])
		if value != ':' && value != '-' && value != '_' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
			break
		}
		nameStart -= size
	}
	if nameStart == nameEnd {
		return templateExpressionAttribute{}, false
	}
	open := incompleteTemplateTagStart(source, expressionStart)
	if open < 0 {
		return templateExpressionAttribute{}, false
	}
	body := source[open+1 : expressionStart]
	index := 0
	attribute := templateExpressionAttribute{Attribute: source[nameStart:nameEnd]}
	if len(body) > 0 && strings.ContainsRune(":&@", rune(body[0])) {
		attribute.Sigil = body[0]
		index++
	}
	tagStart := index
	for index < len(body) && templateAttributeNameRune(body[index:]) {
		_, size := utf8.DecodeRuneInString(body[index:])
		index += size
	}
	if index == tagStart {
		return templateExpressionAttribute{}, false
	}
	attribute.Tag = body[tagStart:index]
	return attribute, true
}

func (s *Server) enclosingSwitchType(document Document, offset int) string {
	source := document.Text
	search := offset
	for search > 0 {
		start := strings.LastIndex(source[:search], "<:switch")
		if start < 0 {
			return ""
		}
		end := templateTagEnd(source, start+1, offset)
		if end < 0 {
			search = start
			continue
		}
		body := source[start:end]
		value := strings.Index(body, "value={{")
		if value < 0 {
			search = start
			continue
		}
		expressionStart := value + len("value={{")
		expressionEnd := strings.Index(body[expressionStart:], "}}")
		if expressionEnd < 0 {
			return ""
		}
		rawExpression := body[expressionStart : expressionStart+expressionEnd]
		leading := len(rawExpression) - len(strings.TrimLeft(rawExpression, " \t\r\n"))
		expressionOffset := start + expressionStart + leading

		// Completion frequently runs while the active case expression is empty
		// or contains a dangling selector. Ask the repaired completion module for
		// the switch expression type so go/types remains the primary authority
		// even though the exact editor document is temporarily invalid. The
		// switch expression precedes the active cursor, so selector repair does
		// not change this source offset.
		if module, _, live := s.completionModule(document, offset); live {
			if typeName, ok := semanticCompletionTypeAtModule(module, expressionOffset); ok {
				return typeName
			}
		}

		// If even the repaired completion module cannot type the switch value,
		// leave the candidates unranked. Phase 13 deliberately removes the old
		// string-based pseudo type checker rather than inventing a second semantic
		// authority for malformed editor text.
		return ""
	}
	return ""
}

func completionItemValueType(item CompletionItem) string {
	if item.Kind == 3 || item.Kind == 2 {
		if len(item.CandidateResults) == 1 {
			return item.CandidateResults[0]
		}
		if len(item.CandidateResults) != 0 {
			return "(multiple)"
		}
	}
	return item.CandidateType
}

func completionTypeCompatibility(expected templateExpectedValue, item CompletionItem) int {
	candidate := normalizeCompletionType(completionItemValueType(item))
	if candidate == "" {
		return 1
	}
	if candidate == "(multiple)" {
		return 2
	}
	if expected.Type != "" {
		want := normalizeCompletionType(expected.Type)
		if candidate == want || strings.TrimPrefix(candidate, "*") == strings.TrimPrefix(want, "*") {
			return 0
		}
		if expected.Alias && expected.Underlying != "" {
			want = normalizeCompletionType(expected.Underlying)
		}
		if item.CandidateAlias && item.CandidateUnderlying != "" {
			candidate = normalizeCompletionType(item.CandidateUnderlying)
		}
		if completionTypeFamily(candidate) == completionTypeFamily(want) && completionTypeFamily(want) != "named" {
			return 0
		}
		return 2
	}
	switch expected.Category {
	case "integer":
		if completionTypeFamily(candidate) == "integer" {
			return 0
		}
	case "range":
		if candidate == "string" || strings.HasPrefix(candidate, "[]") || strings.HasPrefix(candidate, "[") || strings.HasPrefix(candidate, "map[") || strings.HasPrefix(candidate, "chan") || strings.HasPrefix(candidate, "<-chan") {
			return 0
		}
	case "map":
		if strings.HasPrefix(candidate, "map[") {
			return 0
		}
	case "string-map":
		if strings.HasPrefix(candidate, "map[string]string") {
			return 0
		}
	}
	return 2
}

func normalizeCompletionType(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func completionTypeFamily(value string) string {
	switch value {
	case "bool", "untypedbool":
		return "bool"
	case "string", "untypedstring":
		return "string"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune", "untypedint", "untypedrune":
		return "integer"
	case "float32", "float64", "untypedfloat":
		return "float"
	case "complex64", "complex128", "untypedcomplex":
		return "complex"
	default:
		return "named"
	}
}

func completionCompatibilityPrefix(value int) string {
	switch value {
	case 0:
		return "0_"
	case 1:
		return "1_"
	default:
		return "2_"
	}
}

func expectedLiteralCompletions(expected templateExpectedValue) []CompletionItem {
	appendLiteral := func(items []CompletionItem, label, insert, typeName, sortText string, snippet bool) []CompletionItem {
		if expected.Prefix != "" && !strings.HasPrefix(label, expected.Prefix) {
			return items
		}
		format := 0
		if snippet {
			format = 2
		}
		candidateType := typeName
		if expected.Type != "" {
			candidateType = expected.Type
		}
		return append(items, CompletionItem{Label: label, Kind: 12, Detail: typeName + " literal", InsertText: insert, InsertTextFormat: format, SortText: sortText, CandidateType: candidateType})
	}
	var items []CompletionItem
	effectiveType := expected.Type
	if expected.Underlying != "" {
		effectiveType = expected.Underlying
	}
	if normalizeCompletionType(effectiveType) == "bool" {
		items = appendLiteral(items, "true", "true", "bool", "0_literal_true", false)
		items = appendLiteral(items, "false", "false", "bool", "0_literal_false", false)
	}
	if normalizeCompletionType(effectiveType) == "string" && len(expected.Values) == 0 {
		items = appendLiteral(items, `""`, `"${1}"`, "string", "0_literal_string", true)
	}
	if expected.Category == "integer" || completionTypeFamily(normalizeCompletionType(effectiveType)) == "integer" {
		items = appendLiteral(items, "0", "0", "int", "0_literal_zero", false)
	}
	if expected.Category == "map" || expected.Category == "string-map" || completionNilableType(effectiveType) {
		items = appendLiteral(items, "nil", "nil", expected.Type, "0_literal_nil", false)
	}
	return items
}

func expectedValueCompletions(expected templateExpectedValue) []CompletionItem {
	items := make([]CompletionItem, 0, len(expected.Values))
	for index, value := range expected.Values {
		if expected.Prefix != "" && !strings.HasPrefix(value, strings.Trim(expected.Prefix, `"'`)) {
			continue
		}
		items = append(items, CompletionItem{
			Label: `"` + value + `"`, Kind: 12, Detail: "valid " + value + " value",
			InsertText: `"` + value + `"`, SortText: "0_value_" + completionInt(index) + "_" + value,
			CandidateType: expected.Type,
		})
	}
	return items
}

func templateExpressionAttributeValues(attribute templateExpressionAttribute) []string {
	context := templateAttributeCompletionContext{Sigil: attribute.Sigil, Tag: attribute.Tag, Attribute: attribute.Attribute}
	for _, spec := range attributeSpecsForValue(context) {
		if spec.Name == attribute.Attribute {
			return append([]string(nil), spec.Values...)
		}
	}
	return nil
}

func (s *Server) resolveExpectedType(document Document, expected templateExpectedValue) templateExpectedValue {
	underlying, alias := s.resolveCompletionUnderlying(document, expected.Type)
	expected.Underlying, expected.Alias = underlying, alias
	return expected
}

func (s *Server) completeCandidateType(document Document, item *CompletionItem) {
	if item == nil || item.CandidateType == "" || item.CandidateUnderlying != "" {
		return
	}
	item.CandidateUnderlying, item.CandidateAlias = s.resolveCompletionUnderlying(document, item.CandidateType)
}

func (s *Server) resolveCompletionUnderlying(document Document, typeName string) (string, bool) {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" {
		return "", false
	}
	if module, ok := s.parseSemanticModule(document); ok {
		if object := semanticCompletionTypeName(module, typeName); object != nil {
			return goObjectUnderlyingType(object, semanticCompletionQualifier(module))
		}
	}

	// Compatibility fallback for malformed editor text that cannot produce a
	// semantic snapshot. The indexed path remains read-only during Phase 9.
	if qualifier, name, ok := importedNamedType(typeName); ok {
		if targetURI, found := s.importedModuleURI(document, qualifier); found {
			for _, indexed := range s.symbols.DocumentSymbols(targetURI) {
				symbol := indexed.Symbol
				if symbol.Container == "" && symbol.Name == name && typeDefinitionKind(symbol.Kind) {
					return s.qualifyImportedType(targetURI, qualifier, symbol.Underlying), symbol.Alias
				}
			}
		}
		if _, packageInfo, alias, _, found := s.resolveGoPackageImport(document, qualifier); found && alias == qualifier {
			for _, exported := range packageInfo.Exports {
				if exported.Name == name {
					return exported.Underlying, exported.Alias
				}
			}
		}
		return "", false
	}
	for _, indexed := range s.symbols.DocumentSymbols(document.URI) {
		symbol := indexed.Symbol
		if symbol.Container == "" && symbol.Name == typeName && typeDefinitionKind(symbol.Kind) {
			return symbol.Underlying, symbol.Alias
		}
	}
	return "", false
}

func completionNilableType(value string) bool {
	value = normalizeCompletionType(value)
	return strings.HasPrefix(value, "*") || strings.HasPrefix(value, "[]") || strings.HasPrefix(value, "map[") || strings.HasPrefix(value, "chan") || strings.HasPrefix(value, "func(") || value == "any" || strings.HasPrefix(value, "interface{")
}
