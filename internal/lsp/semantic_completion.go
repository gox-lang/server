package lsp

import (
	"go/types"
	"sort"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

// semanticCompletionQualifier preserves the aliases written in the current
// source file. go/types normally prints another package's declared name, but
// editor candidates must use the alias that is actually in scope (for example
// `library.User`, even when the imported package declares `package api`).
func semanticCompletionQualifier(module *goxcompiler.Module) types.Qualifier {
	aliases := make(map[string]string)
	if module != nil && module.Semantic != nil && module.Semantic.Info != nil {
		remember := func(object types.Object) {
			name, ok := object.(*types.PkgName)
			if !ok || name.Imported() == nil || name.Name() == "_" || name.Name() == "." {
				return
			}
			aliases[name.Imported().Path()] = name.Name()
		}
		for _, object := range module.Semantic.Info.Defs {
			remember(object)
		}
		for _, object := range module.Semantic.Info.Implicits {
			remember(object)
		}
	}
	current := (*types.Package)(nil)
	if module != nil && module.Semantic != nil {
		current = module.Semantic.Package
	}
	return func(other *types.Package) string {
		if other == nil || current != nil && other.Path() == current.Path() {
			return ""
		}
		if alias := aliases[other.Path()]; alias != "" {
			return alias
		}
		return other.Name()
	}
}

func (s *Server) semanticPackageMembers(document Document, module *goxcompiler.Module, packageName *types.PkgName, prefix string) []CompletionItem {
	if packageName == nil || packageName.Imported() == nil {
		return nil
	}
	pkg := packageName.Imported()
	qualifier := semanticCompletionQualifier(module)
	items := make([]CompletionItem, 0)
	for _, name := range pkg.Scope().Names() {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		object := pkg.Scope().Lookup(name)
		if object == nil || !object.Exported() || !s.semanticCompletionPackageObjectAllowed(document, object) || s.semanticCompletionObjectIsComponent(document, module, object) {
			continue
		}
		item := s.semanticPackageObjectCompletionItem(document, module, object, qualifier)
		if isGoxSDKPackage(pkg.Path()) {
			item.Detail += " — GOX SDK public API"
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return items
}

func (s *Server) semanticCompletionPackageObjectAllowed(document Document, object types.Object) bool {
	if object == nil || object.Pkg() == nil || !isGoxSDKPackage(object.Pkg().Path()) {
		return true
	}
	packageInfo, err := s.goPackages.Load(s.projectRoot(document.Path), object.Pkg().Path())
	if err != nil {
		return false
	}
	for _, exported := range packageInfo.Exports {
		if exported.Name == object.Name() {
			return true
		}
	}
	return false
}

func (s *Server) semanticCompletionMemberAllowed(document Document, object types.Object, container string) bool {
	if object == nil || object.Pkg() == nil || !isGoxSDKPackage(object.Pkg().Path()) {
		return true
	}
	packageInfo, err := s.goPackages.Load(s.projectRoot(document.Path), object.Pkg().Path())
	if err != nil {
		return false
	}
	for _, exported := range packageInfo.Exports {
		if exported.Name != container {
			continue
		}
		for _, member := range exported.Members {
			if member.Name == object.Name() {
				return true
			}
		}
		return false
	}
	return false
}

func (s *Server) semanticPackageObjectCompletionItem(document Document, module *goxcompiler.Module, object types.Object, qualifier types.Qualifier) CompletionItem {
	item := s.semanticObjectCompletionItem(document, module, object, semanticObjectCompletionKind(object), qualifier)
	if _, ok := s.semanticCompletionSourceSymbol(document, module, object); ok {
		// GOX marker returns are lowered for go/types, so imported .gox
		// declarations keep the source signature for editor presentation.
		return item
	}
	// Go package member completion historically presents the declaration
	// without repeating the qualifier because the user already typed `pkg.`.
	item.Detail = semanticPackageObjectDetail(object, qualifier)
	return item
}

func semanticPackageObjectDetail(object types.Object, qualifier types.Qualifier) string {
	switch value := object.(type) {
	case *types.Func:
		if signature, ok := value.Type().(*types.Signature); ok {
			return "func " + value.Name() + strings.TrimPrefix(types.TypeString(signature, qualifier), "func")
		}
	case *types.Var:
		return "var " + value.Name() + " " + types.TypeString(value.Type(), qualifier)
	case *types.Const:
		return "const " + value.Name() + " " + types.TypeString(value.Type(), qualifier) + " = " + value.Val().ExactString()
	case *types.TypeName:
		return goTypeNameDetail(value, qualifier)
	}
	return goObjectDetail(object, qualifier)
}

func (s *Server) semanticObjectCompletionItem(document Document, module *goxcompiler.Module, object types.Object, kind int, qualifier types.Qualifier) CompletionItem {
	if symbol, ok := s.semanticCompletionSourceSymbol(document, module, object); ok {
		item := sourceSymbolCompletionItem(symbol, symbol.Detail, symbol.Name, symbol.Name)
		typeName, results, parameters := goObjectCompletionTypes(object, qualifier)
		if !sourceSymbolHasGoxMarkerResult(symbol) {
			if typeName != "" {
				item.CandidateType = typeName
			}
			if _, function := object.(*types.Func); function {
				item.CandidateResults = results
			}
		}
		item.CandidateParameters = parameters
		if typeNameObject, ok := object.(*types.TypeName); ok {
			if underlying, alias := goObjectUnderlyingType(typeNameObject, qualifier); underlying != "" {
				item.CandidateUnderlying, item.CandidateAlias = underlying, alias
			}
		}
		return item
	}

	typeName, results, parameters := goObjectCompletionTypes(object, qualifier)
	item := CompletionItem{
		Label: object.Name(), Kind: kind, Detail: goObjectDetail(object, qualifier),
		InsertText: object.Name(), SortText: object.Name(), CandidateType: typeName,
		CandidateResults: results, CandidateParameters: parameters,
	}
	if typeNameObject, ok := object.(*types.TypeName); ok {
		item.CandidateUnderlying, item.CandidateAlias = goObjectUnderlyingType(typeNameObject, qualifier)
	}
	return item
}

func sourceSymbolHasGoxMarkerResult(symbol goxcompiler.SourceSymbol) bool {
	marker := func(value string) bool {
		value = strings.TrimSpace(value)
		if value == "" {
			return false
		}
		if index := strings.LastIndexByte(value, '.'); index >= 0 {
			value = value[index+1:]
		}
		switch value {
		case "Page", "Action", "Component", "WebSocket", "Channel", "Handler", "Html", "HTML", "Text", "JSON", "API", "Binary", "Blob", "File", "Image", "Video", "EventReply":
			return true
		default:
			return false
		}
	}
	if marker(symbol.Type) {
		return true
	}
	for _, result := range symbol.Results {
		if marker(result) {
			return true
		}
	}
	return false
}

func semanticObjectCompletionKind(object types.Object) int {
	switch value := object.(type) {
	case *types.Func:
		if signature, ok := value.Type().(*types.Signature); ok && signature.Recv() != nil {
			return 2
		}
		return 3
	case *types.Var:
		if value.IsField() {
			return 5
		}
		return 6
	case *types.Const:
		return 21
	case *types.TypeName:
		switch value.Type().Underlying().(type) {
		case *types.Struct:
			return 22
		case *types.Interface:
			return 8
		default:
			return 7
		}
	case *types.PkgName:
		return 9
	}
	return 18
}

// semanticCompletionSourceSymbol is metadata only. go/types remains the
// authority for object identity, visibility and types; SourceSymbol is consulted
// solely to preserve GOX-specific presentation such as marker returns and the
// Component boundary that semantic lowering intentionally erases.
func (s *Server) semanticCompletionSourceSymbol(document Document, module *goxcompiler.Module, object types.Object) (goxcompiler.SourceSymbol, bool) {
	if symbol, ok := semanticSourceSymbol(module, object); ok {
		return symbol, true
	}
	if object == nil || object.Pkg() == nil {
		return goxcompiler.SourceSymbol{}, false
	}
	path, ok := goxcompiler.SemanticGoxPackageSource(object.Pkg().Path())
	if !ok {
		return goxcompiler.SourceSymbol{}, false
	}
	uri := FileURI(path)
	container := semanticObjectContainer(object)
	for _, indexed := range s.symbols.DocumentSymbols(uri) {
		symbol := indexed.Symbol
		if symbol.Name == object.Name() && symbol.Container == container {
			return symbol, true
		}
	}
	return goxcompiler.SourceSymbol{}, false
}

func semanticObjectContainer(object types.Object) string {
	function, ok := object.(*types.Func)
	if !ok {
		return ""
	}
	signature, ok := function.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return ""
	}
	t := signature.Recv().Type()
	if pointer, ok := t.(*types.Pointer); ok {
		t = pointer.Elem()
	}
	t = types.Unalias(t)
	if named, ok := t.(*types.Named); ok && named.Obj() != nil {
		return named.Obj().Name()
	}
	return ""
}

func (s *Server) semanticCompletionObjectIsComponent(document Document, module *goxcompiler.Module, object types.Object) bool {
	if symbol, ok := s.semanticCompletionSourceSymbol(document, module, object); ok {
		return ordinarySourceComponent(symbol)
	}
	if object == nil {
		return false
	}
	qualifier := semanticCompletionQualifier(module)
	typeName, results, _ := goObjectCompletionTypes(object, qualifier)
	return completionTypeIsComponent(typeName) || len(results) == 1 && completionTypeIsComponent(results[0])
}

func (s *Server) semanticGoxCompletionMetadataAvailable(module *goxcompiler.Module, object types.Object) bool {
	if object == nil {
		return true
	}
	packagePath := ""
	if name, ok := object.(*types.PkgName); ok && name.Imported() != nil {
		packagePath = name.Imported().Path()
	} else if object.Pkg() != nil {
		packagePath = object.Pkg().Path()
	}
	path, ok := goxcompiler.SemanticGoxPackageSource(packagePath)
	if !ok {
		return true
	}
	_, found := s.symbols.Document(FileURI(path))
	return found
}

func semanticNamedTypeObject(t types.Type) *types.TypeName {
	for t != nil {
		t = types.Unalias(t)
		switch value := t.(type) {
		case *types.Pointer:
			t = value.Elem()
		case *types.Named:
			return value.Obj()
		default:
			return nil
		}
	}
	return nil
}

func semanticCompletionTypeAtModule(module *goxcompiler.Module, offset int) (string, bool) {
	if module == nil {
		return "", false
	}
	node, ok := module.SemanticAtOffset(offset)
	if !ok || node.TypeValue.Type == nil {
		return "", false
	}
	return types.TypeString(node.TypeValue.Type, semanticCompletionQualifier(module)), true
}

func semanticCompletionTypeName(module *goxcompiler.Module, typeName string) *types.TypeName {
	if module == nil || module.Semantic == nil || module.Semantic.Package == nil || module.Semantic.Info == nil {
		return nil
	}
	typeName = strings.TrimSpace(typeName)
	for strings.HasPrefix(typeName, "*") {
		typeName = strings.TrimSpace(strings.TrimPrefix(typeName, "*"))
	}
	if qualifier, name, ok := importedNamedType(typeName); ok {
		var imported *types.Package
		for _, object := range module.Semantic.Info.Defs {
			pkgName, ok := object.(*types.PkgName)
			if ok && pkgName.Name() == qualifier && pkgName.Imported() != nil {
				imported = pkgName.Imported()
				break
			}
		}
		if imported == nil {
			for _, object := range module.Semantic.Info.Implicits {
				pkgName, ok := object.(*types.PkgName)
				if ok && pkgName.Name() == qualifier && pkgName.Imported() != nil {
					imported = pkgName.Imported()
					break
				}
			}
		}
		if imported == nil {
			return nil
		}
		object, _ := imported.Scope().Lookup(name).(*types.TypeName)
		return object
	}
	object, _ := module.Semantic.Package.Scope().Lookup(typeName).(*types.TypeName)
	return object
}
