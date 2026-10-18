package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"
)

func (s *Server) importResolutionDiagnostics(document Document) []Diagnostic {
	module, err := s.analysisModule(document)
	if err != nil {
		return nil
	}

	root := s.goImportRoot(document.Path)
	diagnostics := make([]Diagnostic, 0)
	for _, declaration := range module.GoxImports {
		alias := goxImportDiagnosticAlias(declaration)
		resolved, resolveErr := goxcompiler.ResolveGoxImport(root, document.Path, declaration.Path)
		if resolveErr != nil {
			diagnostics = append(diagnostics, importDiagnostic(
				document,
				declaration.Source,
				"unresolved-gox-import",
				fmt.Sprintf("cannot resolve GOX import %q", declaration.Path),
			))
			diagnostics = append(diagnostics, unresolvedImportAliasDiagnostics(document, alias)...)
			continue
		}

		info, statErr := os.Stat(resolved)
		if statErr != nil {
			diagnostics = append(diagnostics, importDiagnostic(
				document,
				declaration.Source,
				"unresolved-gox-import",
				fmt.Sprintf("cannot resolve GOX import %q", declaration.Path),
			))
			diagnostics = append(diagnostics, unresolvedImportAliasDiagnostics(document, alias)...)
			continue
		}
		if info.IsDir() {
			diagnostics = append(diagnostics, importDiagnostic(
				document,
				declaration.Source,
				"invalid-gox-import",
				fmt.Sprintf("GOX import %q resolves to a directory", declaration.Path),
			))
			diagnostics = append(diagnostics, unresolvedImportAliasDiagnostics(document, alias)...)
			continue
		}

		contents, readErr := os.ReadFile(resolved)
		if readErr != nil {
			diagnostics = append(diagnostics, importDiagnostic(
				document,
				declaration.Source,
				"unreadable-gox-import",
				fmt.Sprintf("cannot read GOX import %q", declaration.Path),
			))
			diagnostics = append(diagnostics, unresolvedImportAliasDiagnostics(document, alias)...)
			continue
		}
		if _, parseErr := goxcompiler.ParseModule(resolved, string(contents)); parseErr != nil {
			diagnostics = append(diagnostics, importDiagnostic(
				document,
				declaration.Source,
				"invalid-gox-import",
				fmt.Sprintf("cannot load GOX import %q: %v", declaration.Path, parseErr),
			))
			diagnostics = append(diagnostics, unresolvedImportAliasDiagnostics(document, alias)...)
		}
	}

	for _, declaration := range module.GoImports {
		if _, loadErr := s.goPackages.Load(root, declaration.Path); loadErr == nil {
			continue
		}
		if s.goImportPathMayBeIncomplete(root, declaration.Path) {
			continue
		}
		diagnostics = append(diagnostics, importDiagnostic(
			document,
			declaration.Source,
			"unresolved-go-import",
			fmt.Sprintf("cannot resolve Go import %q", declaration.Path),
		))
		diagnostics = append(diagnostics, unresolvedImportAliasDiagnostics(document, goImportDiagnosticAlias(declaration))...)
	}
	return diagnostics
}

// goImportPathMayBeIncomplete suppresses a transient unresolved-import
// diagnostic while a user is typing a known import path. Matching is by the
// full path or its final segment; package names are deliberately not used
// here because import paths and package declarations are independent in Go.
func (s *Server) goImportPathMayBeIncomplete(root, importPath string) bool {
	candidates, err := s.goPackages.Discover(root)
	if err != nil {
		return false
	}
	for _, candidate := range candidates {
		if importPathCandidateMatches(importPathCandidate{Path: candidate.Path}, importPath, false) {
			return true
		}
	}
	return false
}

func goxImportDiagnosticAlias(declaration goxcompiler.GoxImport) string {
	if declaration.Name != "" {
		return declaration.Name
	}
	return pathImportDiagnosticAlias(declaration.Path)
}

func goImportDiagnosticAlias(declaration goxcompiler.ImportDecl) string {
	if declaration.Alias != "" {
		return declaration.Alias
	}
	return pathImportDiagnosticAlias(declaration.Path)
}

func pathImportDiagnosticAlias(importPath string) string {
	name := strings.TrimSuffix(filepath.Base(importPath), filepath.Ext(importPath))
	parts := strings.FieldsFunc(name, func(value rune) bool {
		return value == '-' || value == '_' || value == '.' || unicode.IsSpace(value)
	})
	if len(parts) == 0 {
		return ""
	}
	name = strings.Join(parts, "")
	values := []rune(name)
	if len(values) == 0 {
		return ""
	}
	values[0] = unicode.ToLower(values[0])
	return string(values)
}

func unresolvedImportAliasDiagnostics(document Document, alias string) []Diagnostic {
	if alias == "" || alias == "_" || alias == "." {
		return nil
	}
	var diagnostics []Diagnostic
	for _, occurrence := range selectorOccurrences(document.Text) {
		if occurrence.qualifier != alias {
			continue
		}
		start, startErr := document.Position(occurrence.qualifierStart)
		end, endErr := document.Position(occurrence.qualifierEnd)
		if startErr != nil || endErr != nil {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
			Code: "undefined-identifier", Source: "gox", Message: "undefined: " + alias,
		})
	}
	return diagnostics
}

func importDiagnostic(document Document, source goxmap.SourceRange, code, message string) Diagnostic {
	start, startErr := document.Position(source.Range.Start)
	end, endErr := document.Position(source.Range.End)
	if startErr != nil || endErr != nil {
		return Diagnostic{
			Range: pointRange(Position{}), Severity: diagnosticSeverityError,
			Code: code, Source: "gox", Message: message,
		}
	}
	return Diagnostic{
		Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
		Code: code, Source: "gox", Message: message,
	}
}
