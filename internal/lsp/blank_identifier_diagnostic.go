package lsp

import "strings"

func (s *Server) blankIdentifierDiagnostics(document Document) []Diagnostic {
	module, ok := s.semanticModule(document)
	if !ok {
		return nil
	}
	seen := make(map[int]bool)
	var diagnostics []Diagnostic
	for _, semanticErr := range semanticSourceErrors(module) {
		if !strings.Contains(semanticErr.Message, "cannot use _ as value or type") || seen[semanticErr.Offset] {
			continue
		}
		seen[semanticErr.Offset] = true
		diagnostics = append(diagnostics, Diagnostic{
			Range: semanticDiagnosticRange(document, semanticErr.Offset), Severity: diagnosticSeverityError,
			Code: "invalid-blank-identifier", Source: "gox", Message: "cannot use _ as value or type",
		})
	}
	return diagnostics
}
