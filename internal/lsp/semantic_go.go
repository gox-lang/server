package lsp

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) parseSemanticModule(document Document) (*goxcompiler.Module, bool) {
	return s.semanticModule(document)
}

func semanticQualifier(pkg *types.Package) types.Qualifier {
	return func(other *types.Package) string {
		if other == nil || pkg != nil && other.Path() == pkg.Path() {
			return ""
		}
		return other.Name()
	}
}

func (s *Server) semanticHoverAt(document Document, offset int) *Hover {
	module, ok := s.parseSemanticModule(document)
	if !ok {
		return nil
	}
	node, ok := module.SemanticAtOffset(offset)
	if !ok || node.Object == nil {
		return nil
	}
	object := node.Object
	if object.Name() == "_" || !semanticHoverTypeUsable(object.Type()) {
		return nil
	}

	// Package aliases have a richer namespace presentation (including GOX SDK
	// grouping and import metadata). go/types still establishes that this source
	// identifier is the imported package; presentation remains specialized.
	if _, packageName := object.(*types.PkgName); packageName {
		return nil
	}
	selector := semanticSelectorAtOffset(module, offset)
	if !semanticObjectAccessibleFrom(module, object) || !s.semanticHoverObjectAllowed(document, module, object, selector) {
		return nil
	}
	if object.Parent() == types.Universe {
		if builtin, ok := goBuiltinSignatureFor(object.Name()); ok {
			return hoverForDetail(builtin.Label, "")
		}
	}

	// SourceSymbol is metadata only. It preserves GOX-specific presentation
	// that semantic lowering intentionally erases (marker returns, receiver
	// spelling, etc.), while object identity and visibility come from go/types.
	if symbol, ok := s.semanticCompletionSourceSymbol(document, module, object); ok {
		return hoverForDetail(symbol.Detail, symbol.Container)
	}

	qualifier := semanticCompletionQualifier(module)
	detail := goObjectDetail(object, qualifier)
	if selector != nil && semanticSelectorUsesPackageName(module, selector) {
		detail = semanticPackageObjectDetail(object, semanticDeclarationQualifier(module, object.Pkg()))
	}
	return hoverForDetail(detail, "")
}

func semanticHoverTypeUsable(value types.Type) bool {
	if value == nil {
		return false
	}
	return !strings.Contains(types.TypeString(value, nil), "invalid type")
}

func semanticSelectorAtOffset(module *goxcompiler.Module, offset int) *ast.SelectorExpr {
	if module == nil || module.Semantic == nil || module.Semantic.File == nil || module.Semantic.Origins == nil {
		return nil
	}
	var best *ast.SelectorExpr
	bestLen := int(^uint(0) >> 1)
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil {
			return true
		}
		origin, ok := module.Semantic.Origins.Get(selector.Sel)
		if !ok || !origin.Exact() || origin.Source.URI != module.Path {
			return true
		}
		r := origin.Source.Range
		if offset < r.Start || offset > r.End {
			return true
		}
		if length := r.End - r.Start; length < bestLen {
			best, bestLen = selector, length
		}
		return true
	})
	return best
}

func (s *Server) semanticHoverObjectAllowed(document Document, module *goxcompiler.Module, object types.Object, selector *ast.SelectorExpr) bool {
	if object == nil || object.Pkg() == nil || !isGoxSDKPackage(object.Pkg().Path()) {
		return true
	}
	container := semanticObjectContainer(object)
	if container == "" && selector != nil && module != nil && module.Semantic != nil && module.Semantic.Info != nil {
		if tv, ok := module.Semantic.Info.Types[selector.X]; ok && tv.Type != nil {
			if named := semanticNamedTypeObject(tv.Type); named != nil {
				container = named.Name()
			}
		}
	}
	if container != "" {
		return s.semanticCompletionMemberAllowed(document, object, container)
	}
	return s.semanticCompletionPackageObjectAllowed(document, object)
}

