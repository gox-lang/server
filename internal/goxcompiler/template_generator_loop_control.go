package gox

import (
	"go/ast"
	"go/token"
)

func (g *templateGenerator) generateBreakDirective(n *DirectiveNode) error {
	if g.ctx == nil || g.ctx.LoopDepth <= 0 {
		return newTemplateErrorWithHint("invalid-break-outside-loop", n.Pos, "<:break /> can only be used inside <:for>, <:each>, or <:repeat>", "Move <:break /> inside a loop directive or remove it.")
	}

	if hasMeaningfulDirectiveChildren(n.Children) {
		return newTemplateErrorWithHint("invalid-directive-children", n.Pos, "<:break /> cannot have children", "Use <:break /> as a self-closing directive.")
	}

	g.flushText()
	g.emitLine(n.Pos)

	g.stmts = append(g.stmts, &ast.BranchStmt{
		Tok: token.BREAK,
	})

	return nil
}

func (g *templateGenerator) generateContinueDirective(n *DirectiveNode) error {
	if g.ctx == nil || g.ctx.LoopDepth <= 0 {
		return newTemplateErrorWithHint("invalid-continue-outside-loop", n.Pos, "<:continue /> can only be used inside <:for>, <:each>, or <:repeat>", "Move <:continue /> inside a loop directive or remove it.")
	}

	if hasMeaningfulDirectiveChildren(n.Children) {
		return newTemplateErrorWithHint("invalid-directive-children", n.Pos, "<:continue /> cannot have children", "Use <:continue /> as a self-closing directive.")
	}

	g.flushText()
	g.emitLine(n.Pos)

	g.stmts = append(g.stmts, &ast.BranchStmt{
		Tok: token.CONTINUE,
	})

	return nil
}
