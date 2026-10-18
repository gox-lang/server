package gox

import (
	"bytes"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
)

// FormatTemplate formats one complete GOX template. baseIndent is already
// present before the first tag and is applied to every following line.
func FormatTemplate(source, baseIndent string) (string, error) {
	trimmed := strings.TrimSpace(source)
	fragment := strings.HasPrefix(trimmed, "<>") && strings.HasSuffix(trimmed, "</>")
	parseSource := trimmed
	if fragment {
		parseSource = strings.TrimSpace(trimmed[2 : len(trimmed)-3])
	}
	template, err := ParseTemplate(parseSource)
	if err != nil {
		return "", err
	}
	renderer := templateRenderer{}
	if fragment {
		renderer.line(0, "<>")
		for _, child := range templateRootChildren(template.Root) {
			renderer.renderNode(child, 1)
		}
		renderer.line(0, "</>")
	} else {
		renderer.renderNode(template.Root, 0)
	}
	result := strings.TrimSuffix(renderer.builder.String(), "\n")
	if baseIndent != "" {
		result = strings.ReplaceAll(result, "\n", "\n"+baseIndent)
	}
	return result, nil
}

type templateRenderer struct {
	builder strings.Builder
}

func (r *templateRenderer) line(level int, value string) {
	r.builder.WriteString(strings.Repeat("\t", level))
	r.builder.WriteString(value)
	r.builder.WriteByte('\n')
}

func (r *templateRenderer) renderNode(node TemplateNode, level int) {
	switch value := node.(type) {
	case *FragmentNode:
		for _, child := range effectiveTemplateChildren(value.Children) {
			r.renderNode(child, level)
		}
	case *ElementNode:
		r.renderContainer("", value.Tag, value.Attrs, value.Children, value.SelfClosed, level)
	case *DirectiveNode:
		r.renderContainer(":", value.Name, value.Attrs, value.Children, value.SelfClosed, level)
	case *ComponentNode:
		r.renderContainer("&", value.Name, value.Attrs, value.Children, value.SelfClosed, level)
	case *ClientNode:
		r.renderContainer("@", value.Name, value.Attrs, value.Children, value.SelfClosed, level)
	case *TextNode:
		if text := strings.TrimSpace(value.Value); text != "" {
			r.line(level, text)
		}
	case *ExprNode:
		r.line(level, formatTemplateExpr(value.Raw, value.Mode))
	case *CommentNode:
		r.line(level, "<!--"+value.Value+"-->")
	}
}

func (r *templateRenderer) renderContainer(prefix, name string, attrs []TemplateAttr, children []TemplateNode, selfClosed bool, level int) {
	open := "<" + prefix + name
	formattedAttrs := make([]string, 0, len(attrs))
	for _, attribute := range attrs {
		formattedAttrs = append(formattedAttrs, formatTemplateAttribute(attribute))
	}
	ending := ">"
	if selfClosed {
		ending = " />"
	}
	single := open
	if len(formattedAttrs) > 0 {
		single += " " + strings.Join(formattedAttrs, " ")
	}
	single += ending
	if len(single)+level*4 <= 100 {
		open = single
	} else {
		var builder strings.Builder
		builder.WriteString(open)
		for _, attribute := range formattedAttrs {
			builder.WriteByte('\n')
			builder.WriteString(strings.Repeat("\t", level+1))
			builder.WriteString(attribute)
		}
		builder.WriteByte('\n')
		builder.WriteString(strings.Repeat("\t", level))
		builder.WriteString(strings.TrimSpace(ending))
		open = builder.String()
	}
	children = effectiveTemplateChildren(children)
	if selfClosed {
		r.line(level, open)
		return
	}
	close := "</" + prefix + name + ">"
	if prefix == "" && isEmbeddedCodeTag(name) {
		if content, ok := embeddedCodeContent(children); ok {
			r.line(level, open)
			r.renderEmbeddedCode(content, level+1)
			r.line(level, close)
			return
		}
	}
	if prefix != ":" && templateChildrenInline(children) {
		var inline strings.Builder
		inline.WriteString(open)
		for _, child := range children {
			inline.WriteString(renderTemplateInline(child))
		}
		inline.WriteString(close)
		r.line(level, inline.String())
		return
	}
	r.line(level, open)
	for _, child := range children {
		r.renderNode(child, level+1)
	}
	r.line(level, close)
}

func isEmbeddedCodeTag(name string) bool {
	return strings.EqualFold(name, "style") || strings.EqualFold(name, "script")
}