func semanticDeclarationQualifier(module *goxcompiler.Module, owner *types.Package) types.Qualifier {
	base := semanticCompletionQualifier(module)
	return func(other *types.Package) string {
		if other == nil || owner != nil && other.Path() == owner.Path() {
			return ""
		}
		return base(other)
	}
}

func (s *Server) semanticCompletionItems(document Document, offset int, prefix string) ([]CompletionItem, bool) {
	repaired, repairedOffset := repairIncompleteSelectors(document.Text, offset)
	var module *goxcompiler.Module
	var err error
	if repaired == document.Text {
		module, _ = s.parseSemanticModule(document)
	} else {
		module, err = s.parseTransientModule(document, repaired)
	}
	if err != nil || module == nil || module.Semantic == nil || module.Semantic.Info == nil {
		return nil, false
	}
	const placeholder = "__gox_completion"
	var receiver ast.Expr
	bestDistance := int(^uint(0) >> 1)
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil {
			return true
		}
		origin, ok := module.Semantic.Origins.Get(selector.Sel)
		if !ok || origin.Source.URI != module.Path {
			return true
		}
		r := origin.Source.Range
		if selector.Sel.Name == placeholder {
			// A dangling selector (`value.`) is repaired transiently with the
			// placeholder. Its origin is anchored near the live cursor.
		} else {
			// A partial selector (`value.Na`) is valid syntax, so the canonical
			// semantic AST already contains it. Only the selector under the
			// current cursor may own this completion request.
			if prefix == "" || !strings.HasPrefix(selector.Sel.Name, prefix) || repairedOffset < r.Start || repairedOffset > r.End {
				return true
			}
		}
		distance := repairedOffset - r.Start
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			receiver, bestDistance = selector.X, distance
		}
		return true
	})
	if receiver == nil {
		return nil, false
	}
	if ident, ok := receiver.(*ast.Ident); ok {
		if packageName, ok := module.Semantic.Info.ObjectOf(ident).(*types.PkgName); ok && packageName.Imported() != nil {
			// A malformed/incomplete editor selector can still leave go/types with
			// a placeholder PkgName after an import failed. For GOX imports that is
			// not enough to own completion: only the project-aware importer from
			// Phase 6 gives the package its canonical synthetic path. Otherwise the
			// established indexed module resolver remains the compatibility fallback
			// for this transient edit. This prevents an empty fake package scope from
			// swallowing valid completions such as widgets.Helper.
			if _, targetURI, goxImport := s.resolveImportedModule(document, ident.Name); goxImport {
				sourcePath, semanticGox := goxcompiler.SemanticGoxPackageSource(packageName.Imported().Path())
				if !semanticGox || FileURI(sourcePath) != targetURI {
					return nil, false
				}
			}
			if !s.semanticGoxCompletionMetadataAvailable(module, packageName) {
				return nil, false
			}
			return s.semanticPackageMembers(document, module, packageName, prefix), true
		}
	}
	tv, ok := module.Semantic.Info.Types[receiver]
	if !ok || tv.Type == nil {
		return nil, false
	}
	if object := semanticNamedTypeObject(tv.Type); object != nil && !s.semanticGoxCompletionMetadataAvailable(module, object) {
		return nil, false
	}
	return s.semanticMembers(document, module, tv.Type, tv.Addressable(), prefix, semanticCompletionQualifier(module)), true
}

