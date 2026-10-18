package gox

import (
	"go/ast"
)

func (g *templateGenerator) generateIfGroupDirective(n *DirectiveNode) error {
	if len(n.Children) == 0 {
		return newTemplateErrorWithHint("missing-if-group-children", n.Pos, "<:if-group> requires children", "Add <:if>, optional <:else-if>, and optional <:else> children.")
	}

	g.flushText()
	g.emitLine(n.Pos)

	ifStmt, err := buildIfGroupAST(g.ctx, n.Children)
	if err != nil {
		return err
	}

	g.stmts = append(g.stmts, ifStmt)

	return nil
}

func buildIfGroupAST(
	ctx *TemplateGenContext,
	children []TemplateNode,
) (*ast.IfStmt, error) {
	var root *ast.IfStmt
	var current *ast.IfStmt
	var hasElse bool

	for _, child := range children {
		dir, ok := child.(*DirectiveNode)
		if !ok {
			if isIgnorableDirectiveChild(child) {
				continue
			}

			return nil, newTemplateErrorWithHint("invalid-if-group-child", templateNodePosition(child), "<:if-group> only accepts <:if>, <:else-if>, <:else> children", "Move this content inside an allowed if-group directive.")
		}

		switch dir.Name {
		case "if":
			if root != nil {
				return nil, newTemplateErrorWithHint("duplicate-if-group-if", dir.Pos, "<:if-group> can only contain one initial <:if>", "Use <:else-if> for additional conditions.")
			}
			// if i != 0 {
			// no es perfecto porque puede haber whitespace antes, pero ayuda
			// la validación fuerte real la da root == nil
			// }

			cond, err := requiredDirectiveExpr(dir, "condition")
			if err != nil {
				return nil, err
			}

			init, bindings, err := templateIfAssignment(dir)
			if err != nil {
				return nil, err
			}
			ctx = templateIfContext(ctx, bindings)
			body, err := generateDirectiveChildrenStmts(ctx, dir.Children)
			if err != nil {
				return nil, err
			}

			root = &ast.IfStmt{
				Cond: cond,
				Body: &ast.BlockStmt{List: body},
			}

			if init != nil {
				root.Init = init
			}
			current = root

		case "else-if":
			if root == nil {
				return nil, newTemplateErrorWithHint("else-if-without-if", dir.Pos, "<:else-if> requires previous <:if> inside <:if-group>", "Start the group with <:if condition={{...}}>.")
			}
			if hasElse {
				return nil, newTemplateErrorWithHint("else-if-after-else", dir.Pos, "<:else-if> cannot appear after <:else>", "Place all <:else-if> blocks before <:else>.")
			}

			cond, err := requiredDirectiveExpr(dir, "condition")
			if err != nil {
				return nil, err
			}

			init, bindings, err := templateIfAssignment(dir)
			if err != nil {
				return nil, err
			}
			ctx = templateIfContext(ctx, bindings)
			body, err := generateDirectiveChildrenStmts(ctx, dir.Children)
			if err != nil {
				return nil, err
			}

			next := &ast.IfStmt{
				Cond: cond,
				Body: &ast.BlockStmt{List: body},
			}

			if init != nil {
				next.Init = init
			}
			current.Else = next
			current = next

		case "else":
			if len(dir.Attrs) != 0 {
				return nil, newTemplateErrorWithHint("invalid-else-attribute", dir.Pos, "<:else> does not accept attributes or bindings", "Use :assign on <:if> or <:else-if>.")
			}
			if root == nil {
				return nil, newTemplateErrorWithHint("else-without-if", dir.Pos, "<:else> requires previous <:if> inside <:if-group>", "Start the group with <:if condition={{...}}>.")
			}
			if hasElse {
				return nil, newTemplateErrorWithHint("duplicate-else-block", dir.Pos, "<:if-group> can only contain one <:else>", "Remove the extra <:else> block.")
			}

			body, err := generateDirectiveChildrenStmts(ctx, dir.Children)
			if err != nil {
				return nil, err
			}

			current.Else = &ast.BlockStmt{List: body}
			hasElse = true

		default:
			return nil, newTemplateErrorfWithHint("invalid-if-group-child", dir.Pos, "Only <:if>, <:else-if>, and <:else> are allowed inside <:if-group>.", "<:if-group> invalid child <:%s>", dir.Name)
		}
	}

	if root == nil {
		return nil, newTemplateErrorWithHint("missing-initial-if", TemplatePosition{}, "<:if-group> requires an initial <:if>", "Start the group with <:if condition={{...}}>.")
	}

	return root, nil
}
