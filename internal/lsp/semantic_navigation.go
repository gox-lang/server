package lsp

import (
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type semanticNavigationState uint8

const (
	semanticNavigationNone semanticNavigationState = iota
	semanticNavigationOwned
	semanticNavigationBlocked
	semanticNavigationFallback
)

type semanticSymbolIdentity struct {
	Kind       string
	Name       string
	Package    string
	Container  string
	LocalURI   string
	LocalStart int
	LocalEnd   int
}

type semanticNavigationTarget struct {
	Module   *goxcompiler.Module
	Object   types.Object
	Identity semanticSymbolIdentity
}

func (s *Server) semanticNavigationTargetAt(document Document, offset int) (semanticNavigationTarget, semanticNavigationState) {
	if !s.contextAt(document, offset).goCode() {
		return semanticNavigationTarget{}, semanticNavigationNone
	}
	module, ok := s.parseSemanticModule(document)
	if !ok {
		return semanticNavigationTarget{}, semanticNavigationNone
	}
	node, ok := module.SemanticAtOffset(offset)
	if !ok || node.Object == nil {
		return semanticNavigationTarget{}, semanticNavigationNone
	}
	object := node.Object
	if object.Name() == "_" || object.Parent() == types.Universe {
		return semanticNavigationTarget{}, semanticNavigationBlocked
	}
	if _, packageName := object.(*types.PkgName); packageName {
		// Import aliases and GOX module aliases intentionally retain their
		// specialized navigation/rename semantics. In particular, GOX aliases
		// include the module declaration in references, which is not a Go object.
		return semanticNavigationTarget{}, semanticNavigationFallback
	}
	selector := semanticSelectorAtOffset(module, offset)
	if !semanticObjectAccessibleFrom(module, object) || !s.semanticHoverObjectAllowed(document, module, object, selector) {
		return semanticNavigationTarget{}, semanticNavigationBlocked
	}
	if symbol, metadata := s.semanticCompletionSourceSymbol(document, module, object); metadata && ordinarySourceComponent(symbol) {
		// Template component tags (<&...>) are a GOX surface rather than Go
		// identifiers. Their specialized resolver remains intentionally: mixing
		// those tag occurrences with Go AST identity would drop opening/closing
		// tag references.
		return semanticNavigationTarget{}, semanticNavigationFallback
	}
	identity, ok := semanticIdentityForObject(module, object)
	if !ok {
		return semanticNavigationTarget{}, semanticNavigationNone
	}
	return semanticNavigationTarget{Module: module, Object: object, Identity: identity}, semanticNavigationOwned
}

func semanticIdentityForObject(module *goxcompiler.Module, object types.Object) (semanticSymbolIdentity, bool) {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || object == nil {
		return semanticSymbolIdentity{}, false
	}
	kind := semanticObjectIdentityKind(object)
	if kind == "" {
		return semanticSymbolIdentity{}, false
	}

	if semanticObjectIsLocal(object) {
		origin, ok := semanticDefinitionOrigin(module, object)
		if !ok {
			return semanticSymbolIdentity{}, false
		}
		return semanticSymbolIdentity{
			Kind:       kind,
			Name:       object.Name(),
			LocalURI:   FileURI(origin.Source.URI),
			LocalStart: origin.Source.Range.Start,
			LocalEnd:   origin.Source.Range.End,
		}, true
	}

	packageIdentity := semanticPackageIdentity(module, object.Pkg())
	if packageIdentity == "" {
		return semanticSymbolIdentity{}, false
	}
	container := ""
	switch value := object.(type) {
	case *types.Func:
		container = semanticObjectContainer(value)
	case *types.Var:
		if value.IsField() {
			container = semanticFieldContainer(value)
			if container == "" {
				// A field of an anonymous/local struct cannot be named across package
				// boundaries. Keep its exact declaration identity instead.
				origin, ok := semanticDefinitionOrigin(module, object)
				if !ok {
					return semanticSymbolIdentity{}, false
				}
				return semanticSymbolIdentity{
					Kind:       kind,
					Name:       object.Name(),
					LocalURI:   FileURI(origin.Source.URI),
					LocalStart: origin.Source.Range.Start,
					LocalEnd:   origin.Source.Range.End,
				}, true
			}
		}
	}
	return semanticSymbolIdentity{Kind: kind, Name: object.Name(), Package: packageIdentity, Container: container}, true
}

func semanticObjectIdentityKind(object types.Object) string {
	switch value := object.(type) {
	case *types.Var:
		if value.IsField() {
			return "field"
		}
		return "var"
	case *types.Func:
		if signature, ok := value.Type().(*types.Signature); ok && signature.Recv() != nil {
			return "method"
		}
		return "func"
	case *types.TypeName:
		return "type"
	case *types.Const:
		return "const"
	case *types.Label:
		return "label"
	default:
		return ""
	}
}

func semanticObjectIsLocal(object types.Object) bool {
	if object == nil {
		return false
	}
	if variable, ok := object.(*types.Var); ok && variable.IsField() {
		return false
	}
	if function, ok := object.(*types.Func); ok {
		if signature, ok := function.Type().(*types.Signature); ok && signature.Recv() != nil {
			return false
		}
	}
	pkg := object.Pkg()
	if pkg == nil || object.Parent() == nil {
		return true
	}
	return object.Parent() != pkg.Scope()
}

func semanticPackageIdentity(module *goxcompiler.Module, pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	if module != nil && module.Semantic != nil && module.Semantic.Package == pkg {
		return "gox:" + semanticCleanPath(module.Path)
	}
	if source, ok := goxcompiler.SemanticGoxPackageSource(pkg.Path()); ok {
		return "gox:" + semanticCleanPath(source)
	}
	return "go:" + pkg.Path()
}

func semanticCleanPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return filepath.Clean(path)
}