func (s *Server) semanticMembers(document Document, module *goxcompiler.Module, t types.Type, addressable bool, prefix string, qualifier types.Qualifier) []CompletionItem {
	items := map[string]CompletionItem{}
	container := ""
	if object := semanticNamedTypeObject(t); object != nil {
		container = object.Name()
	}
	appendMethods := func(methodSet *types.MethodSet) {
		for i := 0; i < methodSet.Len(); i++ {
			fn, ok := methodSet.At(i).Obj().(*types.Func)
			if !ok || !strings.HasPrefix(fn.Name(), prefix) || !semanticObjectAccessibleFrom(module, fn) || !s.semanticCompletionMemberAllowed(document, fn, container) {
				continue
			}
			if s.semanticCompletionObjectIsComponent(document, module, fn) {
				continue
			}
			items[fn.Name()] = s.semanticObjectCompletionItem(document, module, fn, 2, qualifier)
		}
	}
	appendMethods(types.NewMethodSet(t))
	// Go permits x.m as shorthand for (&x).m when x is addressable. Method-set
	// completion must mirror that selector rule or pointer-receiver methods
	// disappear for ordinary local variables.
	if addressable {
		if _, pointer := t.(*types.Pointer); !pointer {
			appendMethods(types.NewMethodSet(types.NewPointer(t)))
		}
	}
	base := t
	if p, ok := base.(*types.Pointer); ok {
		base = p.Elem()
	}
	base = types.Unalias(base)
	if named, ok := base.(*types.Named); ok {
		base = named.Underlying()
	}
	if st, ok := base.(*types.Struct); ok {
		for i := 0; i < st.NumFields(); i++ {
			field := st.Field(i)
			if !strings.HasPrefix(field.Name(), prefix) || !semanticObjectAccessibleFrom(module, field) || !s.semanticCompletionMemberAllowed(document, field, container) {
				continue
			}
			if s.semanticCompletionObjectIsComponent(document, module, field) {
				continue
			}
			items[field.Name()] = s.semanticObjectCompletionItem(document, module, field, 5, qualifier)
		}
	}
	result := make([]CompletionItem, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Label < result[j].Label })
	return result
}

func (s *Server) semanticSignatureCandidate(document Document, openOffset int) (signatureCandidate, bool) {
	module, ok := s.parseSemanticModule(document)
	if !ok {
		return signatureCandidate{}, false
	}
	call := semanticCallAtOffset(module, openOffset)
	if call == nil {
		return signatureCandidate{}, false
	}
	object := semanticCallableObject(module, call.Fun)
	if !semanticObjectAccessibleFrom(module, object) {
		return signatureCandidate{}, false
	}
	selector, _ := call.Fun.(*ast.SelectorExpr)
	if object != nil && !s.semanticHoverObjectAllowed(document, module, object, selector) {
		return signatureCandidate{}, false
	}
	if object != nil {
		if object.Parent() == types.Universe {
			if candidate, ok := goBuiltinSignatureCandidate(object.Name()); ok {
				return candidate, true
			}
		}
		if symbol, ok := s.semanticCompletionSourceSymbol(document, module, object); ok {
			if symbol.Kind == goxcompiler.SymbolFunction || symbol.Kind == goxcompiler.SymbolMethod {
				return signatureCandidateFromSourceSymbol(symbol), true
			}
		}
	}
	tv, ok := module.Semantic.Info.Types[call.Fun]
	if !ok || tv.Type == nil {
		return signatureCandidate{}, false
	}
	sig, ok := tv.Type.(*types.Signature)
	if !ok {
		return signatureCandidate{}, false
	}
	name := semanticCallableName(call.Fun)
	owner := (*types.Package)(nil)
	if object != nil {
		owner = object.Pkg()
	}
	qualifier := semanticDeclarationQualifier(module, owner)
	params := make([]goxcompiler.SourceParameter, 0, sig.Params().Len())
	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		pt := types.TypeString(p.Type(), qualifier)
		if sig.Variadic() && i == sig.Params().Len()-1 {
			if sl, ok := p.Type().(*types.Slice); ok {
				pt = "..." + types.TypeString(sl.Elem(), qualifier)
			}
		}
		params = append(params, goxcompiler.SourceParameter{Name: p.Name(), Type: pt})
	}
	results := make([]string, 0, sig.Results().Len())
	for i := 0; i < sig.Results().Len(); i++ {
		results = append(results, types.TypeString(sig.Results().At(i).Type(), qualifier))
	}
	label := "func " + name + strings.TrimPrefix(types.TypeString(sig, qualifier), "func")
	resultType := ""
	if len(results) == 1 {
		resultType = results[0]
	}
	return signatureCandidate{
		Label:      label,
		Parameters: params,
		Results:    results,
		Type:       resultType,
		Component:  semanticSignatureReturnsComponent(sig) || semanticObjectIsSourceComponent(s, document, module, object),
	}, true
}

