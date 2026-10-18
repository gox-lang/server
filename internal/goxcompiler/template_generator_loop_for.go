package gox

import (
	"go/ast"
	"go/token"
	"strconv"
)

func (g *templateGenerator) generateForDirective(n *DirectiveNode) error {
	rangeAttr, ok := findTemplateAttr(n.Attrs, "range")
	if !ok {
		return newTemplateErrorWithHint("missing-required-attribute", n.Pos, "<:for> requires range attribute", "Use <:for range={{items}}>...</:for>.")
	}

	if rangeAttr.Expr == nil {
		return newTemplateErrorWithHint("missing-required-expression", rangeAttr.Pos, "<:for range={{...}}> requires Go expression", "Use a Go expression inside range={{...}}.")
	}

	key, value, err := parseForBindings(n)
	if err != nil {
		return err
	}

	g.flushText()
	g.emitLine(n.Pos)

	emptyNode, eachNode, advancedMode, err := parseForSpecialBlocks(n)
	if err != nil {
		return err
	}

	loopCtx := *g.ctx
	loopCtx.LoopDepth++
	loopCtx.TypeEnv = templateTypeEnvWithRangeBindings(g.ctx.TypeEnv, rangeAttr.Expr, key, value)

	// modo corto
	if !advancedMode {
		body, err := generateDirectiveChildrenStmts(&loopCtx, n.Children)
		if err != nil {
			return err
		}

		g.stmts = append(g.stmts, &ast.RangeStmt{
			Key:   key,
			Value: value,
			Tok:   token.DEFINE,
			X:     rangeAttr.Expr,
			Body:  &ast.BlockStmt{List: body},
		})

		return nil
	}

	// modo avanzado
	emptyBody, err := generateDirectiveChildrenStmts(g.ctx, emptyNode.Children)
	if err != nil {
		return err
	}

	eachBody, err := generateDirectiveChildrenStmts(&loopCtx, eachNode.Children)
	if err != nil {
		return err
	}

	rangeStmt := &ast.RangeStmt{
		Key:   key,
		Value: value,
		Tok:   token.DEFINE,
		X:     rangeAttr.Expr,
		Body: &ast.BlockStmt{
			List: eachBody,
		},
	}

	// Capture once per entry to this loop. Choose the name after generating
	// nested bodies so it cannot shadow their temporaries or user references.
	emptyBlock := &ast.BlockStmt{List: emptyBody}
	rangeName := freshForRangeName(rangeStmt, emptyBlock)
	rangeStmt.X = ast.NewIdent(rangeName)
	g.stmts = append(g.stmts, &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(rangeName)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{rangeAttr.Expr},
		},
		Cond: buildLenEqualsZeroExpr(ast.NewIdent(rangeName)),
		Body: &ast.BlockStmt{
			List: emptyBody,
		},
		Else: &ast.BlockStmt{
			List: []ast.Stmt{
				rangeStmt,
			},
		},
	})

	return nil
}

func freshForRangeName(nodes ...ast.Node) string {
	used := make(map[string]bool)
	for _, node := range nodes {
		ast.Inspect(node, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok {
				used[ident.Name] = true
			}
			return true
		})
	}
	for index := 1; ; index++ {
		name := "goxRange" + strconv.Itoa(index)
		if !used[name] {
			return name
		}
	}
}

func parseForBindings(n *DirectiveNode) (ast.Expr, ast.Expr, error) {
	var bindings []string

	for _, attr := range n.Attrs {
		if attr.Name == "range" {
			continue
		}

		if attr.Kind != AttrBoolean {
			return nil, nil, newTemplateErrorfWithHint("invalid-loop-binding", attr.Pos, "Use bare identifiers as bindings, for example <:for index item range={{items}}>.", "<:for> binding %q must be a bare identifier", attr.Name)
		}

		bindings = append(bindings, attr.Name)
	}

	switch len(bindings) {
	case 0:
		return nil, nil, nil

	case 1:
		return ast.NewIdent(bindings[0]), nil, nil

	case 2:
		return ast.NewIdent(bindings[0]), ast.NewIdent(bindings[1]), nil

	default:
		return nil, nil, newTemplateErrorWithHint("too-many-loop-bindings", n.Pos, "<:for> accepts at most two bindings", "Use zero, one, or two bindings before range={{...}}.")
	}
}

func parseForSpecialBlocks(
	n *DirectiveNode,
) (
	emptyNode *DirectiveNode,
	eachNode *DirectiveNode,
	advanced bool,
	err error,
) {
	var meaningful []TemplateNode

	for _, child := range n.Children {
		if isIgnorableDirectiveChild(child) {
			continue
		}

		meaningful = append(meaningful, child)
	}

	hasEmpty := false
	hasEach := false

	for _, child := range meaningful {
		dir, ok := child.(*DirectiveNode)
		if !ok {
			continue
		}

		switch dir.Name {
		case "empty":
			if hasEmpty {
				return nil, nil, false,
					newTemplateErrorWithHint("duplicate-empty-block", dir.Pos, "<:for> can only contain one <:empty>", "Remove the extra <:empty> block.")
			}

			hasEmpty = true
			emptyNode = dir

		case "each":
			if hasEach {
				return nil, nil, false,
					newTemplateErrorWithHint("duplicate-each-block", dir.Pos, "<:for> can only contain one <:each>", "Remove the extra <:each> block.")
			}

			hasEach = true
			eachNode = dir
		}
	}

	advanced = hasEmpty || hasEach

	if !advanced {
		return nil, nil, false, nil
	}

	if !hasEmpty {
		return nil, nil, false,
			newTemplateErrorWithHint("missing-empty-block", n.Pos, "<:for> advanced mode requires <:empty>", "Add <:empty> before <:each>, or remove <:each> to use simple mode.")
	}

	if !hasEach {
		return nil, nil, false,
			newTemplateErrorWithHint("missing-each-block", n.Pos, "<:for> advanced mode requires <:each>", "Add <:each> after <:empty>, or remove <:empty> to use simple mode.")
	}

	if len(meaningful) != 2 {
		return nil, nil, false,
			newTemplateErrorWithHint("invalid-for-advanced-child", n.Pos, "<:for> advanced mode only allows <:empty> and <:each>", "Move other content inside <:empty> or <:each>.")
	}

	first, ok := meaningful[0].(*DirectiveNode)
	if !ok {
		return nil, nil, false,
			newTemplateErrorWithHint("invalid-for-advanced-child", n.Pos, "<:for> advanced mode only allows <:empty> and <:each>", "Move other content inside <:empty> or <:each>.")
	}

	second, ok := meaningful[1].(*DirectiveNode)
	if !ok {
		return nil, nil, false,
			newTemplateErrorWithHint("invalid-for-advanced-child", n.Pos, "<:for> advanced mode only allows <:empty> and <:each>", "Move other content inside <:empty> or <:each>.")
	}

	if first.Name != "empty" || second.Name != "each" {
		return nil, nil, false,
			newTemplateErrorWithHint("invalid-for-block-order", n.Pos, "<:for> requires <:empty> before <:each>", "Place <:empty> before <:each>.")
	}

	return emptyNode, eachNode, true, nil
}

func buildLenEqualsZeroExpr(expr ast.Expr) ast.Expr {
	return &ast.BinaryExpr{
		X: &ast.CallExpr{
			Fun: ast.NewIdent("len"),
			Args: []ast.Expr{
				expr,
			},
		},
		Op: token.EQL,
		Y: &ast.BasicLit{
			Kind:  token.INT,
			Value: "0",
		},
	}
}
