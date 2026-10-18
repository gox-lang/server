package gox

import (
	"go/ast"
	"go/token"
)

func (g *templateGenerator) generateRepeatDirective(n *DirectiveNode) error {
	timesAttr, ok := findTemplateAttr(n.Attrs, "times")
	if !ok {
		return newTemplateErrorWithHint("missing-required-attribute", n.Pos, "<:repeat> requires times attribute", "Use <:repeat times={{n}}>...</:repeat>.")
	}

	if timesAttr.Expr == nil {
		return newTemplateErrorWithHint("missing-required-expression", timesAttr.Pos, "<:repeat times={{...}}> requires Go expression", "Use a Go expression inside times={{...}}.")
	}

	idxName, err := parseRepeatIndexBinding(n)
	if err != nil {
		return err
	}

	g.flushText()
	g.emitLine(n.Pos)

	loopCtx := *g.ctx
	loopCtx.LoopDepth++
	loopCtx.TypeEnv = templateTypeEnvWithRepeatBinding(g.ctx.TypeEnv, idxName)

	body, err := generateDirectiveChildrenStmts(&loopCtx, n.Children)
	if err != nil {
		return err
	}

	rangeStmt := &ast.RangeStmt{
		X:    timesAttr.Expr,
		Body: &ast.BlockStmt{List: body},
	}

	if idxName != "" {
		rangeStmt.Key = ast.NewIdent(idxName)
		rangeStmt.Tok = token.DEFINE
	}

	g.stmts = append(g.stmts, rangeStmt)

	return nil
}

func parseRepeatIndexBinding(n *DirectiveNode) (string, error) {
	var bindings []string

	for _, attr := range n.Attrs {
		if attr.Name == "times" {
			continue
		}

		if attr.Kind != AttrBoolean {
			return "", newTemplateErrorfWithHint("invalid-repeat-binding", attr.Pos, "Use one bare identifier as the optional repeat index binding.", "<:repeat> binding %q must be a bare identifier", attr.Name)
		}

		bindings = append(bindings, attr.Name)
	}

	switch len(bindings) {
	case 0:
		return "", nil

	case 1:
		return bindings[0], nil

	default:
		return "", newTemplateErrorWithHint("too-many-repeat-bindings", n.Pos, "<:repeat> accepts at most one binding", "Use zero or one binding before times={{...}}.")
	}
}
