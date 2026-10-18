package gox

import (
	"fmt"

	"github.com/gox-lang/server/internal/goxmap"
)

// semanticTemplateProjector lowers template structure into Go-only scaffolding
// for source-level type checking. User-authored Go expressions and directive
// bindings are copied exactly from the .gox source; markup and control syntax
// are generated anchors. This keeps go/types authoritative for scopes without
// exposing runtime template machinery to the semantic model.
type semanticTemplateProjector struct {
	writer        *goxmap.Writer
	upstream      *goxmap.Map
	sourceURI     string
	source        string
	sourceLength  int
	templateRange goxmap.Range
}

func (p *semanticTemplateProjector) writeTemplateValue(template *Template) error {
	if p == nil || p.writer == nil || template == nil || template.Root == nil {
		return fmt.Errorf("semantic template projection requires writer and template")
	}
	if err := p.generated("func() interface{} {\n", goxmap.SourceRange{}); err != nil {
		return err
	}
	if err := p.writeNode(template.Root); err != nil {
		return err
	}
	return p.generated("\treturn nil\n}()", goxmap.SourceRange{})
}

func (p *semanticTemplateProjector) writeNode(node TemplateNode) error {
	if node == nil {
		return nil
	}
	switch current := node.(type) {
	case *ExprNode:
		return p.writeExpressionStatement(current.ExprSource)
	case *FragmentNode:
		return p.writeChildren(current.Children)
	case *ElementNode:
		if err := p.writeAttributeExpressions(current.Attrs, nil); err != nil {
			return err
		}
		return p.writeChildren(current.Children)
	case *ComponentNode:
		if err := p.writeAttributeExpressions(current.Attrs, nil); err != nil {
			return err
		}
		return p.writeChildren(current.Children)
	case *ClientNode:
		if err := p.writeAttributeExpressions(current.Attrs, nil); err != nil {
			return err
		}
		return p.writeChildren(current.Children)
	case *DirectiveNode:
		return p.writeDirective(current)
	case *TextNode, *CommentNode:
		return nil
	default:
		return nil
	}
}

func (p *semanticTemplateProjector) writeChildren(children []TemplateNode) error {
	for _, child := range children {
		if err := p.writeNode(child); err != nil {
			return err
		}
	}
	return nil
}

func (p *semanticTemplateProjector) writeDirective(node *DirectiveNode) error {
	if node == nil {
		return nil
	}
	switch node.Name {
	case "if":
		return p.writeIf(node)
	case "if-group":
		return p.writeIfGroup(node)
	case "for":
		return p.writeFor(node)
	case "repeat":
		return p.writeRepeat(node)
	case "switch":
		return p.writeSwitch(node)
	case "break":
		return p.generated("\tbreak\n", goxmap.SourceRange{})
	case "continue":
		return p.generated("\tcontinue\n", goxmap.SourceRange{})
	case "empty", "each", "else-if", "else", "case", "default":
		// Parent directives own the semantics of these structural blocks. If an
		// invalid standalone block reaches the projection, preserve expressions
		// and children without inventing a scope; the runtime validator remains
		// authoritative for the structural error.
		if err := p.writeAttributeExpressions(node.Attrs, nil); err != nil {
			return err
		}
		return p.writeChildren(node.Children)
	default:
		if err := p.writeAttributeExpressions(node.Attrs, nil); err != nil {
			return err
		}
		return p.writeChildren(node.Children)
	}
}

func (p *semanticTemplateProjector) writeIf(node *DirectiveNode) error {
	condition, ok := findTemplateAttr(node.Attrs, "condition")
	if !ok || !semanticSourceRangePresent(condition.ExprSource) {
		return p.writeDirectiveFallback(node, nil)
	}
	if err := p.generated("\tif ", condition.ExprSource); err != nil {
		return err
	}
	if err := p.writeIfAssignment(node); err != nil {
		return err
	}
	if err := p.exact(condition.ExprSource); err != nil {
		return err
	}
	if err := p.generated(" {\n", condition.ExprSource); err != nil {
		return err
	}
	if err := p.writeAttributeExpressions(node.Attrs, map[string]bool{"condition": true, ":assign": true}); err != nil {
		return err
	}
	if err := p.writeChildren(node.Children); err != nil {
		return err
	}
	return p.generated("\t}\n", condition.ExprSource)
}

