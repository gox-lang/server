package lsp

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) conditionDiagnostics(document Document) []Diagnostic {
	module, ok := s.semanticModule(document)
	if !ok || module.Semantic == nil || module.Semantic.File == nil || module.Semantic.Info == nil || module.Semantic.Origins == nil {
		return nil
	}
	var diagnostics []Diagnostic
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.IfStmt:
			context := "if statement"
			if origin, exact := module.Semantic.Origins.Get(value.Cond); exact && origin.Exact() && sourceOriginInsideTemplate(module, origin) {
				context = ":if directive"
			}
			diagnostics = append(diagnostics, s.semanticRequireBooleanExpression(document, module, value.Cond, context)...)
		case *ast.ForStmt:
			if value.Cond != nil {
				diagnostics = append(diagnostics, s.semanticRequireBooleanExpression(document, module, value.Cond, "for statement")...)
			}
		case *ast.UnaryExpr:
			if value.Op == token.NOT {
				diagnostics = append(diagnostics, s.semanticRequireBooleanOperand(document, module, value.X, "!")...)
			}
		case *ast.BinaryExpr:
			switch value.Op {
			case token.LAND, token.LOR:
				diagnostics = append(diagnostics, s.semanticRequireBooleanOperand(document, module, value.X, value.Op.String())...)
				diagnostics = append(diagnostics, s.semanticRequireBooleanOperand(document, module, value.Y, value.Op.String())...)
			case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
				diagnostics = append(diagnostics, s.semanticComparisonDiagnostics(document, module, value)...)
			}
		}
		return true
	})
	return diagnostics
}

func (s *Server) semanticRequireBooleanExpression(document Document, module *goxcompiler.Module, expression ast.Expr, context string) []Diagnostic {
	actual, ok := semanticTypeAt(module, expression, 0)
	if !ok || semanticBooleanType(actual) {
		return nil
	}
	return semanticNodeDiagnostic(document, module, expression, "non-boolean-condition",
		fmt.Sprintf("non-boolean condition in %s (type %s)", context, semanticTypeText(module, actual)))
}

func (s *Server) semanticRequireBooleanOperand(document Document, module *goxcompiler.Module, expression ast.Expr, operator string) []Diagnostic {
	actual, ok := semanticTypeAt(module, expression, 0)
	if !ok || semanticBooleanType(actual) {
		return nil
	}
	return semanticNodeDiagnostic(document, module, expression, "invalid-boolean-operand",
		fmt.Sprintf("operator %s requires boolean operand; got %s", operator, semanticTypeText(module, actual)))
}

func (s *Server) semanticComparisonDiagnostics(document Document, module *goxcompiler.Module, expression *ast.BinaryExpr) []Diagnostic {
	left, leftOK := semanticTypeAt(module, expression.X, 0)
	right, rightOK := semanticTypeAt(module, expression.Y, 0)
	if !leftOK || !rightOK || semanticTypeIsUntypedNil(left) || semanticTypeIsUntypedNil(right) {
		return nil
	}
	leftText := semanticComparisonOperandTypeText(module, expression.X, left)
	rightText := semanticComparisonOperandTypeText(module, expression.Y, right)
	compatible := types.AssignableTo(left, right) || types.AssignableTo(right, left)
	if !compatible {
		return semanticNodeDiagnostic(document, module, expression, "mismatched-comparison",
			fmt.Sprintf("invalid operation: mismatched types %s and %s", leftText, rightText))
	}
	if orderedComparisonOperator(expression.Op) && (!semanticOrderedType(left) || !semanticOrderedType(right)) {
		return semanticNodeDiagnostic(document, module, expression, "invalid-comparison",
			fmt.Sprintf("operator %s not defined on %s and %s", expression.Op, leftText, rightText))
	}
	if (expression.Op == token.EQL || expression.Op == token.NEQ) && (!types.Comparable(left) || !types.Comparable(right)) {
		return semanticNodeDiagnostic(document, module, expression, "invalid-comparison",
			fmt.Sprintf("operator %s not defined on %s and %s", expression.Op, leftText, rightText))
	}
	return nil
}

func semanticComparisonOperandTypeText(module *goxcompiler.Module, expression ast.Expr, typ types.Type) string {
	// go/types contextually converts the predeclared boolean constants true and
	// false to bool in expressions such as `enabled < false`. GOX diagnostics
	// have historically described the source literal itself, whose source type
	// is untyped bool. Keep the semantic checker responsible for validity while
	// preserving that stable source-facing presentation.
	if ident, ok := expression.(*ast.Ident); ok && (ident.Name == "true" || ident.Name == "false") {
		return "untyped bool"
	}
	return semanticTypeText(module, typ)
}

func semanticTypeIsUntypedNil(typ types.Type) bool {
	basic, ok := typ.(*types.Basic)
	return ok && basic.Kind() == types.UntypedNil
}

func orderedComparisonOperator(operator token.Token) bool {
	return operator == token.LSS || operator == token.LEQ || operator == token.GTR || operator == token.GEQ
}
