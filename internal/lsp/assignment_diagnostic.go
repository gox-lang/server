package lsp

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) assignmentTypeDiagnostics(document Document) []Diagnostic {
	module, ok := s.semanticModule(document)
	if !ok || module.Semantic == nil || module.Semantic.File == nil || module.Semantic.Info == nil || module.Semantic.Origins == nil {
		return nil
	}
	var diagnostics []Diagnostic
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CompositeLit:
			origin, exact := module.Semantic.Origins.Get(value)
			if exact && origin.Exact() {
				diagnostics = append(diagnostics, s.semanticStructLiteralDiagnostics(document, module, value)...)
			}
		case *ast.ValueSpec:
			if value.Type == nil {
				break
			}
			expected := module.Semantic.Info.TypeOf(value.Type)
			for index, expression := range value.Values {
				resultIndex := 0
				if len(value.Values) == 1 && len(value.Names) > 1 {
					resultIndex = index
				}
				diagnostics = append(diagnostics, s.semanticAssignmentTypeDiagnostic(document, module, expression, expected, resultIndex, "in assignment")...)
			}
		case *ast.AssignStmt:
			if value.Tok != token.ASSIGN {
				break
			}
			for index, left := range value.Lhs {
				rightIndex := index
				resultIndex := 0
				if len(value.Rhs) == 1 && len(value.Lhs) > 1 {
					rightIndex = 0
					resultIndex = index
				}
				if rightIndex >= len(value.Rhs) {
					continue
				}
				expected := module.Semantic.Info.TypeOf(left)
				diagnostics = append(diagnostics, s.semanticAssignmentTypeDiagnostic(document, module, value.Rhs[rightIndex], expected, resultIndex, "in assignment")...)
			}
		}
		return true
	})
	return diagnostics
}

func (s *Server) semanticStructLiteralDiagnostics(document Document, module *goxcompiler.Module, literal *ast.CompositeLit) []Diagnostic {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || literal == nil {
		return nil
	}
	literalType := module.Semantic.Info.TypeOf(literal.Type)
	if literalType == nil {
		return nil
	}
	underlying := literalType.Underlying()
	structure, ok := underlying.(*types.Struct)
	if !ok {
		return nil
	}
	typeName := semanticTypeText(module, literalType)
	var diagnostics []Diagnostic
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok {
			continue
		}
		fieldIndex := -1
		for index := 0; index < structure.NumFields(); index++ {
			if structure.Field(index).Name() == key.Name {
				fieldIndex = index
				break
			}
		}
		if fieldIndex < 0 {
			diagnostics = append(diagnostics, semanticNodeDiagnostic(document, module, key,
				"unknown-field", fmt.Sprintf("unknown field %s in struct literal of type %s", key.Name, typeName))...)
			continue
		}
		field := structure.Field(fieldIndex)
		if !field.Exported() && field.Pkg() != nil && module.Semantic.Package != nil && field.Pkg().Path() != module.Semantic.Package.Path() {
			diagnostics = append(diagnostics, semanticNodeDiagnostic(document, module, key,
				"unexported-field", fmt.Sprintf("cannot refer to unexported field %s in struct literal of type %s", key.Name, typeName))...)
			continue
		}
		diagnostics = append(diagnostics, s.semanticAssignmentTypeDiagnostic(document, module, pair.Value, field.Type(), 0, "in field "+key.Name)...)
	}
	return diagnostics
}

func (s *Server) semanticAssignmentTypeDiagnostic(document Document, module *goxcompiler.Module, expression ast.Expr, expected types.Type, resultIndex int, context string) []Diagnostic {
	if expected == nil || expression == nil {
		return nil
	}
	actual, ok := semanticTypeAt(module, expression, resultIndex)
	if !ok || actual == nil || semanticTypeAssignable(actual, expected) {
		return nil
	}
	origin, ok := moduleSourceOrigin(module, expression)
	if !ok || !origin.Exact() {
		return nil
	}
	start, startErr := document.Position(origin.Source.Range.Start)
	end, endErr := document.Position(origin.Source.Range.End)
	if startErr != nil || endErr != nil {
		return nil
	}
	expressionText := strings.TrimSpace(document.Text[origin.Source.Range.Start:origin.Source.Range.End])
	return []Diagnostic{{
		Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
		Code: "type-mismatch", Source: "gox",
		Message: fmt.Sprintf("cannot use %s (type %s) as %s %s", expressionText, semanticTypeText(module, actual), semanticTypeText(module, expected), context),
	}}
}

func semanticNodeDiagnostic(document Document, module *goxcompiler.Module, node ast.Node, code, message string) []Diagnostic {
	origin, ok := moduleSourceOrigin(module, node)
	if !ok || !origin.Exact() {
		return nil
	}
	start, startErr := document.Position(origin.Source.Range.Start)
	end, endErr := document.Position(origin.Source.Range.End)
	if startErr != nil || endErr != nil {
		return nil
	}
	return []Diagnostic{{
		Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
		Code: code, Source: "gox", Message: message,
	}}
}
