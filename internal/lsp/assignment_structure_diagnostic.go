package lsp

import (
	"fmt"
	"go/ast"
	"go/token"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) assignmentStructureDiagnostics(document Document) []Diagnostic {
	module, err := s.analysisModule(document)
	if err != nil || module.File == nil || module.Origins == nil {
		return nil
	}

	var diagnostics []Diagnostic
	diagnostics = append(diagnostics, topLevelRedeclarationDiagnostics(document, module)...)
	ast.Inspect(module.File, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.ValueSpec:
			for _, name := range value.Names {
				if name.Name != "_" && localNameRedeclared(module, name) {
					diagnostics = append(diagnostics, sourceNodeDiagnostic(document, module, name,
						"redeclared-local", fmt.Sprintf("%s redeclared in this block", name.Name))...)
				}
			}
			diagnostics = append(diagnostics, s.assignmentValueArityDiagnostics(document, module, value, len(value.Names), value.Values)...)
		case *ast.AssignStmt:
			if value.Tok == token.DEFINE && !shortDeclarationHasNewName(module, value) {
				diagnostics = append(diagnostics, sourceNodeDiagnostic(document, module, value,
					"no-new-variables", "no new variables on left side of :=")...)
			}
			diagnostics = append(diagnostics, s.assignmentArityDiagnostics(document, module, value)...)
		}
		return true
	})
	return diagnostics
}

func topLevelRedeclarationDiagnostics(document Document, module *goxcompiler.Module) []Diagnostic {
	seen := make(map[string]goxcompiler.SourceSymbol)
	var diagnostics []Diagnostic
	for _, symbol := range module.Symbols {
		if symbol.Container != "" || !topLevelDeclarationKind(symbol.Kind) {
			continue
		}
		if previous, exists := seen[symbol.Name]; exists && previous.Source.Range.Start != symbol.Source.Range.Start {
			start, startErr := document.Position(symbol.Source.Range.Start)
			end, endErr := document.Position(symbol.Source.Range.End)
			if startErr == nil && endErr == nil {
				diagnostics = append(diagnostics, Diagnostic{
					Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
					Code: "redeclared-symbol", Source: "gox", Message: symbol.Name + " redeclared in this block",
				})
			}
			continue
		}
		seen[symbol.Name] = symbol
	}
	return diagnostics
}

func topLevelDeclarationKind(kind goxcompiler.SourceSymbolKind) bool {
	switch kind {
	case goxcompiler.SymbolFunction, goxcompiler.SymbolType, goxcompiler.SymbolStruct,
		goxcompiler.SymbolInterface, goxcompiler.SymbolVariable, goxcompiler.SymbolConstant:
		return true
	default:
		return false
	}
}

func localNameRedeclared(module *goxcompiler.Module, name *ast.Ident) bool {
	origin, ok := module.Origins.Get(name)
	if !ok || !origin.Exact() {
		return false
	}
	current, found := localSymbolDeclaredAt(module.LocalSymbols, name.Name, origin.Source.Range.Start)
	if !found {
		return false
	}
	return localSymbolHasEarlierDeclaration(module.LocalSymbols, current)
}

func shortDeclarationHasNewName(module *goxcompiler.Module, statement *ast.AssignStmt) bool {
	for _, expression := range statement.Lhs {
		name, ok := expression.(*ast.Ident)
		if !ok || name.Name == "_" {
			continue
		}
		origin, exact := module.Origins.Get(name)
		if !exact || !origin.Exact() {
			continue
		}
		current, found := localSymbolDeclaredAt(module.LocalSymbols, name.Name, origin.Source.Range.Start)
		if !found {
			continue
		}
		if !localSymbolHasEarlierDeclaration(module.LocalSymbols, current) {
			return true
		}
	}
	return false
}

func localSymbolHasEarlierDeclaration(symbols []goxcompiler.LocalSymbol, current goxcompiler.LocalSymbol) bool {
	for _, candidate := range symbols {
		if candidate.Name == current.Name && candidate.Source.Range.Start < current.Source.Range.Start &&
			candidate.Scope.Range == current.Scope.Range {
			return true
		}
	}
	return false
}

func localSymbolDeclaredAt(symbols []goxcompiler.LocalSymbol, name string, offset int) (goxcompiler.LocalSymbol, bool) {
	for _, symbol := range symbols {
		if symbol.Name == name && symbol.Source.Range.Start == offset {
			return symbol, true
		}
	}
	return goxcompiler.LocalSymbol{}, false
}

func (s *Server) assignmentArityDiagnostics(document Document, module *goxcompiler.Module, statement *ast.AssignStmt) []Diagnostic {
	return s.assignmentValueArityDiagnostics(document, module, statement, len(statement.Lhs), statement.Rhs)
}

func (s *Server) assignmentValueArityDiagnostics(document Document, module *goxcompiler.Module, node ast.Node, want int, values []ast.Expr) []Diagnostic {
	if want == 0 || len(values) == 0 {
		return nil
	}
	have := len(values)
	callName := ""
	if len(values) == 1 {
		call, ok := values[0].(*ast.CallExpr)
		if !ok {
			// Index expressions, receives and type assertions may legally yield a
			// second comma-ok value. Leave those cases to full type information.
			if want > 1 {
				return nil
			}
		} else if context, found := sourceSignatureCallContext(document, module, call); found {
			if candidate, resolved := s.resolveSignatureCandidate(document, context); resolved && !signatureCandidateIsComponent(candidate) {
				have = len(candidate.Results)
				if have == 0 && candidate.Type != "" {
					have = 1
				}
				callName = signatureContextName(context)
			} else {
				return nil
			}
		} else {
			return nil
		}
	}
	if want == have {
		return nil
	}
	message := fmt.Sprintf("assignment mismatch: %s but %s", assignmentCount(want, "variable"), assignmentCount(have, "value"))
	if callName != "" {
		message = fmt.Sprintf("assignment mismatch: %s but %s returns %s", assignmentCount(want, "variable"), callName, assignmentCount(have, "value"))
	}
	return sourceNodeDiagnostic(document, module, node, "assignment-mismatch", message)
}

func assignmentCount(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func sourceNodeDiagnostic(document Document, module *goxcompiler.Module, node ast.Node, code, message string) []Diagnostic {
	origin, ok := module.Origins.Get(node)
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