func (p *semanticTemplateProjector) writeIfGroup(node *DirectiveNode) error {
	var wroteBranch bool
	for _, child := range node.Children {
		directive, ok := child.(*DirectiveNode)
		if !ok {
			if isIgnorableDirectiveChild(child) {
				continue
			}
			if err := p.writeNode(child); err != nil {
				return err
			}
			continue
		}

		switch directive.Name {
		case "if", "else-if":
			condition, ok := findTemplateAttr(directive.Attrs, "condition")
			if !ok || !semanticSourceRangePresent(condition.ExprSource) {
				return p.writeDirectiveFallback(node, nil)
			}
			prefix := "\tif "
			if wroteBranch {
				prefix = "\telse if "
			}
			if err := p.generated(prefix, condition.ExprSource); err != nil {
				return err
			}
			if err := p.writeIfAssignment(directive); err != nil {
				return err
			}
			if err := p.exact(condition.ExprSource); err != nil {
				return err
			}
			if err := p.generated(" {\n", condition.ExprSource); err != nil {
				return err
			}
			if err := p.writeAttributeExpressions(directive.Attrs, map[string]bool{"condition": true, ":assign": true}); err != nil {
				return err
			}
			if err := p.writeChildren(directive.Children); err != nil {
				return err
			}
			if err := p.generated("\t}", condition.ExprSource); err != nil {
				return err
			}
			wroteBranch = true

		case "else":
			if !wroteBranch {
				return p.writeDirectiveFallback(node, nil)
			}
			if err := p.generated(" else {\n", goxmap.SourceRange{}); err != nil {
				return err
			}
			if err := p.writeAttributeExpressions(directive.Attrs, nil); err != nil {
				return err
			}
			if err := p.writeChildren(directive.Children); err != nil {
				return err
			}
			if err := p.generated("\t}", goxmap.SourceRange{}); err != nil {
				return err
			}

		default:
			return p.writeDirectiveFallback(node, nil)
		}
	}
	if wroteBranch {
		return p.generated("\n", goxmap.SourceRange{})
	}
	return p.writeDirectiveFallback(node, nil)
}

func (p *semanticTemplateProjector) writeFor(node *DirectiveNode) error {
	rangeAttr, ok := findTemplateAttr(node.Attrs, "range")
	if !ok || !semanticSourceRangePresent(rangeAttr.ExprSource) {
		return p.writeDirectiveFallback(node, nil)
	}
	bindings := templateBooleanBindings(node.Attrs, "range")
	empty, each := templateForBranches(node.Children)
	advanced := empty != nil || each != nil

	if advanced && empty != nil && each != nil {
		// Runtime advanced-for first checks len(rangeExpr) and only ranges the
		// same expression in the non-empty branch. Keep the user expression
		// exact in the len check so Go diagnostics map to source; the range uses
		// a generated copy solely to infer binding types without giving one
		// source range two exact semantic identities.
		if err := p.generated("\tif len(", rangeAttr.ExprSource); err != nil {
			return err
		}
		if err := p.exact(rangeAttr.ExprSource); err != nil {
			return err
		}
		if err := p.generated(") == 0 {\n", rangeAttr.ExprSource); err != nil {
			return err
		}
		if err := p.writeChildren(empty.Children); err != nil {
			return err
		}
		if err := p.generated("\t} else {\n", rangeAttr.ExprSource); err != nil {
			return err
		}
		if err := p.writeRangeHeaderGeneratedExpression(bindings, rangeAttr.ExprSource); err != nil {
			return err
		}
		if err := p.writeChildren(each.Children); err != nil {
			return err
		}
		if err := p.generated("\t\t}\n\t}\n", rangeAttr.ExprSource); err != nil {
			return err
		}
		return p.writeAttributeExpressions(node.Attrs, map[string]bool{"range": true})
	}

	if err := p.writeRangeHeader(bindings, rangeAttr.ExprSource); err != nil {
		return err
	}
	if err := p.writeChildren(node.Children); err != nil {
		return err
	}
	if err := p.generated("\t}\n", rangeAttr.ExprSource); err != nil {
		return err
	}
	return p.writeAttributeExpressions(node.Attrs, map[string]bool{"range": true})
}

