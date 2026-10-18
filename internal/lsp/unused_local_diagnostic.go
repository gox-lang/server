package lsp

import (
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) unusedLocalDiagnostics(document Document) []Diagnostic {
	module, ok := s.semanticModule(document)
	if !ok {
		return nil
	}
	seen := make(map[int]bool)
	var diagnostics []Diagnostic
	const prefix = "declared and not used: "
	for _, semanticErr := range semanticSourceErrors(module) {
		index := strings.Index(semanticErr.Message, prefix)
		if index < 0 || seen[semanticErr.Offset] {
			continue
		}
		name := strings.TrimSpace(semanticErr.Message[index+len(prefix):])
		if name == "" || name == "_" {
			continue
		}
		if s.localUsedByGoxTemplateReference(document, module, name, semanticErr.Offset) {
			continue
		}
		seen[semanticErr.Offset] = true
		diagnostics = append(diagnostics, Diagnostic{
			Range: semanticDiagnosticRange(document, semanticErr.Offset), Severity: diagnosticSeverityError,
			Code: "unused-local", Source: "gox", Message: prefix + name,
		})
	}
	return diagnostics
}

// localUsedByGoxTemplateReference reconciles GOX-only component tag uses with
// go/types' unused-variable diagnostics. A reference such as <&menu/> is not a
// Go identifier use in the semantic projection, but it is a real source-level
// use of the local component. Scope resolution stays compiler-owned through
// WorkspaceIndex.LocalSymbolAt so shadowed locals are not conflated.
func (s *Server) localUsedByGoxTemplateReference(document Document, module *goxcompiler.Module, name string, declarationOffset int) bool {
	if s == nil || module == nil || name == "" {
		return false
	}
	for _, reference := range module.References {
		if reference.Kind != goxcompiler.ReferenceComponent || reference.Qualifier != "" || reference.Name != name {
			continue
		}
		local, ok := s.symbols.LocalSymbolAt(document.URI, name, reference.Source.Range.Start)
		if ok && local.Source.Range.Start == declarationOffset {
			return true
		}
	}
	return false
}
