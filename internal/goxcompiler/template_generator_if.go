package gox

import "go/ast"

func (g *templateGenerator) generateIfDirective(n *DirectiveNode) error {
	conditionAttr, ok := findTemplateAttr(n.Attrs, "condition")
	if !ok {
		return newTemplateErrorWithHint("missing-required-attribute", n.Pos, "<:if> requires condition attribute", "Use <:if condition={{expr}}>...</:if>.")
	}

	if conditionAttr.Expr == nil {
		return newTemplateErrorWithHint("missing-required-expression", conditionAttr.Pos, "<:if condition={{...}}> requires Go expression", "Use a Go expression inside the condition attribute, for example condition={{user != nil}}.")
	}

	init, bindings, err := templateIfAssignment(n)
	if err != nil {
		return err
	}
	g.flushText()
	g.emitLine(n.Pos)

	childGen := templateGenerator{
		ctx: templateIfContext(g.ctx, bindings),
	}

	for _, child := range n.Children {
		if err := childGen.generateNode(child); err != nil {
			return err
		}
	}

	childGen.flushText()

	stmt := &ast.IfStmt{
		Cond: conditionAttr.Expr,
		Body: &ast.BlockStmt{
			List: childGen.stmts,
		},
	}
	if init != nil {
		stmt.Init = init
	}
	g.stmts = append(g.stmts, stmt)

	return nil
}