func embeddedCodeContent(children []TemplateNode) (string, bool) {
	var builder strings.Builder
	for _, child := range children {
		switch value := child.(type) {
		case *TextNode:
			builder.WriteString(value.Value)
		case *ExprNode:
			builder.WriteString(formatTemplateExpr(value.Raw, value.Mode))
		default:
			return "", false
		}
	}
	return builder.String(), true
}

func (r *templateRenderer) renderEmbeddedCode(source string, level int) {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	lines := strings.Split(source, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return
	}

	commonIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if commonIndent < 0 || indent < commonIndent {
			commonIndent = indent
		}
	}
	if commonIndent < 0 {
		commonIndent = 0
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			r.builder.WriteByte('\n')
			continue
		}
		trim := commonIndent
		if leading := len(line) - len(strings.TrimLeft(line, " \t")); trim > leading {
			trim = leading
		}
		r.line(level, line[trim:])
	}
}

func formatTemplateAttribute(attribute TemplateAttr) string {
	switch attribute.Kind {
	case AttrBoolean:
		return attribute.Name
	case AttrStatic:
		return attribute.Name + "=" + quoteTemplateAttribute(attribute.Value)
	case AttrExpr:
		return attribute.Name + "=" + formatTemplateExpr(attribute.Raw, attribute.Mode)
	case AttrSpecial:
		if attribute.Raw != "" {
			return attribute.Name + "=" + formatTemplateExpr(attribute.Raw, attribute.Mode)
		}
		if attribute.Value != "" {
			return attribute.Name + "=" + quoteTemplateAttribute(attribute.Value)
		}
		return attribute.Name
	default:
		return attribute.Name
	}
}

func quoteTemplateAttribute(value string) string {
	if !strings.Contains(value, `"`) {
		return `"` + value + `"`
	}
	return `'` + value + `'`
}

func formatTemplateExpr(source string, mode TemplateExprMode) string {
	formatted := strings.TrimSpace(source)
	if expression, err := parser.ParseExpr(formatted); err == nil {
		var output bytes.Buffer
		if format.Node(&output, token.NewFileSet(), expression) == nil {
			formatted = output.String()
		}
	} else if expressionList, listErr := parser.ParseExpr("[]any{" + formatted + "}"); listErr == nil {
		var output bytes.Buffer
		if format.Node(&output, token.NewFileSet(), expressionList) == nil {
			wrapped := output.String()
			if open := strings.IndexByte(wrapped, '{'); open >= 0 && strings.HasSuffix(wrapped, "}") {
				formatted = wrapped[open+1 : len(wrapped)-1]
			}
		}
	}
	if mode == ExprRaw {
		return "{{< " + formatted + " >}}"
	}
	return "{{ " + formatted + " }}"
}

func effectiveTemplateChildren(children []TemplateNode) []TemplateNode {
	result := make([]TemplateNode, 0, len(children))
	for _, child := range children {
		if text, ok := child.(*TextNode); ok && strings.TrimSpace(text.Value) == "" {
			continue
		}
		result = append(result, child)
	}
	return result
}

func templateChildrenInline(children []TemplateNode) bool {
	if len(children) == 0 {
		return true
	}
	hasText := false
	for _, child := range children {
		switch value := child.(type) {
		case *TextNode:
			hasText = true
			continue
		case *ExprNode:
			continue
		case *ElementNode:
			if !hasText {
				return false
			}
			if !value.SelfClosed && !templateChildrenInline(effectiveTemplateChildren(value.Children)) {
				return false
			}
			continue
		default:
			return false
		}
	}
	return true
}

func renderTemplateInline(node TemplateNode) string {
	switch value := node.(type) {
	case *TextNode:
		return value.Value
	case *ExprNode:
		return formatTemplateExpr(value.Raw, value.Mode)
	case *ElementNode:
		open := "<" + value.Tag
		for _, attribute := range value.Attrs {
			open += " " + formatTemplateAttribute(attribute)
		}
		if value.SelfClosed {
			return open + " />"
		}
		open += ">"
		for _, child := range effectiveTemplateChildren(value.Children) {
			open += renderTemplateInline(child)
		}
		return open + "</" + value.Tag + ">"
	}
	return ""
}

func templateRootChildren(root TemplateNode) []TemplateNode {
	if fragment, ok := root.(*FragmentNode); ok {
		return effectiveTemplateChildren(fragment.Children)
	}
	return []TemplateNode{root}
}
