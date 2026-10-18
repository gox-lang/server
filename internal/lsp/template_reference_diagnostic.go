package lsp

import (
	"fmt"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type templateReferenceResolution int

const (
	templateReferenceMissing templateReferenceResolution = iota
	templateReferenceExists
	templateReferenceUnexported
	templateReferenceWrongKind
)

func (s *Server) templateReferenceDiagnostics(document Document) []Diagnostic {
	module, err := s.analysisModule(document)
	if err != nil {
		return nil
	}
	var diagnostics []Diagnostic
	for _, reference := range module.References {
		resolution := s.resolveTemplateReference(document, reference)
		if resolution == templateReferenceExists {
			continue
		}
		start, startErr := document.Position(reference.Source.Range.Start)
		end, endErr := document.Position(reference.Source.Range.End)
		if startErr != nil || endErr != nil {
			continue
		}
		name := reference.Name
		if reference.Qualifier != "" {
			name = reference.Qualifier + "." + name
		}
		code, message := templateReferenceDiagnosticMessage(reference.Kind, resolution, name)
		diagnostics = append(diagnostics, Diagnostic{
			Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
			Code: code, Source: "gox", Message: message,
		})
	}
	return diagnostics
}

func (s *Server) resolveTemplateReference(document Document, reference goxcompiler.SourceReference) templateReferenceResolution {
	fullName := reference.Name
	tagNameStart := reference.Source.Range.Start
	if reference.Qualifier != "" {
		fullName = reference.Qualifier + "." + reference.Name
		tagNameStart -= len(reference.Qualifier) + 1
	}
	if reference.Kind == goxcompiler.ReferenceClient {
		// Diagnostics already have a parsed module. Do not run completion's
		// whole-document repair/scanning for every client tag occurrence.
		if module, err := s.analysisModule(document); err == nil && module != nil {
			for _, symbol := range module.Symbols {
				if symbol.Name == fullName && symbol.Kind == goxcompiler.SymbolClient {
					return templateReferenceExists
				}
			}
		}

		for _, candidate := range s.symbols.DocumentSymbols(document.URI) {
			if candidate.Symbol.Name != reference.Name || candidate.Symbol.Container != "" {
				continue
			}
			if candidate.Symbol.Kind == goxcompiler.SymbolClient {
				return templateReferenceExists
			}
			return templateReferenceWrongKind
		}
		return templateReferenceMissing
	}
	context := templateSigilContext{Sigil: '&', Prefix: fullName, ReplaceStart: tagNameStart - 2}
	if completionContainsLabel(s.componentSigilCompletions(document, context, reference.Source.Range.End), "&"+fullName) {
		return templateReferenceExists
	}
	if reference.Qualifier != "" {
		_, targetURI, ok := s.resolveImportedModule(document, reference.Qualifier)
		if !ok {
			return templateReferenceMissing
		}
		for _, candidate := range s.symbols.DocumentSymbols(targetURI) {
			symbol := candidate.Symbol
			if symbol.Name != reference.Name || symbol.Container != "" {
				continue
			}
			if !sourceComponentSymbol(symbol) {
				return templateReferenceWrongKind
			}
			if !symbol.Exported {
				return templateReferenceUnexported
			}
			return templateReferenceExists
		}
		return templateReferenceMissing
	}
	if local, ok := s.symbols.LocalSymbolAt(document.URI, reference.Name, reference.Source.Range.Start); ok {
		if completionTypeIsComponent(local.Type) {
			return templateReferenceExists
		}
		return templateReferenceWrongKind
	}
	for _, candidate := range s.symbols.DocumentSymbols(document.URI) {
		symbol := candidate.Symbol
		if symbol.Name != reference.Name || symbol.Container != "" {
			continue
		}
		if sourceComponentSymbol(symbol) {
			return templateReferenceExists
		}
		if symbol.Kind == goxcompiler.SymbolClient {
			return templateReferenceWrongKind
		}
		if moduleMemberKind(symbol.Kind) {
			return templateReferenceWrongKind
		}
	}
	return templateReferenceMissing
}

func templateReferenceDiagnosticMessage(kind goxcompiler.SourceReferenceKind, resolution templateReferenceResolution, name string) (string, string) {
	if kind == goxcompiler.ReferenceClient {
		if resolution == templateReferenceWrongKind {
			return "invalid-client-component", fmt.Sprintf("%s is not a client component", name)
		}
		return "undefined-client-component", fmt.Sprintf("undefined client component: %s", name)
	}
	switch resolution {
	case templateReferenceUnexported:
		return "unexported-component", fmt.Sprintf("cannot refer to unexported component: %s", name)
	case templateReferenceWrongKind:
		return "invalid-component", fmt.Sprintf("%s is not a GOX component", name)
	default:
		return "undefined-component", fmt.Sprintf("undefined component: %s", name)
	}
}