func semanticCallAtOffset(module *goxcompiler.Module, offset int) *ast.CallExpr {
	if module == nil || module.Semantic == nil {
		return nil
	}
	if node, ok := module.SemanticAtOffset(offset); ok {
		if call, ok := node.Node.(*ast.CallExpr); ok {
			return call
		}
	}
	var call *ast.CallExpr
	bestLen := int(^uint(0) >> 1)
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		candidate, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		origin, ok := module.Semantic.Origins.Get(candidate)
		if !ok || !origin.Exact() || origin.Source.URI != module.Path {
			return true
		}
		r := origin.Source.Range
		if offset < r.Start || offset > r.End {
			return true
		}
		if length := r.End - r.Start; length < bestLen {
			call, bestLen = candidate, length
		}
		return true
	})
	return call
}

func semanticSignatureReturnsComponent(sig *types.Signature) bool {
	if sig == nil || sig.Results() == nil || sig.Results().Len() != 1 {
		return false
	}
	return semanticTypeIsGoxComponent(sig.Results().At(0).Type())
}

func semanticTypeIsGoxComponent(t types.Type) bool {
	for t != nil {
		t = types.Unalias(t)
		switch value := t.(type) {
		case *types.Pointer:
			t = value.Elem()
		case *types.Named:
			object := value.Obj()
			return object != nil && object.Name() == "Component" && object.Pkg() != nil && isGoxSDKPackage(object.Pkg().Path())
		default:
			return false
		}
	}
	return false
}

func semanticObjectIsSourceComponent(s *Server, document Document, module *goxcompiler.Module, object types.Object) bool {
	if s == nil || object == nil {
		return false
	}
	if symbol, ok := s.semanticCompletionSourceSymbol(document, module, object); ok {
		return ordinarySourceComponent(symbol)
	}
	return false
}

func semanticForeignGoxObject(module *goxcompiler.Module, object types.Object) bool {
	if object == nil || object.Pkg() == nil {
		return false
	}
	return semanticGoxDependencyPackage(module, object.Pkg().Path())
}

func semanticGoxDependencyPackage(_ *goxcompiler.Module, packagePath string) bool {
	if packagePath == "" {
		return false
	}
	_, ok := goxcompiler.SemanticGoxPackageSource(packagePath)
	return ok
}

func semanticObjectAccessibleFrom(module *goxcompiler.Module, object types.Object) bool {
	if object == nil || object.Pkg() == nil || module == nil || module.Semantic == nil || module.Semantic.Package == nil {
		return true
	}
	if object.Pkg().Path() == module.Semantic.Package.Path() {
		return true
	}
	return object.Exported()
}