func semanticFieldContainer(field *types.Var) string {
	if field == nil || field.Pkg() == nil {
		return ""
	}
	scope := field.Pkg().Scope()
	for _, name := range scope.Names() {
		typeName, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || typeName.Type() == nil {
			continue
		}
		base := types.Unalias(typeName.Type())
		named, ok := base.(*types.Named)
		if ok {
			base = named.Underlying()
		}
		structure, ok := base.Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for index := 0; index < structure.NumFields(); index++ {
			if structure.Field(index) == field {
				return typeName.Name()
			}
		}
	}
	return ""
}

func semanticDefinitionOrigin(module *goxcompiler.Module, object types.Object) (goxcompiler.NodeOrigin, bool) {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || module.Semantic.Origins == nil || object == nil {
		return goxcompiler.NodeOrigin{}, false
	}
	for ident, defined := range module.Semantic.Info.Defs {
		if ident == nil || defined != object {
			continue
		}
		origin, ok := module.Semantic.Origins.Get(ident)
		if ok && origin.Exact() {
			return origin, true
		}
	}
	return goxcompiler.NodeOrigin{}, false
}

func semanticIdentityMatches(module *goxcompiler.Module, object types.Object, target semanticSymbolIdentity) bool {
	identity, ok := semanticIdentityForObject(module, object)
	return ok && identity == target
}

func (s *Server) semanticDefinitionLocations(document Document, position Position) ([]Location, bool) {
	offset, err := document.Offset(position)
	if err != nil {
		return nil, false
	}
	target, state := s.semanticNavigationTargetAt(document, offset)
	switch state {
	case semanticNavigationBlocked:
		return []Location{}, true
	case semanticNavigationFallback, semanticNavigationNone:
		return nil, false
	}
	if strings.HasPrefix(target.Identity.Package, "go:") {
		// Real Go packages already map types.Object back to source through
		// GoPackageIndex. This remains semantic identity; only the final source
		// location lookup is package-index metadata.
		return nil, false
	}
	if location, ok := semanticObjectDefinitionLocation(document, target.Module, target.Object); ok {
		return []Location{location}, true
	}
	if strings.HasPrefix(target.Identity.Package, "gox:") {
		path := strings.TrimPrefix(target.Identity.Package, "gox:")
		candidate, ok := s.semanticDocumentForPath(path)
		if !ok {
			return []Location{}, true
		}
		module, ok := s.parseSemanticModule(candidate)
		if !ok {
			return []Location{}, true
		}
		for ident, object := range module.Semantic.Info.Defs {
			if ident == nil || object == nil || !semanticIdentityMatches(module, object, target.Identity) {
				continue
			}
			if location, ok := semanticIdentLocation(candidate, module, ident); ok {
				return []Location{location}, true
			}
		}
		return []Location{}, true
	}
	return []Location{}, true
}

func semanticObjectDefinitionLocation(document Document, module *goxcompiler.Module, object types.Object) (Location, bool) {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil {
		return Location{}, false
	}
	for ident, defined := range module.Semantic.Info.Defs {
		if ident == nil || defined != object {
			continue
		}
		return semanticIdentLocation(document, module, ident)
	}
	return Location{}, false
}