func (p *semanticTemplateProjector) writeRangeHeader(bindings []TemplateAttr, expression goxmap.SourceRange) error {
	if err := p.generated("\t\tfor ", expression); err != nil {
		return err
	}
	for index, binding := range bindings {
		if index > 0 {
			if err := p.generated(", ", binding.NameSource); err != nil {
				return err
			}
		}
		if err := p.exact(binding.NameSource); err != nil {
			return err
		}
	}
	if len(bindings) > 0 {
		if err := p.generated(" := range ", expression); err != nil {
			return err
		}
	} else if err := p.generated("range ", expression); err != nil {
		return err
	}
	if err := p.exact(expression); err != nil {
		return err
	}
	return p.generated(" {\n", expression)
}

func (p *semanticTemplateProjector) writeRangeHeaderGeneratedExpression(bindings []TemplateAttr, expression goxmap.SourceRange) error {
	if err := p.generated("\t\tfor ", expression); err != nil {
		return err
	}
	for index, binding := range bindings {
		if index > 0 {
			if err := p.generated(", ", binding.NameSource); err != nil {
				return err
			}
		}
		if err := p.exact(binding.NameSource); err != nil {
			return err
		}
	}
	if len(bindings) > 0 {
		if err := p.generated(" := range ", expression); err != nil {
			return err
		}
	} else if err := p.generated("range ", expression); err != nil {
		return err
	}
	if err := p.generatedSourceCopy(expression); err != nil {
		return err
	}
	return p.generated(" {\n", expression)
}

func (p *semanticTemplateProjector) writeRepeat(node *DirectiveNode) error {
	times, ok := findTemplateAttr(node.Attrs, "times")
	if !ok || !semanticSourceRangePresent(times.ExprSource) {
		return p.writeDirectiveFallback(node, nil)
	}
	bindings := templateBooleanBindings(node.Attrs, "times")
	if len(bindings) > 1 {
		return p.writeDirectiveFallback(node, map[string]bool{"times": true})
	}
	if err := p.generated("\tfor ", times.ExprSource); err != nil {
		return err
	}
	if len(bindings) == 1 {
		if err := p.exact(bindings[0].NameSource); err != nil {
			return err
		}
		if err := p.generated(" := range ", times.ExprSource); err != nil {
			return err
		}
	} else if err := p.generated("range ", times.ExprSource); err != nil {
		return err
	}
	if err := p.exact(times.ExprSource); err != nil {
		return err
	}
	if err := p.generated(" {\n", times.ExprSource); err != nil {
		return err
	}
	if err := p.writeChildren(node.Children); err != nil {
		return err
	}
	if err := p.generated("\t}\n", times.ExprSource); err != nil {
		return err
	}
	return p.writeAttributeExpressions(node.Attrs, map[string]bool{"times": true})
}

func (p *semanticTemplateProjector) writeSwitch(node *DirectiveNode) error {
	value, ok := findTemplateAttr(node.Attrs, "value")
	if !ok || !semanticSourceRangePresent(value.ExprSource) {
		return p.writeDirectiveFallback(node, nil)
	}
	if err := p.generated("\tswitch ", value.ExprSource); err != nil {
		return err
	}
	if err := p.exact(value.ExprSource); err != nil {
		return err
	}
	if err := p.generated(" {\n", value.ExprSource); err != nil {
		return err
	}
	if err := p.writeAttributeExpressions(node.Attrs, map[string]bool{"value": true}); err != nil {
		return err
	}

	for _, child := range node.Children {
		directive, ok := child.(*DirectiveNode)
		if !ok {
			if isIgnorableDirectiveChild(child) {
				continue
			}
			if err := p.writeNode(child); err != nil {
				return err
			}
			continue
		}
		switch directive.Name {
		case "case":
			isAttr, ok := findTemplateAttr(directive.Attrs, "is")
			if !ok || !semanticSourceRangePresent(isAttr.ExprSource) {
				return p.writeDirectiveFallback(node, nil)
			}
			if err := p.generated("\tcase ", isAttr.ExprSource); err != nil {
				return err
			}
			if err := p.exact(isAttr.ExprSource); err != nil {
				return err
			}
			if err := p.generated(":\n", isAttr.ExprSource); err != nil {
				return err
			}
			if err := p.writeAttributeExpressions(directive.Attrs, map[string]bool{"is": true}); err != nil {
				return err
			}
			if err := p.writeChildren(directive.Children); err != nil {
				return err
			}
			if hasFallthrough, _ := switchCaseFallthrough(directive); hasFallthrough {
				if err := p.generated("\t\tfallthrough\n", directiveSourceRange(directive)); err != nil {
					return err
				}
			}
		case "default":
			if err := p.generated("\tdefault:\n", directiveSourceRange(directive)); err != nil {
				return err
			}
			if err := p.writeAttributeExpressions(directive.Attrs, nil); err != nil {
				return err
			}
			if err := p.writeChildren(directive.Children); err != nil {
				return err
			}
		default:
			if err := p.writeNode(directive); err != nil {
				return err
			}
		}
	}
	return p.generated("\t}\n", value.ExprSource)
}

