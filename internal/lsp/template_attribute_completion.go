package lsp

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type templateAttributeCompletionContext struct {
	Sigil       byte
	Tag         string
	Attribute   string
	Prefix      string
	PrefixStart int
	Existing    map[string]bool
	Value       bool
}

type templateAttributeSpec struct {
	Name    string
	Detail  string
	Snippet string
	Values  []string
}

func (s *Server) templateAttributeCompletions(document Document, offset int, cursor cursorContext) ([]CompletionItem, bool) {
	if cursor.Kind != cursorContextTemplateAttribute || !cursor.HasAttribute {
		return nil, false
	}
	context := cursor.Attribute
	if context.Value {
		return templateAttributeValueItems(document, context, offset), true
	}
	specs := s.templateAttributeSpecs(document, context, offset)
	items := make([]CompletionItem, 0, len(specs))
	for _, spec := range specs {
		if context.Existing[spec.Name] || !strings.HasPrefix(spec.Name, context.Prefix) {
			continue
		}
		item, ok := templateAttributeItem(document, context, offset, spec)
		if ok {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SortText < items[j].SortText })
	return items, true
}

func (s *Server) templateAttributeSpecs(document Document, context templateAttributeCompletionContext, offset int) []templateAttributeSpec {
	switch context.Sigil {
	case '&':
		symbol, ok := s.componentSymbolForTag(document, context.Tag, offset)
		if !ok {
			return nil
		}
		var specs []templateAttributeSpec
		for _, parameter := range symbol.Parameters {
			if parameter.Name == "" || parameter.Name == "_" || parameter.Name == "children" && parameter.Type == "gox.Component" {
				continue
			}
			specs = append(specs, templateAttributeSpec{Name: parameter.Name, Detail: parameter.Type + " component parameter", Snippet: parameter.Name + "={{ ${1} }}"})
		}
		return specs
	case '@':
		if !s.clientTagExists(document, context.Tag) {
			return nil
		}
		return []templateAttributeSpec{
			{Name: "hydrate", Detail: "GOX client hydration strategy", Snippet: `hydrate="${1|load,idle,visible,hover,interaction|}"`, Values: []string{"load", "idle", "visible", "hover", "interaction"}},
			{Name: "adapt", Detail: "GOX client dimensional adaptation", Snippet: `adapt="${1|` + strings.Join(goxcompiler.ClientAdaptValues(), ",") + `|}"`, Values: goxcompiler.ClientAdaptValues()},
			{Name: "isolate", Detail: "GOX client style isolation", Snippet: "isolate", Values: []string{"true", "false"}},
			{Name: "transition", Detail: "GOX client replacement transition", Snippet: `transition="${1:fade}"`, Values: []string{"fade"}},
			{Name: ":spread", Detail: "Spread client props from a Go value", Snippet: ":spread={{ ${1} }}"},
		}
	case ':':
		return directiveAttributeSpecs(context.Tag)
	default:
		return htmlAttributeSpecs(context.Tag)
	}
}

func directiveAttributeSpecs(tag string) []templateAttributeSpec {
	expression := func(name, detail string) templateAttributeSpec {
		return templateAttributeSpec{Name: name, Detail: detail, Snippet: name + "={{ ${1} }}"}
	}
	switch tag {
	case "if", "else-if":
		return []templateAttributeSpec{expression("condition", "GOX conditional expression"), expression(":assign", "GOX if initializer (declare bindings before :assign)")}
	case "for":
		return []templateAttributeSpec{expression("range", "GOX range expression")}
	case "repeat":
		return []templateAttributeSpec{expression("times", "GOX repetition count")}
	case "switch":
		return []templateAttributeSpec{expression("value", "GOX switch expression")}
	case "case":
		return []templateAttributeSpec{
			expression("is", "GOX case expression or expression list"),
			{Name: "fallthrough", Detail: "Continue with the next GOX case", Snippet: "fallthrough"},
		}
	default:
		return nil
	}
}

