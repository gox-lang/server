package gox

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

var tokenFileSet = token.NewFileSet()

func findTemplateAttr(attrs []TemplateAttr, name string) (TemplateAttr, bool) {
	for _, attr := range attrs {
		if attr.Name == name {
			return attr, true
		}
	}

	return TemplateAttr{}, false
}

func requiredDirectiveExpr(n *DirectiveNode, attrName string) (ast.Expr, error) {
	attr, ok := findTemplateAttr(n.Attrs, attrName)
	if !ok {
		return nil, newTemplateErrorfWithHint("missing-required-attribute", n.Pos, fmt.Sprintf("Use <:%s %s={{expr}}>...", n.Name, attrName), "<:%s> requires %s attribute", n.Name, attrName)
	}

	if attr.Expr == nil {
		return nil, newTemplateErrorfWithHint("missing-required-expression", attr.Pos, fmt.Sprintf("Use a Go expression inside %s={{...}}.", attrName), "<:%s %s={{...}}> requires Go expression", n.Name, attrName)
	}

	return attr.Expr, nil
}

func generateDirectiveChildrenStmts(ctx *TemplateGenContext, children []TemplateNode) ([]ast.Stmt, error) {
	childGen := templateGenerator{
		ctx: ctx,
	}

	for _, child := range children {
		if err := childGen.generateNode(child); err != nil {
			return nil, err
		}
	}

	childGen.flushText()
	childGen.resetLine()

	return childGen.stmts, nil
}

func normalizeModuleName(name string) string {
	name = strings.TrimSpace(name)

	if name == "" {
		return name
	}

	return strings.ToUpper(name[:1]) + name[1:]
}
