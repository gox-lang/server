package gox

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

func (g *templateGenerator) generateComponent(n *ComponentNode) error {
	g.flushText()
	g.emitLine(n.Pos)

	isValue := g.ctx.TypeEnv != nil && (g.ctx.TypeEnv.Vars[n.Name] == "gox.Component" || g.ctx.TypeEnv.ComponentValues[n.Name])
	var contract ComponentContract
	var hasContract bool
	if g.ctx.TypeEnv != nil && !isValue {
		contract, hasContract = g.ctx.TypeEnv.Components[n.Name]
	}
	if g.ctx.TypeEnv != nil && g.ctx.TypeEnv.StrictComponents {
		if isValue && len(n.Attrs) > 0 {
			return newTemplateErrorfWithHint("inline-component-does-not-accept-props", n.Pos, "Render component values without attributes.", "component value %s does not accept props", n.Name)
		}
		if hasContract {
			if err := validateLocalComponentInvocation(n, g.ctx.TypeEnv.Components); err != nil {
				return err
			}
		}
	}
	if isValue || (len(n.Children) > 0 && isSimpleComponentIdent(n.Name) && !hasContract) {
		ifStmt, ok, err := g.tryGenerateComponentFallback(n)
		if err != nil {
			return err
		}

		if ok {
			g.flushText()
			g.stmts = append(g.stmts, ifStmt)
			return nil
		}
	}

	call := &ast.CallExpr{
		Fun: componentNameToExpr(n.Name),
		Args: []ast.Expr{
			dottedIdent(g.ctx.WriterName),
		},
	}

	attrs := n.Attrs
	if hasContract {
		attrs = nil
		for _, prop := range contract.Props {
			for _, attr := range n.Attrs {
				if attr.Name == prop.Name {
					attrs = append(attrs, attr)
					break
				}
			}
		}
	}
	for _, attr := range attrs {
		if attr.Kind == AttrSpecial {
			return newTemplateErrorfWithHint("unsupported-component-attribute", attr.Pos, "Remove the special attribute or use it on an HTML element/directive that supports it.", "special attribute %q is not supported on component <&%s>", attr.Name, n.Name)
		}

		if attr.Kind == AttrBoolean {
			return newTemplateErrorfWithHint("component-prop-requires-value", attr.Pos, "Use prop=\"value\" or prop={{expr}}.", "component prop %q requires value", attr.Name)
		}

		if attr.Expr != nil {
			call.Args = append(call.Args, attr.Expr)
			continue
		}

		if attr.Kind == AttrStatic {
			call.Args = append(call.Args, &ast.BasicLit{
				Kind:  token.STRING,
				Value: strconv.Quote(attr.Value),
			})
			continue
		}

		return newTemplateErrorfWithHint("invalid-component-prop", attr.Pos, "Use a static value or a Go expression for component props.", "invalid component prop %q", attr.Name)
	}

	if len(n.Children) > 0 {
		childrenFn, err := g.buildChildrenFunc(n.Children)
		if err != nil {
			return err
		}

		call.Args = append(call.Args, childrenFn)
	} else if hasContract && contract.Children != nil {
		call.Args = append(call.Args, ast.NewIdent("nil"))
	}
	if !isSimpleComponentIdent(n.Name) && !hasContract && g.ctx.TypeEnv != nil && g.ctx.TypeEnv.ComponentCalls != nil {
		*g.ctx.TypeEnv.ComponentCalls = append(*g.ctx.TypeEnv.ComponentCalls, pendingComponentCall{Node: n, Call: call})
	}

	g.stmts = append(g.stmts, &ast.ExprStmt{X: call})

	return nil
}

func (g *templateGenerator) buildChildrenFunc(children []TemplateNode) (ast.Expr, error) {
	childCtx := *g.ctx
	childCtx.WriterName = "goxWriter"

	childGen := templateGenerator{
		ctx: &childCtx,
	}

	for _, child := range children {
		if err := childGen.generateNode(child); err != nil {
			return nil, err
		}
	}

	childGen.flushText()
	childGen.resetLine()

	return &ast.FuncLit{
		Type: &ast.FuncType{
			Params: &ast.FieldList{
				List: []*ast.Field{
					{
						Names: []*ast.Ident{
							ast.NewIdent("goxWriter"),
						},
						Type: &ast.SelectorExpr{
							X:   ast.NewIdent("gox"),
							Sel: ast.NewIdent("Writer"),
						},
					},
				},
			},
		},
		Body: &ast.BlockStmt{
			List: childGen.stmts,
		},
	}, nil
}

func (g *templateGenerator) tryGenerateComponentFallback(n *ComponentNode) (*ast.IfStmt, bool, error) {
	// solo aplica a invocaciones sin props:
	// <&children>fallback</&children>
	if len(n.Attrs) > 0 {
		return nil, false, nil
	}

	componentIdent := ast.NewIdent(n.Name)

	fallbackStmts, err := generateDirectiveChildrenStmts(g.ctx, n.Children)
	if err != nil {
		return nil, false, err
	}

	return &ast.IfStmt{
		Cond: &ast.BinaryExpr{
			X:  componentIdent,
			Op: token.NEQ,
			Y:  ast.NewIdent("nil"),
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{
					X: &ast.CallExpr{
						Fun: componentIdent,
						Args: []ast.Expr{
							dottedIdent(g.ctx.WriterName),
						},
					},
				},
			},
		},
		Else: &ast.BlockStmt{
			List: fallbackStmts,
		},
	}, true, nil
}

func componentNameToExpr(name string) ast.Expr {
	parts := strings.Split(name, ".")

	var expr ast.Expr = ast.NewIdent(parts[0])

	for i := 1; i < len(parts); i++ {
		expr = &ast.SelectorExpr{
			X:   expr,
			Sel: ast.NewIdent(parts[i]),
		}
	}

	return expr
}

func isSimpleComponentIdent(name string) bool {
	return !strings.Contains(name, ".")
}