var commonHTMLAttributeSpecs = []templateAttributeSpec{
	{Name: "id", Detail: "HTML global attribute", Snippet: `id="${1}"`},
	{Name: "class", Detail: "HTML global attribute", Snippet: `class="${1}"`},
	{Name: "style", Detail: "HTML global attribute", Snippet: `style="${1}"`},
	{Name: "title", Detail: "HTML global attribute", Snippet: `title="${1}"`},
	{Name: "hidden", Detail: "HTML boolean attribute", Snippet: "hidden"},
	{Name: "tabindex", Detail: "HTML global attribute", Snippet: `tabindex="${1:0}"`},
	{Name: "role", Detail: "ARIA role", Snippet: `role="${1}"`},
	{Name: "data-", Detail: "HTML data attribute", Snippet: `data-${1:name}="${2}"`},
	{Name: "aria-", Detail: "ARIA attribute", Snippet: `aria-${1:label}="${2}"`},
	{Name: ":spread", Detail: "Spread HTML attributes from map[string]string", Snippet: ":spread={{ ${1} }}"},
}

var tagHTMLAttributeSpecs = map[string][]templateAttributeSpec{
	"a":        {{Name: "href", Detail: "Link destination", Snippet: `href="${1}"`}, {Name: "target", Detail: "Browsing context", Snippet: `target="${1|_self,_blank,_parent,_top|}"`, Values: []string{"_self", "_blank", "_parent", "_top"}}, {Name: "rel", Detail: "Link relationship", Snippet: `rel="${1}"`, Values: []string{"noopener", "noreferrer", "nofollow", "external", "author", "help", "license"}}, {Name: "download", Detail: "Download link", Snippet: "download"}},
	"button":   {{Name: "type", Detail: "Button type", Snippet: `type="${1|button,submit,reset|}"`, Values: []string{"button", "submit", "reset"}}, {Name: "name", Detail: "Form control name", Snippet: `name="${1}"`}, {Name: "value", Detail: "Button value", Snippet: `value="${1}"`}, {Name: "disabled", Detail: "Disable control", Snippet: "disabled"}},
	"form":     {{Name: "action", Detail: "Form submission URL", Snippet: `action="${1}"`}, {Name: "method", Detail: "Form method", Snippet: `method="${1|get,post,dialog|}"`, Values: []string{"get", "post", "dialog"}}, {Name: "enctype", Detail: "Form encoding", Snippet: `enctype="${1|application/x-www-form-urlencoded,multipart/form-data,text/plain|}"`, Values: []string{"application/x-www-form-urlencoded", "multipart/form-data", "text/plain"}}, {Name: "autocomplete", Detail: "Form autofill", Snippet: `autocomplete="${1|on,off|}"`, Values: []string{"on", "off"}}, {Name: "novalidate", Detail: "Disable form validation", Snippet: "novalidate"}},
	"img":      {{Name: "src", Detail: "Image source", Snippet: `src="${1}"`}, {Name: "alt", Detail: "Alternative text", Snippet: `alt="${1}"`}, {Name: "width", Detail: "Image width", Snippet: `width="${1}"`}, {Name: "height", Detail: "Image height", Snippet: `height="${1}"`}, {Name: "loading", Detail: "Image loading mode", Snippet: `loading="${1|eager,lazy|}"`, Values: []string{"eager", "lazy"}}, {Name: "crossorigin", Detail: "CORS mode", Snippet: `crossorigin="${1|anonymous,use-credentials|}"`, Values: []string{"anonymous", "use-credentials"}}},
	"input":    {{Name: "type", Detail: "Input type", Snippet: `type="${1|text,email,password,number,checkbox,radio,file,hidden,submit|}"`, Values: []string{"text", "email", "password", "number", "checkbox", "radio", "file", "hidden", "submit"}}, {Name: "name", Detail: "Form control name", Snippet: `name="${1}"`}, {Name: "value", Detail: "Input value", Snippet: `value="${1}"`}, {Name: "placeholder", Detail: "Input placeholder", Snippet: `placeholder="${1}"`}, {Name: "autocomplete", Detail: "Autofill hint", Snippet: `autocomplete="${1|on,off,name,email,username,current-password,new-password|}"`, Values: []string{"on", "off", "name", "email", "username", "current-password", "new-password"}}, {Name: "checked", Detail: "Checked state", Snippet: "checked"}, {Name: "disabled", Detail: "Disable control", Snippet: "disabled"}, {Name: "required", Detail: "Require a value", Snippet: "required"}},
	"script":   {{Name: "src", Detail: "Script source", Snippet: `src="${1}"`}, {Name: "type", Detail: "Script type", Snippet: `type="${1|module,text/javascript|}"`, Values: []string{"module", "text/javascript"}}, {Name: "crossorigin", Detail: "CORS mode", Snippet: `crossorigin="${1|anonymous,use-credentials|}"`, Values: []string{"anonymous", "use-credentials"}}, {Name: "async", Detail: "Load asynchronously", Snippet: "async"}, {Name: "defer", Detail: "Defer execution", Snippet: "defer"}},
	"select":   {{Name: "name", Detail: "Form control name", Snippet: `name="${1}"`}, {Name: "multiple", Detail: "Allow multiple selections", Snippet: "multiple"}, {Name: "disabled", Detail: "Disable control", Snippet: "disabled"}, {Name: "required", Detail: "Require a value", Snippet: "required"}},
	"option":   {{Name: "value", Detail: "Option value", Snippet: `value="${1}"`}, {Name: "selected", Detail: "Selected state", Snippet: "selected"}, {Name: "disabled", Detail: "Disable option", Snippet: "disabled"}},
	"textarea": {{Name: "name", Detail: "Form control name", Snippet: `name="${1}"`}, {Name: "placeholder", Detail: "Textarea placeholder", Snippet: `placeholder="${1}"`}, {Name: "rows", Detail: "Visible rows", Snippet: `rows="${1}"`}, {Name: "cols", Detail: "Visible columns", Snippet: `cols="${1}"`}, {Name: "disabled", Detail: "Disable control", Snippet: "disabled"}, {Name: "required", Detail: "Require a value", Snippet: "required"}},
}