func semanticSelectorUsesPackageName(module *goxcompiler.Module, selector *ast.SelectorExpr) bool {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || selector == nil {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = module.Semantic.Info.ObjectOf(ident).(*types.PkgName)
	return ok
}

func semanticCallableObject(module *goxcompiler.Module, expression ast.Expr) types.Object {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil {
		return nil
	}
	switch value := expression.(type) {
	case *ast.Ident:
		return module.Semantic.Info.ObjectOf(value)
	case *ast.SelectorExpr:
		return module.Semantic.Info.ObjectOf(value.Sel)
	default:
		return nil
	}
}

func semanticSourceSymbol(module *goxcompiler.Module, object types.Object) (goxcompiler.SourceSymbol, bool) {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || object == nil {
		return goxcompiler.SourceSymbol{}, false
	}
	for ident, defined := range module.Semantic.Info.Defs {
		if defined != object || ident == nil {
			continue
		}
		origin, ok := module.Semantic.Origins.Get(ident)
		if !ok || !origin.Exact() || origin.Source.URI != module.Path {
			return goxcompiler.SourceSymbol{}, false
		}
		for _, symbol := range module.Symbols {
			if symbol.Name == ident.Name && symbol.Source.URI == origin.Source.URI && symbol.Source.Range.Start == origin.Source.Range.Start {
				return symbol, true
			}
		}
		return goxcompiler.SourceSymbol{}, false
	}
	return goxcompiler.SourceSymbol{}, false
}

func semanticCallCandidate(module *goxcompiler.Module, call *ast.CallExpr) (signatureCandidate, bool) {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || call == nil {
		return signatureCandidate{}, false
	}
	object := semanticCallableObject(module, call.Fun)
	if !semanticObjectAccessibleFrom(module, object) || semanticForeignGoxObject(module, object) {
		return signatureCandidate{}, false
	}
	tv, ok := module.Semantic.Info.Types[call.Fun]
	if !ok || tv.Type == nil {
		return signatureCandidate{}, false
	}
	sig, ok := tv.Type.(*types.Signature)
	if !ok {
		return signatureCandidate{}, false
	}
	qualifier := semanticQualifier(module.Semantic.Package)
	name := semanticCallableName(call.Fun)
	params := make([]goxcompiler.SourceParameter, 0, sig.Params().Len())
	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		pt := types.TypeString(p.Type(), qualifier)
		if sig.Variadic() && i == sig.Params().Len()-1 {
			if slice, ok := p.Type().(*types.Slice); ok {
				pt = "..." + types.TypeString(slice.Elem(), qualifier)
			}
		}
		params = append(params, goxcompiler.SourceParameter{Name: p.Name(), Type: pt})
	}
	results := make([]string, 0, sig.Results().Len())
	for i := 0; i < sig.Results().Len(); i++ {
		results = append(results, types.TypeString(sig.Results().At(i).Type(), qualifier))
	}
	resultType := ""
	if len(results) == 1 {
		resultType = results[0]
	}
	label := "func " + name + strings.TrimPrefix(types.TypeString(sig, qualifier), "func")
	return signatureCandidate{Label: label, Parameters: params, Results: results, Type: resultType}, true
}

func semanticCallableName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	}
	return "call"
}

func semanticCallContext(module *goxcompiler.Module, call *ast.CallExpr) signatureCallContext {
	name := semanticCallableName(call.Fun)
	qualifier := ""
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		if ident, ok := selector.X.(*ast.Ident); ok {
			qualifier = ident.Name
		}
	}
	open := 0
	if origin, ok := module.Semantic.Origins.Get(call); ok && origin.Exact() {
		open = origin.Source.Range.Start
	}
	return signatureCallContext{Qualifier: qualifier, Name: name, OpenOffset: open}
}

