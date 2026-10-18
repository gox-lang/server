package lsp

import (
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

// Phase 13 removed the legacy workspace pseudo type checker from this file.
// These helpers remain because they provide GOX source/index metadata for
// imported modules and presentation; they do not infer Go types.

func (s *Server) importedSymbol(document Document, qualifier, name string) *IndexedSymbol {
	targetURI, ok := s.importedModuleURI(document, qualifier)
	if !ok {
		return nil
	}
	for _, candidate := range s.symbols.Definitions(name) {
		if candidate.URI == targetURI && candidate.Symbol.Exported && moduleMemberKind(candidate.Symbol.Kind) {
			result := candidate
			return &result
		}
	}
	return nil
}

func (s *Server) qualifyImportedType(targetURI, qualifier, typeName string) string {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" {
		return ""
	}
	for _, symbol := range s.symbols.DocumentSymbols(targetURI) {
		if symbol.Symbol.Container != "" || !typeDefinitionKind(symbol.Symbol.Kind) {
			continue
		}
		typeName = qualifyTypeIdentifier(typeName, symbol.Symbol.Name, qualifier+"."+symbol.Symbol.Name)
	}
	return typeName
}

func qualifyTypeIdentifier(source, name, qualified string) string {
	var output strings.Builder
	for index := 0; index < len(source); {
		if index+len(name) <= len(source) && source[index:index+len(name)] == name &&
			(index == 0 || !identifierByte(source[index-1]) && source[index-1] != '.') &&
			(index+len(name) == len(source) || !identifierByte(source[index+len(name)])) {
			output.WriteString(qualified)
			index += len(name)
			continue
		}
		output.WriteByte(source[index])
		index++
	}
	return output.String()
}

func identifierByte(value byte) bool {
	return value == '_' || value >= '0' && value <= '9' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func typeDefinitionKind(kind goxcompiler.SourceSymbolKind) bool {
	return kind == goxcompiler.SymbolType || kind == goxcompiler.SymbolStruct || kind == goxcompiler.SymbolInterface
}