func htmlAttributeSpecs(tag string) []templateAttributeSpec {
	specs := append([]templateAttributeSpec(nil), commonHTMLAttributeSpecs...)
	specs = append(specs, tagHTMLAttributeSpecs[strings.ToLower(tag)]...)
	return specs
}

func templateAttributeValueItems(document Document, context templateAttributeCompletionContext, offset int) []CompletionItem {
	var values []string
	for _, spec := range attributeSpecsForValue(context) {
		if spec.Name == context.Attribute {
			values = spec.Values
			break
		}
	}
	items := make([]CompletionItem, 0, len(values))
	for index, value := range values {
		if !strings.HasPrefix(value, context.Prefix) {
			continue
		}
		start, startErr := document.Position(context.PrefixStart)
		end, endErr := document.Position(offset)
		if startErr != nil || endErr != nil {
			continue
		}
		items = append(items, CompletionItem{Label: value, Kind: 12, Detail: context.Attribute + " value", SortText: fmt.Sprintf("0_%04d_%s", index, value), TextEdit: &CompletionTextEdit{Range: Range{Start: start, End: end}, NewText: value}})
	}
	return items
}

func attributeSpecsForValue(context templateAttributeCompletionContext) []templateAttributeSpec {
	if context.Sigil == '@' {
		return []templateAttributeSpec{{Name: "adapt", Values: goxcompiler.ClientAdaptValues()}, {Name: "hydrate", Values: []string{"load", "idle", "visible", "hover", "interaction"}}, {Name: "transition", Values: []string{"fade"}}, {Name: "isolate", Values: []string{"true", "false"}}}
	}
	if context.Sigil == 0 {
		return htmlAttributeSpecs(context.Tag)
	}
	return nil
}

