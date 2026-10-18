package lsp

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) returnDiagnostics(document Document) []Diagnostic {
	module, ok := s.semanticModule(document)
	if !ok || module.Semantic == nil || module.Semantic.File == nil || module.Semantic.Info == nil || module.Semantic.Origins == nil {
		return nil
	}
	var diagnostics []Diagnostic
	seenFunctions := make(map[ast.Node]bool)
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		if node == nil || seenFunctions[node] {
			return true
		}
		switch function := node.(type) {
		case *ast.FuncDecl:
			seenFunctions[node] = true
			if function.Body != nil {
				if object := module.Semantic.Info.ObjectOf(function.Name); object != nil {
					if signature, ok := object.Type().(*types.Signature); ok {
						diagnostics = append(diagnostics, s.semanticFunctionReturnDiagnostics(document, module, function.Body, signature)...)
					}
				}
			}
		case *ast.FuncLit:
			seenFunctions[node] = true
			if function.Body != nil {
				if typ := module.Semantic.Info.TypeOf(function); typ != nil {
					if signature, ok := typ.Underlying().(*types.Signature); ok {
						diagnostics = append(diagnostics, s.semanticFunctionReturnDiagnostics(document, module, function.Body, signature)...)
					}
				}
			}
		}
		return true
	})
	for _, semanticErr := range semanticSourceErrors(module) {
		if strings.TrimSpace(semanticErr.Message) != "missing return" {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Range: semanticDiagnosticRange(document, semanticErr.Offset), Severity: diagnosticSeverityError,
			Code: "missing-return", Source: "gox", Message: "missing return",
		})
	}
	return diagnostics
}

func (s *Server) semanticFunctionReturnDiagnostics(document Document, module *goxcompiler.Module, body *ast.BlockStmt, signature *types.Signature) []Diagnostic {
	if body == nil || signature == nil {
		return nil
	}
	var diagnostics []Diagnostic
	ast.Inspect(body, func(node ast.Node) bool {
		if literal, ok := node.(*ast.FuncLit); ok && literal != nil {
			return false
		}
		statement, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		origin, exact := module.Semantic.Origins.Get(statement)
		if !exact || !origin.Exact() {
			return false
		}
		diagnostics = append(diagnostics, s.semanticReturnStatementDiagnostics(document, module, statement, signature)...)
		return false
	})
	return diagnostics
}

func (s *Server) semanticReturnStatementDiagnostics(document Document, module *goxcompiler.Module, statement *ast.ReturnStmt, signature *types.Signature) []Diagnostic {
	results := signature.Results()
	want := results.Len()
	if len(statement.Results) == 0 {
		if semanticSignatureResultsNamed(results) {
			return nil
		}
		if want == 0 {
			return nil
		}
		return semanticNodeDiagnostic(document, module, statement, "return-count",
			fmt.Sprintf("return value mismatch: have 0 values, want %s", assignmentCount(want, "value")))
	}

	haveTypes := semanticReturnValueTypes(module, statement.Results)
	if len(haveTypes) != want {
		return semanticNodeDiagnostic(document, module, statement, "return-count",
			fmt.Sprintf("return value mismatch: have %s, want %s", assignmentCount(len(haveTypes), "value"), assignmentCount(want, "value")))
	}
	if len(statement.Results) == 1 && len(haveTypes) > 1 {
		// A tuple-producing call supplies all result slots; go/types already owns
		// the element types and no source expression corresponds to one tuple
		// element independently. Count validation is sufficient here.
		return nil
	}
	var diagnostics []Diagnostic
	for index, expression := range statement.Results {
		if index >= want {
			break
		}
		expected := results.At(index).Type()
		items := s.semanticAssignmentTypeDiagnostic(document, module, expression, expected, 0,
			fmt.Sprintf("in return value %d", index+1))
		for itemIndex := range items {
			items[itemIndex].Code = "return-type-mismatch"
		}
		diagnostics = append(diagnostics, items...)
	}
	return diagnostics
}

func semanticSignatureResultsNamed(results *types.Tuple) bool {
	if results == nil || results.Len() == 0 {
		return false
	}
	for index := 0; index < results.Len(); index++ {
		if results.At(index).Name() == "" {
			return false
		}
	}
	return true
}

func semanticReturnValueTypes(module *goxcompiler.Module, values []ast.Expr) []types.Type {
	if len(values) == 0 {
		return nil
	}
	if len(values) == 1 {
		if typ, ok := semanticTypeAtRaw(module, values[0]); ok {
			if tuple, ok := typ.(*types.Tuple); ok {
				result := make([]types.Type, 0, tuple.Len())
				for index := 0; index < tuple.Len(); index++ {
					result = append(result, tuple.At(index).Type())
				}
				return result
			}
			return []types.Type{typ}
		}
		return []types.Type{nil}
	}
	result := make([]types.Type, 0, len(values))
	for _, expression := range values {
		typ, _ := semanticTypeAt(module, expression, 0)
		result = append(result, typ)
	}
	return result
}

func semanticTypeAtRaw(module *goxcompiler.Module, expression ast.Expr) (types.Type, bool) {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || expression == nil {
		return nil, false
	}
	value, ok := module.Semantic.Info.Types[expression]
	return value.Type, ok && value.Type != nil
}

// functionBodyTerminates reports whether control cannot reach the closing brace
// of a function that declares results. It follows Go's terminating-statement
// rules rather than merely checking whether the body contains a return.
