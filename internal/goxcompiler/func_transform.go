package gox

import (
	"go/ast"
)

type GoxReturnKind string

const (
	GoxReturnNone       GoxReturnKind = ""
	GoxReturnHTML       GoxReturnKind = "html"
	GoxReturnText       GoxReturnKind = "text"
	GoxReturnJSON       GoxReturnKind = "json"
	GoxReturnBinary     GoxReturnKind = "binary"
	GoxReturnAction     GoxReturnKind = "action"
	GoxReturnWebSocket  GoxReturnKind = "websocket"
	GoxReturnChannel    GoxReturnKind = "channel"
	GoxReturnEventReply GoxReturnKind = "event-reply"
)

func TransformGoxFunctionsWithOrigins(file *ast.File, origins *OriginTable) error {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		kind := detectGoxReturnKind(file, fn)
		if kind == GoxReturnNone {
			continue
		}

		returnName := detectGoxReturnName(file, fn)
		originSource := goxFunctionSignatureOrigin(fn, origins)

		switch kind {
		case GoxReturnHTML:
			if returnName == "Page" || returnName == "Handler" {
				ensurePageContextParam(fn, origins, originSource)
			} else if returnName == "Component" {
				ensureComponentWriterParam(fn, origins, originSource)
			}
			stripFuncReturn(fn)

		case GoxReturnAction:
			ensureContextParam(fn, false, origins, originSource)
			transformActionReturn(fn)

		case GoxReturnWebSocket, GoxReturnChannel:
			stripFuncReturn(fn)
		}
	}

	// Component values are render callbacks at runtime. Keep this conversion
	// separate from the source-level semantic projection used by the editor.
	ast.Inspect(file, func(node ast.Node) bool {
		lowerValueType := func(original ast.Expr) ast.Expr {
			path, name, ok := goxSourceTypeIdentity(file, original)
			if !ok || path != goxSDKImportPath || name != "Component" {
				return original
			}
			lowered := componentCallbackType()
			if origins != nil {
				origins.InheritTree(lowered, original)
			}
			return lowered
		}
		if value, ok := node.(*ast.ValueSpec); ok {
			value.Type = lowerValueType(value.Type)
		}
		var fields []*ast.Field
		if typ, ok := node.(*ast.FuncType); ok && typ.Params != nil {
			fields = typ.Params.List
		}
		for _, field := range fields {
			field.Type = lowerValueType(field.Type)
		}
		return true
	})
	return nil
}

func componentCallbackType() *ast.FuncType {
	return &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{
		Type: &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("Writer")},
	}}}}
}

func goxFunctionSignatureOrigin(fn *ast.FuncDecl, origins *OriginTable) ast.Node {
	if fn == nil || origins == nil {
		return nil
	}
	if fn.Type != nil && fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			if _, ok := origins.Get(field); ok {
				return field
			}
			if field != nil {
				if _, ok := origins.Get(field.Type); ok {
					return field.Type
				}
			}
		}
	}
	if fn.Name != nil {
		if _, ok := origins.Get(fn.Name); ok {
			return fn.Name
		}
	}
	return nil
}

func detectGoxReturnKind(file *ast.File, fn *ast.FuncDecl) GoxReturnKind {
	return goxReturnKindFromName(detectGoxReturnName(file, fn))
}

func detectGoxReturnName(file *ast.File, fn *ast.FuncDecl) string {
	if fn == nil || fn.Type == nil {
		return ""
	}
	return goxReturnNameFromResults(file, fn.Type.Results)
}

// DetectGoxReturnKind and DetectGoxReturnName are retained for focused callers
// that only have a declaration node. Runtime/compiler production paths use the
// file-aware helpers above so semantic identity cannot be inferred from a bare
// type name.
func DetectGoxReturnKind(fn *ast.FuncDecl) GoxReturnKind {
	return goxReturnKindFromName(DetectGoxReturnName(fn))
}

func DetectGoxReturnName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Type == nil || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return ""
	}
	expr := fn.Type.Results.List[0].Type
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil {
		return ""
	}
	qualifier, ok := selector.X.(*ast.Ident)
	if !ok || qualifier.Name != "gox" || !isGoxSemanticReturnMarker(selector.Sel.Name) {
		return ""
	}
	return selector.Sel.Name
}

func goxTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.StarExpr:
		return goxTypeName(t.X)
	default:
		return ""
	}
}

func stripFuncReturn(fn *ast.FuncDecl) {
	fn.Type.Results = nil
}

func transformActionReturn(fn *ast.FuncDecl) {
	stripFuncReturn(fn)
}

func ensurePageContextParam(fn *ast.FuncDecl, origins *OriginTable, source ast.Node) {
	params := &ast.FieldList{
		List: []*ast.Field{goxContextField("ctx")},
	}
	fn.Type.Params = params
	if origins != nil {
		origins.InheritTree(params, source)
	}
}

func ensureComponentWriterParam(fn *ast.FuncDecl, origins *OriginTable, source ast.Node) {
	if fn.Type.Params == nil {
		fn.Type.Params = &ast.FieldList{}
	}

	writer := &ast.Field{
		Names: []*ast.Ident{ast.NewIdent("goxWriter")},
		Type: &ast.SelectorExpr{
			X:   ast.NewIdent("gox"),
			Sel: ast.NewIdent("Writer"),
		},
	}
	fn.Type.Params.List = append([]*ast.Field{writer}, fn.Type.Params.List...)
	if origins != nil {
		origins.InheritTree(writer, source)
	}
}

func ensureContextParam(fn *ast.FuncDecl, first bool, origins *OriginTable, source ast.Node) {
	if fn.Type.Params == nil {
		fn.Type.Params = &ast.FieldList{}
	}

	params := fn.Type.Params
	flat := flattenFuncParams(params)

	for _, p := range flat {
		if p.Name == "ctx" && isGoxContext(p.Type) {
			return
		}
	}

	ctxField := goxContextField("ctx")
	if origins != nil {
		origins.InheritTree(ctxField, source)
	}

	if first {
		params.List = append([]*ast.Field{ctxField}, params.List...)
		return
	}

	params.List = append(params.List, ctxField)
}

func goxContextField(name string) *ast.Field {
	return &ast.Field{
		Names: []*ast.Ident{ast.NewIdent(name)},
		Type: &ast.StarExpr{
			X: &ast.SelectorExpr{
				X:   ast.NewIdent("gox"),
				Sel: ast.NewIdent("Context"),
			},
		},
	}
}

func isGoxContext(expr ast.Expr) bool {
	ptr, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}

	sel, ok := ptr.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "gox" && sel.Sel.Name == "Context"
}