func (p *semanticTemplateProjector) writeDirectiveFallback(node *DirectiveNode, handled map[string]bool) error {
	if err := p.writeAttributeExpressions(node.Attrs, handled); err != nil {
		return err
	}
	return p.writeChildren(node.Children)
}

func (p *semanticTemplateProjector) writeAttributeExpressions(attrs []TemplateAttr, handled map[string]bool) error {
	for _, attr := range attrs {
		if handled != nil && handled[attr.Name] {
			continue
		}
		if semanticSourceRangePresent(attr.ExprSource) {
			if err := p.writeExpressionStatement(attr.ExprSource); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *semanticTemplateProjector) writeExpressionStatement(expression goxmap.SourceRange) error {
	if !semanticSourceRangePresent(expression) {
		return nil
	}
	if err := p.generated("\t_ = ", expression); err != nil {
		return err
	}
	if err := p.exact(expression); err != nil {
		return err
	}
	return p.generated("\n", expression)
}

func (p *semanticTemplateProjector) exact(source goxmap.SourceRange) error {
	input, err := semanticProjectionInputRange(p.upstream, p.sourceURI, source)
	if err != nil {
		return err
	}
	if !input.Valid() || input.Empty() || input.End > p.sourceLength {
		return fmt.Errorf("semantic template source range %v is outside projection input", input)
	}
	return p.writer.WriteSource(input)
}

func (p *semanticTemplateProjector) generatedSourceCopy(source goxmap.SourceRange) error {
	input, err := semanticProjectionInputRange(p.upstream, p.sourceURI, source)
	if err != nil {
		return err
	}
	if !input.Valid() || input.Empty() || input.End > len(p.source) {
		return fmt.Errorf("semantic template source range %v is outside projection input", input)
	}
	return p.writer.WriteGenerated(p.source[input.Start:input.End], input)
}

func (p *semanticTemplateProjector) generated(text string, source goxmap.SourceRange) error {
	anchor := p.templateRange
	if semanticSourceRangePresent(source) {
		if input, err := semanticProjectionInputRange(p.upstream, p.sourceURI, source); err == nil && input.Valid() && !input.Empty() {
			anchor = input
		}
	}
	return p.writer.WriteGenerated(text, anchor)
}

func semanticSourceRangePresent(source goxmap.SourceRange) bool {
	return source.URI != "" && source.Range.Valid() && !source.Range.Empty()
}

func directiveSourceRange(node *DirectiveNode) goxmap.SourceRange {
	if node == nil {
		return goxmap.SourceRange{}
	}
	for _, attr := range node.Attrs {
		if semanticSourceRangePresent(attr.NameSource) {
			return attr.NameSource
		}
	}
	return goxmap.SourceRange{}
}

func (p *semanticTemplateProjector) writeIfAssignment(node *DirectiveNode) error {
	init, bindings, err := templateIfAssignment(node)
	if err != nil || init == nil {
		return nil
	}
	attr, _ := findTemplateAttr(node.Attrs, ":assign")
	for i, b := range bindings {
		if i > 0 {
			if err := p.generated(", ", b.NameSource); err != nil {
				return err
			}
		}
		if err := p.exact(b.NameSource); err != nil {
			return err
		}
	}
	if err := p.generated(" := ", attr.ExprSource); err != nil {
		return err
	}
	if err := p.exact(attr.ExprSource); err != nil {
		return err
	}
	return p.generated("; ", attr.ExprSource)
}
