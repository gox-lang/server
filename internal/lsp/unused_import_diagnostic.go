package lsp

import (
	"fmt"
	"os"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) unusedImportDiagnostics(document Document) []Diagnostic {
	module, err := s.analysisModule(document)
	if err != nil {
		return nil
	}

	root := s.projectRoot(document.Path)
	var diagnostics []Diagnostic
	for _, declaration := range module.GoxImports {
		if declaration.Name == "_" || declaration.Name == "." {
			continue
		}
		resolved, resolveErr := goxcompiler.ResolveGoxImport(root, document.Path, declaration.Path)
		if resolveErr != nil {
			continue
		}
		contents, readErr := os.ReadFile(resolved)
		if readErr != nil {
			continue
		}
		target, parseErr := goxcompiler.ParseModule(resolved, string(contents))
		if parseErr != nil {
			continue
		}
		alias := declaration.Name
		if alias == "" {
			alias = target.DeclaredName
		}
		if alias == "" || importAliasUsed(document.Text, alias) {
			continue
		}
		diagnostics = append(diagnostics, importDiagnostic(
			document,
			declaration.Source,
			"unused-import",
			unusedImportMessage(declaration.Path, alias, declaration.Name != ""),
		))
	}

	for _, declaration := range module.GoImports {
		if declaration.Alias == "_" || declaration.Alias == "." {
			continue
		}
		packageInfo, loadErr := s.goPackages.Load(root, declaration.Path)
		if loadErr != nil {
			continue
		}
		alias := declaration.Alias
		if alias == "" {
			alias = packageInfo.Name
		}
		if alias == "" || importAliasUsed(document.Text, alias) {
			continue
		}
		diagnostics = append(diagnostics, importDiagnostic(
			document,
			declaration.Source,
			"unused-import",
			unusedImportMessage(declaration.Path, alias, declaration.Alias != ""),
		))
	}
	return diagnostics
}

func importAliasUsed(source, alias string) bool {
	for _, occurrence := range selectorOccurrences(source) {
		if occurrence.qualifier == alias {
			return true
		}
	}
	return false
}

func unusedImportMessage(importPath, alias string, explicit bool) string {
	if explicit {
		return fmt.Sprintf("%q imported as %s and not used", importPath, alias)
	}
	return fmt.Sprintf("%q imported and not used", importPath)
}
