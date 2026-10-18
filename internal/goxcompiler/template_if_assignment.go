package gox

import (
	"go/ast"
	"go/token"
)

// The initializer belongs to the if's implicit Go scope, including its else.
func templateIfAssignment(n *DirectiveNode) (*ast.AssignStmt, []TemplateAttr, error) {
	var bindings []TemplateAttr
	var assignment *TemplateAttr
	seen := map[string]bool{}
	for i := range n.Attrs {
		a := &n.Attrs[i]
		switch a.Name {
		case "condition":
			continue
		case ":assign":
			if assignment != nil || a.Expr == nil {
				return nil, nil, newTemplateErrorWithHint("invalid-if-assignment", a.Pos, ":assign requires one Go expression and cannot be repeated", "Use bindings :assign={{ expression }} condition={{ expression }}.")
			}
			assignment = a
		default:
			if a.Kind != AttrBoolean || !token.IsIdentifier(a.Name) || assignment != nil {
				return nil, nil, newTemplateErrorWithHint("invalid-if-binding", a.Pos, "if bindings must be bare identifiers before :assign", "Use user ok :assign={{ findUser(id) }} condition={{ ok }}.")
			}
			if a.Name != "_" && seen[a.Name] {
				return nil, nil, newTemplateErrorWithHint("duplicate-if-binding", a.Pos, "duplicate if binding", "Use distinct names or _.")
			}
			seen[a.Name] = true
			bindings = append(bindings, *a)
		}
	}
	if assignment == nil && len(bindings) == 0 {
		return nil, nil, nil
	}
	if assignment == nil || len(bindings) == 0 || (len(seen) == 1 && seen["_"]) {
		return nil, nil, newTemplateErrorWithHint("missing-if-assignment-binding", n.Pos, ":assign requires bindings with at least one non-blank identifier", "Use name :assign={{ expression }} condition={{ expression }}.")
	}
	lhs := make([]ast.Expr, len(bindings))
	for i, b := range bindings {
		lhs[i] = ast.NewIdent(b.Name)
	}
	return &ast.AssignStmt{Lhs: lhs, Tok: token.DEFINE, Rhs: []ast.Expr{assignment.Expr}}, bindings, nil
}

func templateIfContext(ctx *TemplateGenContext, bindings []TemplateAttr) *TemplateGenContext {
	if len(bindings) == 0 {
		return ctx
	}
	next := *ctx
	if ctx.TypeEnv == nil {
		next.TypeEnv = NewTemplateTypeEnv()
	} else {
		next.TypeEnv = ctx.TypeEnv.Clone()
	}
	for _, b := range bindings {
		if b.Name != "_" {
			next.TypeEnv.Vars[b.Name] = "any"
			delete(next.TypeEnv.ComponentValues, b.Name)
		}
	}
	return &next
}
