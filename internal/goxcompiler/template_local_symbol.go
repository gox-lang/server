package gox

import (
	"go/ast"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

type templateDirectiveBounds struct {
	contentStart int
	contentEnd   int
}

type templateDirectiveFrame struct {
	name       string
	nameOffset int
	content    int
}

func collectTemplateLocalSymbols(source string, file *ast.File, templates []TemplateReturn) []LocalSymbol {
	if file == nil || len(templates) == 0 {
		return nil
	}
	base := buildTemplateBaseTypeEnv(file)
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name != nil {
			functions[function.Name.Name] = function
		}
	}
	var symbols []LocalSymbol
	for _, template := range templates {
		if template.Template == nil || template.Template.Root == nil {
			continue
		}
		function := functions[template.FuncName]
		if function == nil {
			continue
		}
		bounds, err := templateDirectiveRanges(template)
		if err != nil {
			continue
		}
		env := templateTypeEnvForFunc(function, base)
		collectTemplateNodeLocalSymbols(source, template.Template.Root, env, bounds, &symbols)
	}
	return symbols
}

func collectTemplateNodeLocalSymbols(source string, node TemplateNode, env *TemplateTypeEnv, bounds map[int]templateDirectiveBounds, output *[]LocalSymbol) {
	switch value := node.(type) {
	case *FragmentNode:
		collectTemplateChildrenLocalSymbols(source, value.Children, env, bounds, output)
	case *ElementNode:
		collectTemplateChildrenLocalSymbols(source, value.Children, env, bounds, output)
	case *ComponentNode:
		collectTemplateChildrenLocalSymbols(source, value.Children, env, bounds, output)
	case *ClientNode:
		collectTemplateChildrenLocalSymbols(source, value.Children, env, bounds, output)
	case *DirectiveNode:
		collectDirectiveLocalSymbols(source, value, env, bounds, output)
	}
}

func collectTemplateChildrenLocalSymbols(source string, children []TemplateNode, env *TemplateTypeEnv, bounds map[int]templateDirectiveBounds, output *[]LocalSymbol) {
	for _, child := range children {
		collectTemplateNodeLocalSymbols(source, child, env, bounds, output)
	}
}

func collectDirectiveLocalSymbols(source string, directive *DirectiveNode, env *TemplateTypeEnv, bounds map[int]templateDirectiveBounds, output *[]LocalSymbol) {
	if directive == nil {
		return
	}
	nameOffset, ok := templatePositionOffset(source, directive.Pos)
	if !ok {
		return
	}
	directiveBounds, hasBounds := bounds[nameOffset]
	switch directive.Name {
	case "if-group":
		branchEnv := env
		for _, child := range directive.Children {
			branch, ok := child.(*DirectiveNode)
			if !ok {
				continue
			}
			if branch.Name == "if" || branch.Name == "else-if" {
				branchEnv = collectIfAssignmentSymbols(source, branch, branchEnv, directiveBounds, hasBounds, output)
			}
			collectTemplateChildrenLocalSymbols(source, branch.Children, branchEnv, bounds, output)
		}
	case "if":
		branchEnv := collectIfAssignmentSymbols(source, directive, env, directiveBounds, hasBounds, output)
		collectTemplateChildrenLocalSymbols(source, directive.Children, branchEnv, bounds, output)
	case "for":
		rangeAttribute, hasRange := findTemplateAttr(directive.Attrs, "range")
		if !hasRange || rangeAttribute.Expr == nil {
			return
		}
		bindings := templateBooleanBindings(directive.Attrs, "range")
		keyType, valueType := templateRangeBindingTypes(env.ExprType(rangeAttribute.Expr))
		loopEnv := env.Clone()
		if len(bindings) > 0 && bindings[0].Name != "_" {
			loopEnv.Vars[bindings[0].Name] = keyType
		}
		if len(bindings) > 1 && bindings[1].Name != "_" {
			loopEnv.Vars[bindings[1].Name] = valueType
		}

		empty, each := templateForBranches(directive.Children)
		if empty != nil || each != nil {
			if empty != nil {
				collectTemplateChildrenLocalSymbols(source, empty.Children, env, bounds, output)
			}
			if each != nil {
				eachOffset, eachOK := templatePositionOffset(source, each.Pos)
				eachBounds, scopeOK := bounds[eachOffset]
				if eachOK && scopeOK {
					appendTemplateBindings(source, bindings, []string{keyType, valueType}, eachBounds, output)
				}
				collectTemplateChildrenLocalSymbols(source, each.Children, loopEnv, bounds, output)
			}
			return
		}
		if hasBounds {
			appendTemplateBindings(source, bindings, []string{keyType, valueType}, directiveBounds, output)
		}
		collectTemplateChildrenLocalSymbols(source, directive.Children, loopEnv, bounds, output)

	case "repeat":
		bindings := templateBooleanBindings(directive.Attrs, "times")
		repeatEnv := env.Clone()
		if len(bindings) > 0 && bindings[0].Name != "_" {
			repeatEnv.Vars[bindings[0].Name] = "int"
		}
		if hasBounds {
			appendTemplateBindings(source, bindings, []string{"int"}, directiveBounds, output)
		}
		collectTemplateChildrenLocalSymbols(source, directive.Children, repeatEnv, bounds, output)

	default:
		collectTemplateChildrenLocalSymbols(source, directive.Children, env, bounds, output)
	}
}