func templateAttributeItem(document Document, context templateAttributeCompletionContext, offset int, spec templateAttributeSpec) (CompletionItem, bool) {
	start, startErr := document.Position(context.PrefixStart)
	end, endErr := document.Position(offset)
	if startErr != nil || endErr != nil {
		return CompletionItem{}, false
	}
	return CompletionItem{Label: spec.Name, Kind: 10, Detail: spec.Detail, InsertTextFormat: 2, SortText: "0_" + spec.Name, TextEdit: &CompletionTextEdit{Range: Range{Start: start, End: end}, NewText: spec.Snippet}}, true
}

func (s *Server) componentSymbolForTag(document Document, tag string, offset int) (goxcompiler.SourceSymbol, bool) {
	if separator := strings.IndexByte(tag, '.'); separator > 0 {
		alias, name := tag[:separator], tag[separator+1:]
		targetURI, ok := s.importedModuleURI(document, alias)
		if !ok {
			return goxcompiler.SourceSymbol{}, false
		}
		for _, indexed := range s.symbols.DocumentSymbols(targetURI) {
			if indexed.Symbol.Name == name && indexed.Symbol.Exported && sourceComponentSymbol(indexed.Symbol) {
				return indexed.Symbol, true
			}
		}
		return goxcompiler.SourceSymbol{}, false
	}
	if local, ok := s.symbols.LocalSymbolAt(document.URI, tag, offset); ok && local.Type == "gox.Component" {
		return goxcompiler.SourceSymbol{Name: tag, Kind: goxcompiler.SymbolVariable, Type: local.Type, Detail: local.Detail}, true
	}
	for _, indexed := range s.symbols.DocumentSymbols(document.URI) {
		if indexed.Symbol.Name == tag && indexed.Symbol.Container == "" && sourceComponentSymbol(indexed.Symbol) {
			return indexed.Symbol, true
		}
	}
	return goxcompiler.SourceSymbol{}, false
}

func (s *Server) clientTagExists(document Document, tag string) bool {
	for _, indexed := range s.symbols.DocumentSymbols(document.URI) {
		if indexed.Symbol.Kind == goxcompiler.SymbolClient && indexed.Symbol.Name == tag {
			return true
		}
	}
	return false
}

