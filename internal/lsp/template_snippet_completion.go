package lsp

import (
	"strings"
	"unicode"
	"unicode/utf8"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type templateSnippet struct {
	label  string
	detail string
	text   string
}

var htmlElementNames = []string{
	"a", "abbr", "address", "article", "aside", "audio", "b", "base", "bdi", "bdo", "blockquote", "body", "br", "button",
	"canvas", "caption", "cite", "code", "col", "colgroup", "data", "datalist", "dd", "del", "details", "dfn", "dialog", "div", "dl", "dt",
	"em", "embed", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "head", "header", "hgroup", "hr", "html",
	"i", "iframe", "img", "input", "ins", "kbd", "label", "legend", "li", "link", "main", "map", "mark", "menu", "meta", "meter", "nav", "noscript",
	"object", "ol", "optgroup", "option", "output", "p", "picture", "pre", "progress", "q", "rp", "rt", "ruby", "s", "samp", "script", "search",
	"section", "select", "slot", "small", "source", "span", "strong", "style", "sub", "summary", "sup", "table", "tbody", "td", "template", "textarea",
	"tfoot", "th", "thead", "time", "title", "tr", "track", "u", "ul", "var", "video", "wbr",
}

var htmlVoidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true,
	"link": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true,
}

var templateDirectiveSnippets = []templateSnippet{
	{label: "if", detail: "GOX conditional directive", text: "<:if condition={{ ${1} }}>\n\t${2}\n</:if>"},
	{label: "for", detail: "GOX range directive", text: "<:for ${1:index} ${2:item} range={{ ${3:items} }}>\n\t${4}\n</:for>"},
	{label: "switch", detail: "GOX switch directive", text: "<:switch value={{ ${1} }}>\n\t<:case is={{ ${2} }}>\n\t\t${3}\n\t</:case>\n\t<:default>\n\t\t${4}\n\t</:default>\n</:switch>"},
	{label: "repeat", detail: "GOX repeat directive", text: "<:repeat ${1:index} times={{ ${2} }}>\n\t${3}\n</:repeat>"},
	{label: "break", detail: "GOX loop break directive", text: "<:break />"},
	{label: "continue", detail: "GOX loop continue directive", text: "<:continue />"},
}

func (s *Server) templateNodeCompletions(document Document, prefix string, prefixStart, offset int) ([]CompletionItem, bool) {
	module, semanticOffset, live := s.completionModule(document, offset)
	if !templateNodeContext(document.Text, offset, prefixStart, module, semanticOffset, live) {
		return nil, false
	}
	items := make([]CompletionItem, 0)
	for _, tag := range htmlElementNames {
		if !strings.HasPrefix(tag, prefix) {
			continue
		}
		items = append(items, CompletionItem{
			Label: tag, Kind: 15, Detail: "HTML element <" + tag + ">",
			InsertText: htmlElementSnippet(tag), InsertTextFormat: 2, SortText: "0_" + tag,
		})
	}
	for _, directive := range templateDirectiveSnippets {
		if !strings.HasPrefix(directive.label, prefix) {
			continue
		}
		items = append(items, CompletionItem{
			Label: directive.label, Kind: 15, Detail: directive.detail,
			InsertText: directive.text, InsertTextFormat: 2, SortText: "1_" + directive.label,
		})
	}
	if validCustomElementName(prefix) {
		start, startErr := document.Position(prefixStart)
		end, endErr := document.Position(offset)
		if startErr == nil && endErr == nil {
			snippet := "<" + prefix + ">${1}</" + prefix + ">"
			items = append(items, CompletionItem{
				Label: prefix, Kind: 15, Detail: "HTML custom element <" + prefix + ">",
				InsertTextFormat: 2, SortText: "0_" + prefix,
				TextEdit: &CompletionTextEdit{Range: Range{Start: start, End: end}, NewText: snippet},
			})
		}
	}
	return items, true
}

func templateNodeContext(source string, offset, prefixStart int, module *goxcompiler.Module, semanticOffset int, live bool) bool {
	if classifyCursorContext(source, offset, nil).Kind == cursorContextTemplateExpression || templateTagOpenAt(source, offset) {
		return false
	}
	if insideOpenTemplateMarkup(source, offset) {
		return true
	}
	if live && module != nil {
		for _, template := range module.Templates {
			if semanticOffset >= template.Start && semanticOffset <= template.End {
				return true
			}
		}
	}
	return expectedTemplateValue(source, prefixStart)
}

func templateCompletionPrefixAt(source string, offset int) (string, int) {
	if offset < 0 || offset > len(source) {
		return "", offset
	}
	start := offset
	for start > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:start])
		if value != '-' && value != '.' && value != '_' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
			break
		}
		start -= size
	}
	return source[start:offset], start
}

var reservedCustomElementNames = map[string]bool{
	"annotation-xml":   true,
	"color-profile":    true,
	"font-face":        true,
	"font-face-src":    true,
	"font-face-uri":    true,
	"font-face-format": true,
	"font-face-name":   true,
	"missing-glyph":    true,
}