func appendTemplateBindings(source string, bindings []TemplateAttr, types []string, bounds templateDirectiveBounds, output *[]LocalSymbol) {
	if bounds.contentStart > bounds.contentEnd {
		return
	}
	for index, binding := range bindings {
		if binding.Name == "" || binding.Name == "_" || index >= len(types) {
			continue
		}
		start, ok := templatePositionOffset(source, binding.Pos)
		if !ok {
			continue
		}
		*output = append(*output, LocalSymbol{
			Name: binding.Name,
			Kind: SymbolVariable,
			Source: goxmap.SourceRange{
				URI:   binding.Pos.File,
				Range: goxmap.Range{Start: start, End: start + len(binding.Name)},
			},
			Scope: goxmap.SourceRange{
				URI:   binding.Pos.File,
				Range: goxmap.Range{Start: bounds.contentStart, End: bounds.contentEnd},
			},
			Type:   types[index],
			Detail: "var " + binding.Name + " " + types[index],
		})
	}
}

func templateBooleanBindings(attributes []TemplateAttr, reserved string) []TemplateAttr {
	bindings := make([]TemplateAttr, 0, 2)
	for _, attribute := range attributes {
		if attribute.Name != reserved && attribute.Kind == AttrBoolean {
			bindings = append(bindings, attribute)
		}
	}
	return bindings
}

func templateForBranches(children []TemplateNode) (empty, each *DirectiveNode) {
	for _, child := range children {
		directive, ok := child.(*DirectiveNode)
		if !ok {
			continue
		}
		switch directive.Name {
		case "empty":
			empty = directive
		case "each":
			each = directive
		}
	}
	return empty, each
}

func templateDirectiveRanges(template TemplateReturn) (map[int]templateDirectiveBounds, error) {
	lexer := NewTemplateLexer(template.Raw)
	tokens, err := lexer.LexAll()
	if err != nil {
		return nil, err
	}
	result := make(map[int]templateDirectiveBounds)
	var stack []templateDirectiveFrame
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		switch token.Kind {
		case TokenDirectiveTagStart:
			if index+1 >= len(tokens) || tokens[index+1].Kind != TokenIdent {
				continue
			}
			nameToken := tokens[index+1]
			endIndex := nextTemplateTagEnd(tokens, index+2)
			if endIndex < 0 || tokens[endIndex].Kind == TokenSelfCloseEnd {
				continue
			}
			nameOffset := template.Start + templateTokenOffset(template.Raw, nameToken)
			content := template.Start + templateTokenOffset(template.Raw, tokens[endIndex]) + len(tokens[endIndex].Lit)
			stack = append(stack, templateDirectiveFrame{name: nameToken.Lit, nameOffset: nameOffset, content: content})

		case TokenCloseTagStart:
			if index+1 >= len(tokens) || tokens[index+1].Kind != TokenIdent || len(stack) == 0 {
				continue
			}
			name := strings.TrimPrefix(tokens[index+1].Lit, ":")
			frame := stack[len(stack)-1]
			if frame.name != name {
				continue
			}
			stack = stack[:len(stack)-1]
			result[frame.nameOffset] = templateDirectiveBounds{
				contentStart: frame.content,
				contentEnd:   template.Start + templateTokenOffset(template.Raw, token),
			}
		}
	}
	return result, nil
}

func nextTemplateTagEnd(tokens []TemplateToken, start int) int {
	for index := start; index < len(tokens); index++ {
		if tokens[index].Kind == TokenTagEnd || tokens[index].Kind == TokenSelfCloseEnd {
			return index
		}
	}
	return -1
}

func templateTokenOffset(source string, token TemplateToken) int {
	offset, _ := lineColumnOffset(source, token.Line, token.Col)
	return offset
}

func templatePositionOffset(source string, position TemplatePosition) (int, bool) {
	return lineColumnOffset(source, position.Line, position.Column)
}

func lineColumnOffset(source string, line, column int) (int, bool) {
	if line < 1 || column < 1 {
		return 0, false
	}
	offset := 0
	currentLine := 1
	for currentLine < line {
		next := strings.IndexByte(source[offset:], '\n')
		if next < 0 {
			return 0, false
		}
		offset += next + 1
		currentLine++
	}
	offset += column - 1
	if offset < 0 || offset > len(source) {
		return 0, false
	}
	return offset, true
}

func collectIfAssignmentSymbols(source string, directive *DirectiveNode, env *TemplateTypeEnv, bounds templateDirectiveBounds, hasBounds bool, output *[]LocalSymbol) *TemplateTypeEnv {
	init, bindings, err := templateIfAssignment(directive)
	if err != nil || init == nil {
		return env
	}
	ctx := templateIfContext(&TemplateGenContext{TypeEnv: env}, bindings)
	if hasBounds {
		assignment, _ := findTemplateAttr(directive.Attrs, ":assign")
		bounds.contentStart = assignment.ExprSource.Range.End
		types := make([]string, len(bindings))
		for i := range types {
			types[i] = "any"
		}
		appendTemplateBindings(source, bindings, types, bounds, output)
	}
	return ctx.TypeEnv
}