func semanticIdentLocation(document Document, module *goxcompiler.Module, ident *ast.Ident) (Location, bool) {
	if ident == nil || module == nil || module.Semantic == nil || module.Semantic.Origins == nil {
		return Location{}, false
	}
	origin, ok := module.Semantic.Origins.Get(ident)
	if !ok || !origin.Exact() {
		return Location{}, false
	}
	if semanticCleanPath(origin.Source.URI) != semanticCleanPath(document.Path) {
		return Location{}, false
	}
	start, startErr := document.Position(origin.Source.Range.Start)
	end, endErr := document.Position(origin.Source.Range.End)
	if startErr != nil || endErr != nil {
		return Location{}, false
	}
	return Location{URI: document.URI, Range: Range{Start: start, End: end}}, true
}

func (s *Server) semanticDocumentForPath(path string) (Document, bool) {
	path = semanticCleanPath(path)
	uri := FileURI(path)
	if document, ok := s.symbols.Document(uri); ok {
		return document, true
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Document{}, false
	}
	return Document{URI: uri, Path: path, Text: string(content)}, true
}

func (s *Server) semanticReferences(document Document, position Position, includeDeclaration bool) ([]Location, bool) {
	offset, err := document.Offset(position)
	if err != nil {
		return nil, false
	}
	target, state := s.semanticNavigationTargetAt(document, offset)
	switch state {
	case semanticNavigationBlocked:
		return []Location{}, true
	case semanticNavigationFallback, semanticNavigationNone:
		return nil, false
	}

	documents := s.semanticReferenceDocuments(document, target.Identity)
	locations := make([]Location, 0, 8)
	seen := make(map[Location]bool)
	for _, candidate := range documents {
		module, ok := s.parseSemanticModule(candidate)
		if !ok || module.Semantic == nil || module.Semantic.Info == nil {
			continue
		}
		ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok || ident == nil {
				return true
			}
			object := module.Semantic.Info.ObjectOf(ident)
			if object == nil || !semanticIdentityMatches(module, object, target.Identity) {
				return true
			}
			if !includeDeclaration {
				if defined := module.Semantic.Info.Defs[ident]; defined == object {
					return true
				}
			}
			location, ok := semanticIdentLocation(candidate, module, ident)
			if ok && !seen[location] {
				seen[location] = true
				locations = append(locations, location)
			}
			return true
		})
	}
	sort.SliceStable(locations, func(left, right int) bool {
		if locations[left].URI != locations[right].URI {
			return locations[left].URI < locations[right].URI
		}
		if locations[left].Range.Start.Line != locations[right].Range.Start.Line {
			return locations[left].Range.Start.Line < locations[right].Range.Start.Line
		}
		return locations[left].Range.Start.Character < locations[right].Range.Start.Character
	})
	return locations, true
}

func (s *Server) semanticReferenceDocuments(current Document, identity semanticSymbolIdentity) []Document {
	documents := s.symbols.Documents()
	seen := make(map[string]bool, len(documents)+2)
	for _, document := range documents {
		seen[document.URI] = true
	}
	if !seen[current.URI] {
		documents = append(documents, current)
		seen[current.URI] = true
	}
	if strings.HasPrefix(identity.Package, "gox:") {
		path := strings.TrimPrefix(identity.Package, "gox:")
		if target, ok := s.semanticDocumentForPath(path); ok && !seen[target.URI] {
			documents = append(documents, target)
		}
	}
	if identity.LocalURI != "" {
		filtered := documents[:0]
		for _, document := range documents {
			if document.URI == identity.LocalURI {
				filtered = append(filtered, document)
			}
		}
		documents = filtered
	}
	sort.SliceStable(documents, func(left, right int) bool { return documents[left].URI < documents[right].URI })
	return documents
}

func (s *Server) semanticRenamableAt(document Document, position Position) (bool, bool) {
	offset, err := document.Offset(position)
	if err != nil {
		return false, false
	}
	target, state := s.semanticNavigationTargetAt(document, offset)
	switch state {
	case semanticNavigationBlocked:
		return false, true
	case semanticNavigationFallback, semanticNavigationNone:
		return false, false
	}
	if target.Identity.Package != "" && strings.HasPrefix(target.Identity.Package, "go:") {
		return false, true
	}
	return true, true
}