func rawTemplateAttributeContextAt(source string, offset int) (templateAttributeCompletionContext, bool) {
	if offset < 0 || offset > len(source) || rawInsideTemplateExpression(source, offset) {
		return templateAttributeCompletionContext{}, false
	}
	open := incompleteTemplateTagStart(source, offset)
	if open < 0 {
		return templateAttributeCompletionContext{}, false
	}
	body := source[open+1 : offset]
	if body == "" || strings.HasPrefix(body, "/") || strings.HasPrefix(body, "!") || strings.HasPrefix(body, "?") {
		return templateAttributeCompletionContext{}, false
	}
	context := templateAttributeCompletionContext{Existing: make(map[string]bool)}
	index := 0
	if strings.ContainsRune(":&@", rune(body[0])) {
		context.Sigil = body[0]
		index++
	}
	nameStart := index
	for index < len(body) && templateAttributeNameRune(body[index:]) {
		_, size := utf8.DecodeRuneInString(body[index:])
		index += size
	}
	if index == nameStart || index == len(body) || !unicode.IsSpace(rune(body[index])) {
		return templateAttributeCompletionContext{}, false
	}
	context.Tag = body[nameStart:index]
	for index < len(body) {
		for index < len(body) && unicode.IsSpace(rune(body[index])) {
			index++
		}
		if index == len(body) {
			context.PrefixStart = open + 1 + index
			return context, true
		}
		if body[index] == '/' {
			return templateAttributeCompletionContext{}, false
		}
		attributeStart := index
		for index < len(body) && templateAttributeNameRune(body[index:]) {
			_, size := utf8.DecodeRuneInString(body[index:])
			index += size
		}
		if attributeStart == index {
			return templateAttributeCompletionContext{}, false
		}
		attribute := body[attributeStart:index]
		for index < len(body) && unicode.IsSpace(rune(body[index])) {
			index++
		}
		if index == len(body) {
			context.Prefix, context.PrefixStart = attribute, open+1+attributeStart
			return context, true
		}
		if body[index] != '=' {
			context.Existing[attribute] = true
			continue
		}
		index++
		for index < len(body) && unicode.IsSpace(rune(body[index])) {
			index++
		}
		if index == len(body) {
			context.Attribute, context.Value, context.PrefixStart = attribute, true, offset
			return context, true
		}
		if body[index] == '"' || body[index] == '\'' {
			quote := body[index]
			index++
			valueStart := index
			for index < len(body) && body[index] != quote {
				if body[index] == '\\' && index+1 < len(body) {
					index += 2
				} else {
					index++
				}
			}
			if index == len(body) {
				context.Attribute, context.Value = attribute, true
				context.Prefix, context.PrefixStart = body[valueStart:index], open+1+valueStart
				return context, true
			}
			index++
			context.Existing[attribute] = true
			continue
		}
		if strings.HasPrefix(body[index:], "{{") {
			end := strings.Index(body[index+2:], "}}")
			if end < 0 {
				return templateAttributeCompletionContext{}, false
			}
			index += 2 + end + 2
			context.Existing[attribute] = true
			continue
		}
		valueStart := index
		for index < len(body) && !unicode.IsSpace(rune(body[index])) {
			index++
		}
		if index == len(body) {
			context.Attribute, context.Value = attribute, true
			context.Prefix, context.PrefixStart = body[valueStart:index], open+1+valueStart
			return context, true
		}
		context.Existing[attribute] = true
	}
	return templateAttributeCompletionContext{}, false
}

func incompleteTemplateTagStart(source string, offset int) int {
	quote := byte(0)
	expressionDepth := 0
	lastOpen := -1
	for index := 0; index < offset; index++ {
		if quote != 0 {
			if source[index] == '\\' {
				index++
			} else if source[index] == quote {
				quote = 0
			}
			continue
		}
		if index+1 < offset && source[index:index+2] == "{{" {
			expressionDepth++
			index++
			continue
		}
		if expressionDepth > 0 && index+1 < offset && source[index:index+2] == "}}" {
			expressionDepth--
			index++
			continue
		}
		if expressionDepth > 0 {
			continue
		}
		switch source[index] {
		case '<':
			lastOpen = index
		case '>':
			lastOpen = -1
		case '"', '\'':
			if lastOpen >= 0 {
				quote = source[index]
			}
		}
	}
	return lastOpen
}

func templateAttributeNameRune(source string) bool {
	value, _ := utf8.DecodeRuneInString(source)
	return value == ':' || value == '-' || value == '_' || value == '.' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

// Static client controls do not need a new Go semantic model for each keystroke.
// Keep expression, string/comment and embedded-language ownership checks, then
// use the known declaration index only to confirm the client tag exists.
func (s *Server) fastClientAttributeCompletion(document Document, position Position) (CompletionList, bool) {
	offset, err := document.Offset(position)
	if err != nil {
		return CompletionList{}, false
	}
	attribute, ok := rawTemplateAttributeContextAt(document.Text, offset)
	if !ok || attribute.Sigil != '@' || !s.clientTagExists(document, attribute.Tag) {
		return CompletionList{}, false
	}
	start := incompleteTemplateTagStart(document.Text, offset)
	if start < 0 || !rawGoLexicalContext(document.Text, start) || !rawTemplateMarkupContextAt(document.Text, offset) {
		return CompletionList{}, false
	}
	cursor := classifyCursorContext(document.Text, offset, nil)
	if cursor.Kind != cursorContextTemplateAttribute {
		return CompletionList{}, false
	}
	items, handled := s.templateAttributeCompletions(document, offset, cursor)
	return completionList(items), handled
}
