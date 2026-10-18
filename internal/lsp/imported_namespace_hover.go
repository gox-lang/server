package lsp

import (
	"fmt"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

const importedNamespaceExportLimit = 30

func (s *Server) importedModuleHover(document Document, position Position, definitions []IndexedSymbol) *Hover {
	if len(definitions) != 1 || definitions[0].URI == document.URI || definitions[0].Symbol.Kind != goxcompiler.SymbolModule {
		return nil
	}
	qualifier, _, err := identifierAtPosition(document, position)
	if err != nil || qualifier == "" {
		return nil
	}
	declaration, targetURI, ok := s.resolveImportedModule(document, qualifier)
	if !ok || targetURI != definitions[0].URI {
		return nil
	}
	moduleName := definitions[0].Symbol.Name
	return &Hover{Contents: s.importedModuleMarkup(targetURI, moduleName, declaration.Path, qualifier, declaration.Name != "")}
}

func (s *Server) importedModuleMarkup(uri, moduleName, path, alias string, explicit bool) MarkupContent {
	detail, hidden := s.importedModuleSummary(uri, moduleName)
	value := "```gox\n" + detail + "\n```"
	aliasKind := "inferred"
	if explicit {
		aliasKind = "explicit"
	}
	value += fmt.Sprintf("\n\nGOX module imported with %s alias `%s` from `%s`.", aliasKind, alias, path)
	if hidden > 0 {
		value += fmt.Sprintf("\n\n%d additional exported declarations not shown.", hidden)
	}
	return MarkupContent{Kind: "markdown", Value: value}
}

func (s *Server) importedModuleSummary(uri, moduleName string) (string, int) {
	lines := make([]string, 0, importedNamespaceExportLimit)
	for _, candidate := range s.symbols.DocumentSymbols(uri) {
		symbol := candidate.Symbol
		if !symbol.Exported || symbol.Container != "" || !moduleMemberKind(symbol.Kind) {
			continue
		}
		lines = append(lines, importedExportDetail(symbol))
	}
	hidden := 0
	if len(lines) > importedNamespaceExportLimit {
		hidden = len(lines) - importedNamespaceExportLimit
		lines = lines[:importedNamespaceExportLimit]
	}
	if len(lines) == 0 {
		return "module " + moduleName + " {}", hidden
	}
	return "module " + moduleName + " {\n\t" + strings.Join(lines, "\n\t") + "\n}", hidden
}

func importedExportDetail(symbol goxcompiler.SourceSymbol) string {
	switch symbol.Kind {
	case goxcompiler.SymbolStruct:
		return "struct " + symbol.Name
	case goxcompiler.SymbolInterface:
		return "interface " + symbol.Name
	}
	if detail := strings.TrimSpace(symbol.Detail); detail != "" {
		return detail
	}
	return string(symbol.Kind) + " " + symbol.Name
}
