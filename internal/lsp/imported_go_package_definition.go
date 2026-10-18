package lsp

import (
	"go/ast"
	"go/types"
	"os"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"
)

func (s *Server) importDefinitionLinks(document Document, position Position) []LocationLink {
	offset, err := document.Offset(position)
	if err != nil {
		return nil
	}
	for _, declaration := range s.symbols.Imports(document.URI) {
		if !importSourceContains(declaration.Source.Range, offset) {
			continue
		}
		resolved, err := goxcompiler.ResolveGoxImport(s.projectRoot(document.Path), document.Path, declaration.Path)
		if err != nil {
			return nil
		}
		targetURI := FileURI(resolved)
		target := importTargetLocation(s, targetURI)
		return locationLinksForImport(document, declaration.Source.Range, target)
	}
	for _, declaration := range s.symbols.GoImports(document.URI) {
		if !importSourceContains(declaration.Source.Range, offset) {
			continue
		}
		packageInfo, err := s.goPackages.Load(s.projectRoot(document.Path), declaration.Path)
		if err != nil {
			return nil
		}
		target, ok := goDefinitionLocation(packageInfo.Definition)
		if !ok {
			return nil
		}
		return locationLinksForImport(document, declaration.Source.Range, target)
	}
	return nil
}

func importSourceContains(source goxmap.Range, offset int) bool {
	return source.Contains(offset) || offset == source.End
}

func importTargetLocation(server *Server, uri string) Location {
	for _, symbol := range server.symbols.DocumentSymbols(uri) {
		if symbol.Symbol.Kind != goxcompiler.SymbolModule {
			continue
		}
		if target, ok := server.symbols.Document(uri); ok {
			start, startErr := target.Position(symbol.Symbol.Source.Range.Start)
			end, endErr := target.Position(symbol.Symbol.Source.Range.End)
			if startErr == nil && endErr == nil {
				return Location{URI: uri, Range: Range{Start: start, End: end}}
			}
		}
	}
	return Location{URI: uri, Range: Range{}}
}

func locationLinksForImport(document Document, source goxmap.Range, target Location) []LocationLink {
	start, startErr := document.Position(source.Start)
	end, endErr := document.Position(source.End)
	if startErr != nil || endErr != nil || target.URI == "" {
		return nil
	}
	return []LocationLink{{
		OriginSelectionRange: Range{Start: start, End: end},
		TargetURI:            target.URI,
		TargetRange:          target.Range,
		TargetSelectionRange: target.Range,
	}}
}

func (s *Server) importedGoDefinitionLocations(document Document, position Position) []Location {
	offset, err := document.Offset(position)
	if err != nil {
		return nil
	}
	identifier, _, err := identifierAtPosition(document, position)
	if err != nil || identifier == "" {
		return nil
	}
	selector, hasSelector := selectorAtOffset(document.Text, offset)
	alias := identifier
	if hasSelector {
		alias = selector.qualifier
	}
	_, packageInfo, resolvedAlias, _, ok := s.resolveGoPackageImport(document, alias)
	if !ok || resolvedAlias != alias {
		return nil
	}
	definition := packageInfo.Definition
	if hasSelector && selector.cursorOnMember {
		definition = GoDefinition{}
		for _, exported := range packageInfo.Exports {
			if exported.Name == selector.member {
				definition = exported.Definition
				break
			}
		}
	} else if identifier != resolvedAlias {
		return nil
	}
	location, ok := goDefinitionLocation(definition)
	if !ok {
		return nil
	}
	return []Location{location}
}

func goDefinitionLocation(definition GoDefinition) (Location, bool) {
	if definition.Path == "" || definition.Offset < 0 || definition.Length <= 0 {
		return Location{}, false
	}
	content, err := os.ReadFile(definition.Path)
	if err != nil || definition.Offset+definition.Length > len(content) {
		return Location{}, false
	}
	document := Document{URI: FileURI(definition.Path), Path: definition.Path, Text: string(content)}
	start, startErr := document.Position(definition.Offset)
	end, endErr := document.Position(definition.Offset + definition.Length)
	if startErr != nil || endErr != nil {
		return Location{}, false
	}
	return Location{URI: document.URI, Range: Range{Start: start, End: end}}, true
}

func (s *Server) semanticGoDefinitionLocations(document Document, position Position) []Location {
	offset, err := document.Offset(position)
	if err != nil {
		return nil
	}
	module, ok := s.parseSemanticModule(document)
	if !ok {
		return nil
	}
	node, ok := module.SemanticAtOffset(offset)
	if !ok || node.Object == nil || node.Object.Pkg() == nil {
		return nil
	}
	// Phase 11 owns imported GOX navigation through semanticDefinitionLocations.
	// This path is now only the final source-location bridge for real Go packages
	// (including stdlib), using the canonical semantic object selected above.
	if !semanticObjectAccessibleFrom(module, node.Object) || semanticForeignGoxObject(module, node.Object) {
		return nil
	}
	pkg, err := s.goPackages.Load(s.projectRoot(document.Path), node.Object.Pkg().Path())
	if err != nil {
		return nil
	}
	definition := GoDefinition{}
	selection := node.Selection
	if selection == nil && node.Ident != nil {
		ast.Inspect(module.Semantic.File, func(n ast.Node) bool {
			selector, ok := n.(*ast.SelectorExpr)
			if ok && selector.Sel == node.Ident {
				selection = module.Semantic.Info.Selections[selector]
				return false
			}
			return true
		})
	}
	if selection != nil {
		container := semanticNamedTypeName(selection.Recv())
		for _, exported := range pkg.Exports {
			if exported.Name != container {
				continue
			}
			for _, member := range exported.Members {
				if member.Name == node.Object.Name() {
					definition = member.Definition
					break
				}
			}
		}
	} else {
		for _, exported := range pkg.Exports {
			if exported.Name == node.Object.Name() {
				definition = exported.Definition
				break
			}
		}
	}
	location, ok := goDefinitionLocation(definition)
	if !ok {
		return nil
	}
	return []Location{location}
}

func semanticNamedTypeName(t types.Type) string {
	for {
		switch value := types.Unalias(t).(type) {
		case *types.Pointer:
			t = value.Elem()
		case *types.Named:
			if value.Obj() != nil {
				return value.Obj().Name()
			}
			return ""
		default:
			return ""
		}
	}
}
