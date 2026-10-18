package lsp

import (
	"sort"
	"strings"
	"unicode"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) typeDefinitionLocations(document Document, position Position) []Location {
	if offset, err := document.Offset(position); err == nil && s.contextAt(document, offset).passiveEmbedded() {
		return []Location{}
	}
	if locations := s.importedGoDefinitionLocations(document, position); len(locations) > 0 {
		return locations
	}
	definitions, err := s.definitionsAt(document, position)
	if err != nil || len(definitions) == 0 {
		return []Location{}
	}
	var targets []IndexedSymbol
	var external []Location
	for _, definition := range definitions {
		if sourceTypeKind(definition.Symbol.Kind) {
			targets = append(targets, definition)
			continue
		}
		targetDocument, ok := s.symbols.Document(definition.URI)
		if !ok {
			targetDocument = document
		}
		resolved := s.resolveTypeSymbols(targetDocument, definition.Symbol.Type)
		targets = append(targets, resolved...)
		if len(resolved) == 0 {
			external = append(external, s.resolveGoTypeLocations(targetDocument, definition.Symbol.Type)...)
		}
	}
	external = append(external, s.locationsForDefinitions(uniqueIndexedSymbols(targets))...)
	return uniqueLocations(external)
}

func (s *Server) resolveTypeSymbols(document Document, typeName string) []IndexedSymbol {
	for _, candidate := range namedTypesIn(typeName) {
		if qualifier, name, ok := qualifiedTypeName(candidate); ok {
			if targetURI, imported := s.importedModuleURI(document, qualifier); imported {
				return filterDefinitions(s.symbols.Definitions(name), func(symbol IndexedSymbol) bool {
					return symbol.URI == targetURI && sourceTypeKind(symbol.Symbol.Kind)
				})
			}
			continue
		}
		local := filterDefinitions(s.symbols.Definitions(candidate), func(symbol IndexedSymbol) bool {
			return symbol.URI == document.URI && sourceTypeKind(symbol.Symbol.Kind)
		})
		if len(local) > 0 {
			return local
		}
	}
	return nil
}

func (s *Server) resolveGoTypeLocations(document Document, typeName string) []Location {
	for _, candidate := range namedTypesIn(typeName) {
		qualifier, name, ok := qualifiedTypeName(candidate)
		if !ok {
			continue
		}
		_, packageInfo, alias, _, imported := s.resolveGoPackageImport(document, qualifier)
		if !imported || alias != qualifier {
			continue
		}
		for _, exported := range packageInfo.Exports {
			if exported.Name != name || exported.Kind != "type" && exported.Kind != "struct" && exported.Kind != "interface" {
				continue
			}
			if location, ok := goDefinitionLocation(exported.Definition); ok {
				return []Location{location}
			}
		}
	}
	return nil
}

func (s *Server) implementationLocations(document Document, position Position) []Location {
	definitions, err := s.definitionsAt(document, position)
	if err != nil || len(definitions) == 0 {
		return []Location{}
	}
	var targets []IndexedSymbol
	for _, definition := range definitions {
		if !sourceTypeKind(definition.Symbol.Kind) && definition.Symbol.Kind != goxcompiler.SymbolMethod {
			resolved := s.resolveTypeSymbols(document, definition.Symbol.Type)
			if len(resolved) > 0 {
				definition = resolved[0]
			}
		}
		targets = append(targets, s.implementationsForSymbol(definition)...)
	}
	return uniqueLocations(s.locationsForDefinitions(uniqueIndexedSymbols(targets)))
}

func (s *Server) implementationsForSymbol(target IndexedSymbol) []IndexedSymbol {
	owner := target
	methodName := ""
	if target.Symbol.Kind == goxcompiler.SymbolMethod {
		methodName = target.Symbol.Name
		if candidate, ok := s.containerType(target.URI, target.Symbol.Container); ok {
			owner = candidate
		} else {
			return nil
		}
	}
	interfaces, concretes := s.workspaceNamedTypes()
	var results []IndexedSymbol
	if owner.Symbol.Kind == goxcompiler.SymbolInterface {
		required := s.typeMethods(owner)
		for _, concrete := range concretes {
			if !methodSetImplements(s.typeMethods(concrete), required) {
				continue
			}
			if methodName == "" {
				results = append(results, concrete)
			} else if method, ok := matchingMethod(s.typeMethods(concrete), required[methodName]); ok {
				results = append(results, method)
			}
		}
		return results
	}
	provided := s.typeMethods(owner)
	for _, contract := range interfaces {
		required := s.typeMethods(contract)
		if !methodSetImplements(provided, required) {
			continue
		}
		if methodName == "" {
			results = append(results, contract)
		} else if method, ok := matchingMethod(required, provided[methodName]); ok {
			results = append(results, method)
		}
	}
	return results
}