func (s *Server) semanticVisibleCompletionItems(document Document, module *goxcompiler.Module, offset int, prefix string) ([]CompletionItem, bool) {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil {
		return nil, false
	}
	pos, ok := semanticCompletionPosition(module, offset)
	if !ok {
		return nil, false
	}
	var scope *types.Scope
	var bestSpan token.Pos
	for _, candidate := range module.Semantic.Info.Scopes {
		if candidate == nil || !candidate.Contains(pos) {
			continue
		}
		span := candidate.End() - candidate.Pos()
		if scope == nil || span < bestSpan {
			scope, bestSpan = candidate, span
		}
	}
	if scope == nil {
		return nil, false
	}

	componentLocals := make(map[string]bool)
	for _, local := range visibleLocalSymbols(module.LocalSymbols, offset) {
		if completionTypeIsComponent(local.Type) {
			componentLocals[local.Name] = true
		}
	}
	// Inline/template component locals are also recorded as source symbols.
	// Their semantic projection may intentionally lower the value type, so the
	// source marker remains metadata for the ordinary-completion boundary.
	for _, symbol := range module.Symbols {
		if symbol.Kind == goxcompiler.SymbolVariable && completionTypeIsComponent(symbol.Type) {
			componentLocals[symbol.Name] = true
		}
	}
	qualifier := semanticCompletionQualifier(module)
	items := map[string]CompletionItem{}
	packageScope := (*types.Scope)(nil)
	if module.Semantic.Package != nil {
		packageScope = module.Semantic.Package.Scope()
	}
	for current := scope; current != nil && current != types.Universe; current = current.Parent() {
		for _, name := range current.Names() {
			if name == "_" || !strings.HasPrefix(name, prefix) {
				continue
			}
			if _, exists := items[name]; exists {
				continue
			}
			object := current.Lookup(name)
			if object == nil {
				continue
			}
			// Package declarations are visible throughout the package. Local
			// declarations keep Go's declaration-order rule.
			if current != packageScope && object.Pos().IsValid() && object.Pos() > pos {
				continue
			}
			if componentLocals[name] || s.semanticCompletionObjectIsComponent(document, module, object) {
				continue
			}
			switch value := object.(type) {
			case *types.Var, *types.Const, *types.Func, *types.TypeName:
				item := s.semanticObjectCompletionItem(document, module, object, semanticObjectCompletionKind(object), qualifier)
				item.SortText = "0_" + name
				items[name] = item
			case *types.PkgName:
				if value.Name() == "_" || value.Name() == "." || value.Imported() == nil {
					continue
				}
				detail := "Go package from " + value.Imported().Path()
				if path, ok := goxcompiler.SemanticGoxPackageSource(value.Imported().Path()); ok {
					detail = "GOX module from " + path
				}
				items[name] = CompletionItem{Label: name, Kind: 9, Detail: detail, InsertText: name, SortText: "2_" + name}
			}
		}
	}
	result := make([]CompletionItem, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SortText != result[j].SortText {
			return result[i].SortText < result[j].SortText
		}
		return result[i].Label < result[j].Label
	})
	return result, true
}

func semanticCompletionPosition(module *goxcompiler.Module, offset int) (token.Pos, bool) {
	if module == nil || module.Semantic == nil || module.Semantic.File == nil || module.Semantic.Origins == nil {
		return token.NoPos, false
	}
	var pos token.Pos
	bestLen := int(^uint(0) >> 1)
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		origin, ok := module.Semantic.Origins.Get(node)
		if !ok || !origin.Exact() || origin.Source.URI != module.Path {
			return true
		}
		r := origin.Source.Range
		if offset < r.Start || offset > r.End {
			return true
		}
		if length := r.End - r.Start; length < bestLen {
			pos, bestLen = node.Pos(), length
		}
		return true
	})
	if pos.IsValid() {
		return pos, true
	}
	if module.Semantic.SourceMap == nil || module.Semantic.FileSet == nil || module.Semantic.File == nil {
		return token.NoPos, false
	}
	positions := module.Semantic.SourceMap.GeneratedPositions(module.Path, offset)
	if len(positions) == 0 && offset > 0 {
		positions = module.Semantic.SourceMap.GeneratedPositions(module.Path, offset-1)
	}
	if len(positions) == 0 {
		return token.NoPos, false
	}
	file := module.Semantic.FileSet.File(module.Semantic.File.Pos())
	if file == nil {
		return token.NoPos, false
	}
	generated := positions[0].Offset
	if generated < 0 || generated > file.Size() {
		return token.NoPos, false
	}
	return file.Pos(generated), true
}

func semanticIdentifierCompletionContext(module *goxcompiler.Module, offset int, prefix string) bool {
	if module == nil || module.Semantic == nil || module.Semantic.File == nil || module.Semantic.Origins == nil || prefix == "" {
		return false
	}
	found := false
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		if found || node == nil {
			return !found
		}
		ident, ok := node.(*ast.Ident)
		if !ok || !strings.HasPrefix(ident.Name, prefix) {
			return true
		}
		origin, ok := module.Semantic.Origins.Get(ident)
		if !ok || !origin.Exact() || origin.Source.URI != module.Path {
			return true
		}
		range_ := origin.Source.Range
		if range_.Start <= offset && offset <= range_.End {
			found = true
		}
		return !found
	})
	return found
}