func validCustomElementName(name string) bool {
	if reservedCustomElementNames[name] || !strings.ContainsRune(name, '-') || name == "" {
		return false
	}
	for index, value := range name {
		if index == 0 {
			if value < 'a' || value > 'z' {
				return false
			}
			continue
		}
		if value == '-' || value == '.' || value == '_' || value >= 'a' && value <= 'z' || unicode.IsDigit(value) || unicode.IsLower(value) {
			continue
		}
		return false
	}
	return true
}

func insideOpenTemplateMarkup(source string, offset int) bool {
	depth := 0
	for index := 0; index < offset; index++ {
		if source[index] != '<' {
			continue
		}
		end := strings.IndexByte(source[index:offset], '>')
		if end < 0 {
			break
		}
		end += index
		body := strings.TrimSpace(source[index+1 : end])
		if body == "" {
			depth++
			index = end
			continue
		}
		if body == "/" {
			if depth > 0 {
				depth--
			}
			index = end
			continue
		}
		if strings.HasPrefix(body, "!") || strings.HasPrefix(body, "?") {
			index = end
			continue
		}
		closing := strings.HasPrefix(body, "/")
		name := templateMarkupTagName(strings.TrimPrefix(body, "/"))
		if name == "" {
			continue
		}
		selfClosing := strings.HasSuffix(body, "/") || htmlVoidElements[name]
		if closing {
			if depth > 0 {
				depth--
			}
		} else if !selfClosing {
			depth++
		}
		index = end
	}
	return depth > 0
}

func templateMarkupTagName(body string) string {
	body = strings.TrimLeft(body, ":&@")
	end := 0
	for end < len(body) {
		value, size := utf8.DecodeRuneInString(body[end:])
		if value != '.' && value != '_' && value != '-' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
			break
		}
		end += size
	}
	return body[:end]
}

func expectedTemplateReturn(source string, prefixStart int) bool {
	before := skipSourceSpacesBackward(source, prefixStart)
	if before < len("return") || source[before-len("return"):before] != "return" ||
		before > len("return") && completionIdentifierRuneBefore(source, before-len("return")) {
		return false
	}
	functionStart := strings.LastIndex(source[:before-len("return")], "func ")
	if functionStart < 0 {
		return false
	}
	bodyStart := strings.IndexByte(source[functionStart:before], '{')
	if bodyStart < 0 {
		return false
	}
	signature := source[functionStart : functionStart+bodyStart]
	return strings.Contains(signature, "gox.Component") || strings.Contains(signature, "gox.Page")
}

// expectedTemplateValue mirrors the value boundaries accepted by the GOX
// template-expression transformer. Returns need the surrounding component or
// page signature, while assignment RHSs and call arguments are syntactic
// template value positions in their own right.
func expectedTemplateValue(source string, prefixStart int) bool {
	if expectedTemplateReturn(source, prefixStart) {
		return true
	}
	before := skipSourceSpacesBackward(source, prefixStart)
	if before == 0 {
		return false
	}
	last := source[before-1]
	if last == '(' || last == ',' {
		return true
	}
	if last != '=' {
		return false
	}
	if before < 2 {
		return true
	}
	switch source[before-2] {
	case '=', '!', '<', '>':
		return false
	}
	return true
}

func completionIdentifierRuneBefore(source string, offset int) bool {
	if offset <= 0 {
		return false
	}
	value, _ := utf8.DecodeLastRuneInString(source[:offset])
	return value == '_' || unicode.IsLetter(value) || unicode.IsDigit(value)
}

func templateTagOpenAt(source string, offset int) bool {
	open := strings.LastIndexByte(source[:offset], '<')
	close := strings.LastIndexByte(source[:offset], '>')
	return open > close
}

func htmlElementSnippet(tag string) string {
	switch tag {
	case "a":
		return `<a href="${1}">${2}</a>`
	case "img":
		return `<img src="${1}" alt="${2}">`
	case "input":
		return `<input type="${1:text}" name="${2}">`
	case "link":
		return `<link rel="${1}" href="${2}">`
	case "meta":
		return `<meta name="${1}" content="${2}">`
	case "script":
		return `<script>${1}</script>`
	case "style":
		return `<style>${1}</style>`
	}
	if htmlVoidElements[tag] {
		return "<" + tag + ">"
	}
	return "<" + tag + ">${1}</" + tag + ">"
}

func goStatementSnippetCompletions(prefix string) []CompletionItem {
	snippets := []templateSnippet{
		{label: "if", detail: "Go if statement", text: "if ${1:condition} {\n\t${2}\n}"},
		{label: "for", detail: "Go for statement", text: "for ${1:condition} {\n\t${2}\n}"},
		{label: "switch", detail: "Go switch statement", text: "switch ${1:value} {\ncase ${2}:\n\t${3}\ndefault:\n\t${4}\n}"},
		{label: "go", detail: "GOX safe goroutine", text: "go func() {\n\tdefer func() {\n\t\tif err := recover(); err != nil {\n\t\t\t${1:// handle panic}\n\t\t}\n\t}()\n\n\t${2}\n}()"},
	}
	items := make([]CompletionItem, 0, len(snippets))
	for _, snippet := range snippets {
		if !strings.HasPrefix(snippet.label, prefix) {
			continue
		}
		items = append(items, CompletionItem{
			Label: snippet.label, Kind: 15, Detail: snippet.detail,
			InsertText: snippet.text, InsertTextFormat: 2, SortText: "3_" + snippet.label,
		})
	}
	return items
}
