package gox

import (
	"go/ast"
	"go/parser"
	"go/token"
)

func (g *templateGenerator) generateSwitchDirective(n *DirectiveNode) error {
	valueExpr, err := requiredDirectiveExpr(n, "value")
	if err != nil {
		return err
	}

	branches, err := validateSwitchBranches(n)
	if err != nil {
		return err
	}

	g.flushText()
	g.emitLine(n.Pos)

	switchStmt := &ast.SwitchStmt{
		Tag: valueExpr,
	}

	cases := make([]ast.Stmt, 0, len(branches))

	for _, dir := range branches {
		switch dir.Name {
		case "case":
			caseClause, err := generateSwitchCaseClause(g.ctx, dir)
			if err != nil {
				return err
			}

			cases = append(cases, caseClause)

		case "default":
			defaultClause, err := generateSwitchDefaultClause(g.ctx, dir)
			if err != nil {
				return err
			}

			cases = append(cases, defaultClause)
		}
	}

	switchStmt.Body = &ast.BlockStmt{
		List: cases,
	}

	g.stmts = append(g.stmts, switchStmt)

	return nil
}

func validateSwitchBranches(n *DirectiveNode) ([]*DirectiveNode, error) {
	var branches []*DirectiveNode

	for _, child := range n.Children {
		dir, ok := child.(*DirectiveNode)
		if !ok {
			if isIgnorableDirectiveChild(child) {
				continue
			}

			return nil, newTemplateErrorWithHint("invalid-switch-child", templateNodePosition(child), "<:switch> only accepts <:case> and <:default>", "Move this content inside a <:case> or <:default> block.")
		}

		switch dir.Name {
		case "case", "default":
			branches = append(branches, dir)
		default:
			return nil, newTemplateErrorfWithHint("invalid-switch-child", dir.Pos, "Only <:case> and <:default> are allowed inside <:switch>.", "<:switch> invalid child <:%s>", dir.Name)
		}
	}

	if len(branches) == 0 {
		return nil, newTemplateErrorWithHint("empty-switch", n.Pos, "<:switch> requires at least one <:case> or <:default>", "Add a <:case is={{...}}> or <:default> block.")
	}

	defaultIndex := -1
	for i, dir := range branches {
		switch dir.Name {
		case "case":
			hasFallthrough, err := switchCaseFallthrough(dir)
			if err != nil {
				return nil, err
			}
			if hasFallthrough && i == len(branches)-1 {
				return nil, newTemplateErrorWithHint("fallthrough-last-case", dir.Pos, "<:case fallthrough> requires a following switch branch", "Remove fallthrough or add another <:case> or <:default> after this case.")
			}

		case "default":
			if defaultIndex >= 0 {
				return nil, newTemplateErrorWithHint("duplicate-default-block", dir.Pos, "<:switch> can only contain one <:default>", "Remove the extra <:default> block.")
			}
			defaultIndex = i

			if _, ok := findTemplateAttr(dir.Attrs, "fallthrough"); ok {
				return nil, newTemplateErrorWithHint("fallthrough-on-default", dir.Pos, "fallthrough is only valid on <:case>", "Remove fallthrough from <:default>.")
			}
		}
	}

	if defaultIndex >= 0 && defaultIndex != len(branches)-1 {
		return nil, newTemplateErrorWithHint("default-not-last", branches[defaultIndex].Pos, "<:default> must be the last branch in <:switch>", "Move <:default> after all <:case> blocks.")
	}

	return branches, nil
}

func switchCaseFallthrough(n *DirectiveNode) (bool, error) {
	var found bool

	for _, attr := range n.Attrs {
		if attr.Name != "fallthrough" {
			continue
		}

		if found {
			return false, newTemplateErrorWithHint("duplicate-fallthrough-attribute", attr.Pos, "<:case> can only declare fallthrough once", "Remove the duplicate fallthrough attribute.")
		}
		found = true

		if attr.Kind != AttrBoolean {
			return false, newTemplateErrorWithHint("invalid-fallthrough-attribute", attr.Pos, "fallthrough must be a bare boolean attribute", "Use <:case is={{...}} fallthrough>...</:case>.")
		}
	}

	return found, nil
}

func generateSwitchCaseClause(ctx *TemplateGenContext, n *DirectiveNode) (*ast.CaseClause, error) {
	attr, ok := findTemplateAttr(n.Attrs, "is")
	if !ok {
		return nil, newTemplateErrorWithHint("missing-required-attribute", n.Pos, "<:case> requires is attribute", "Use <:case is={value}>...</:case>.")
	}

	if attr.Raw == "" {
		return nil, newTemplateErrorWithHint("missing-required-expression", attr.Pos, "<:case is={{...}}> requires expression", "Use a Go expression or expression list inside is={{...}}.")
	}

	exprs, err := parseCaseExprList(attr.Raw)
	if err != nil {
		return nil, err
	}

	body, err := generateDirectiveChildrenStmts(ctx, n.Children)
	if err != nil {
		return nil, err
	}

	hasFallthrough, err := switchCaseFallthrough(n)
	if err != nil {
		return nil, err
	}
	if hasFallthrough {
		body = append(body, &ast.BranchStmt{Tok: token.FALLTHROUGH})
	}

	return &ast.CaseClause{
		List: exprs,
		Body: body,
	}, nil
}

func generateSwitchDefaultClause(
	ctx *TemplateGenContext,
	n *DirectiveNode,
) (*ast.CaseClause, error) {
	body, err := generateDirectiveChildrenStmts(ctx, n.Children)
	if err != nil {
		return nil, err
	}

	return &ast.CaseClause{
		Body: body,
	}, nil
}

func parseCaseExprList(src string) ([]ast.Expr, error) {
	fileSrc := "package main\nvar _ = []any{" + src + "}"

	file, err := parser.ParseFile(
		tokenFileSet,
		"",
		fileSrc,
		0,
	)
	if err != nil {
		return nil, newTemplateErrorWithHint("invalid-case-expression-list", TemplatePosition{}, "invalid <:case is={{...}}> expression list: "+err.Error(), "Use one or more valid Go expressions separated by commas.")
	}

	decl := file.Decls[0].(*ast.GenDecl)
	spec := decl.Specs[0].(*ast.ValueSpec)

	composite := spec.Values[0].(*ast.CompositeLit)

	var exprs []ast.Expr

	exprs = append(exprs, composite.Elts...)

	return exprs, nil
}