func (s *Server) workspaceNamedTypes() ([]IndexedSymbol, []IndexedSymbol) {
	var interfaces, concretes []IndexedSymbol
	for _, document := range s.symbols.Documents() {
		for _, symbol := range s.symbols.DocumentSymbols(document.URI) {
			switch symbol.Symbol.Kind {
			case goxcompiler.SymbolInterface:
				interfaces = append(interfaces, symbol)
			case goxcompiler.SymbolStruct, goxcompiler.SymbolType:
				concretes = append(concretes, symbol)
			}
		}
	}
	return interfaces, concretes
}

func (s *Server) containerType(uri, container string) (IndexedSymbol, bool) {
	container = strings.TrimPrefix(strings.TrimSpace(container), "*")
	for _, symbol := range s.symbols.DocumentSymbols(uri) {
		if symbol.Symbol.Name == container && sourceTypeKind(symbol.Symbol.Kind) {
			return symbol, true
		}
	}
	return IndexedSymbol{}, false
}

func (s *Server) typeMethods(owner IndexedSymbol) map[string]IndexedSymbol {
	container := strings.TrimPrefix(strings.TrimSpace(owner.Symbol.Name), "*")
	methods := map[string]IndexedSymbol{}
	for _, symbol := range s.symbols.DocumentSymbols(owner.URI) {
		if symbol.Symbol.Kind == goxcompiler.SymbolMethod && strings.TrimPrefix(symbol.Symbol.Container, "*") == container {
			methods[symbol.Symbol.Name] = symbol
		}
	}
	return methods
}

func methodSetImplements(provided, required map[string]IndexedSymbol) bool {
	for name, contract := range required {
		method, ok := provided[name]
		if !ok || !methodSignaturesEqual(method.Symbol, contract.Symbol) {
			return false
		}
	}
	return true
}

func matchingMethod(methods map[string]IndexedSymbol, expected IndexedSymbol) (IndexedSymbol, bool) {
	method, ok := methods[expected.Symbol.Name]
	return method, ok && methodSignaturesEqual(method.Symbol, expected.Symbol)
}

func methodSignaturesEqual(left, right goxcompiler.SourceSymbol) bool {
	if len(left.Parameters) != len(right.Parameters) || len(left.Results) != len(right.Results) {
		return false
	}
	for index := range left.Parameters {
		if normalizedSignatureType(left.Parameters[index].Type) != normalizedSignatureType(right.Parameters[index].Type) {
			return false
		}
	}
	for index := range left.Results {
		if normalizedSignatureType(left.Results[index]) != normalizedSignatureType(right.Results[index]) {
			return false
		}
	}
	return true
}

func normalizedSignatureType(value string) string {
	value = strings.Join(strings.Fields(value), "")
	if separator := strings.LastIndexByte(value, '.'); separator >= 0 {
		value = value[separator+1:]
	}
	return value
}

func sourceTypeKind(kind goxcompiler.SourceSymbolKind) bool {
	return kind == goxcompiler.SymbolType || kind == goxcompiler.SymbolStruct || kind == goxcompiler.SymbolInterface
}

func namedTypesIn(value string) []string {
	var result []string
	for offset := 0; offset < len(value); {
		if value[offset] != '_' && !unicode.IsLetter(rune(value[offset])) {
			offset++
			continue
		}
		start := offset
		for offset < len(value) && (value[offset] == '_' || value[offset] == '.' || unicode.IsLetter(rune(value[offset])) || unicode.IsDigit(rune(value[offset]))) {
			offset++
		}
		candidate := value[start:offset]
		if !builtinTypeWord(candidate) {
			result = append(result, candidate)
		}
	}
	return result
}

func builtinTypeWord(value string) bool {
	switch value {
	case "bool", "byte", "complex64", "complex128", "error", "float32", "float64", "int", "int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "any", "interface", "struct", "map", "chan", "func":
		return true
	default:
		return false
	}
}

func qualifiedTypeName(value string) (string, string, bool) {
	separator := strings.LastIndexByte(value, '.')
	return value[:maxInt(separator, 0)], value[separator+1:], separator > 0 && separator < len(value)-1
}

func uniqueIndexedSymbols(values []IndexedSymbol) []IndexedSymbol {
	seen := map[string]bool{}
	result := make([]IndexedSymbol, 0, len(values))
	for _, value := range values {
		key := value.URI + "\x00" + value.Symbol.Name + "\x00" + string(value.Symbol.Kind) + "\x00" + value.Symbol.Container
		if !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].URI != result[j].URI {
			return result[i].URI < result[j].URI
		}
		return result[i].Symbol.Source.Range.Start < result[j].Symbol.Source.Range.Start
	})
	return result
}

func uniqueLocations(values []Location) []Location {
	seen := map[Location]bool{}
	result := make([]Location, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
