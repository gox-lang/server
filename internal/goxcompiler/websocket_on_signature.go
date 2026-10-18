package gox

import (
	"fmt"
	"go/ast"
	"go/token"
)

// RewriteWebSocketOnSignatures preserves callback parameter names that Go
// reflection discards. It changes On calls backed by a function literal or a
// named module function inside a module with a WebSocket or Channel entrypoint.
func RewriteWebSocketOnSignatures(mod *Module, fset *token.FileSet) error {
	return RewriteWebSocketOnSignaturesWithOrigins(mod, fset, nil)
}

func RewriteWebSocketOnSignaturesWithOrigins(mod *Module, fset *token.FileSet, origins *OriginTable) error {
	if mod == nil || mod.File == nil || !moduleHasEventEntrypoint(mod) {
		return nil
	}
	var rewriteErr error
	ast.Inspect(mod.File, func(node ast.Node) bool {
		if rewriteErr != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "On" {
			return true
		}
		fnType := websocketHandlerType(mod.File, call.Args[1])
		if fnType == nil {
			return true
		}
		hasReply, err := validateWebSocketOnResults(fnType.Results)
		if err != nil {
			compileErr := newCompilerErrorWithHint("invalid-websocket-on-signature", err.Error(), "Use func(args...) with no return, or func(args...) gox.EventReply with exactly one returned value.")
			compileErr.File = normalizeCompileErrorPath(mod.Path)
			if fset != nil {
				position := fset.Position(call.Args[1].Pos())
				compileErr.Line = position.Line - 1
				if compileErr.Line < 1 {
					compileErr.Line = position.Line
				}
				compileErr.Column = position.Column
			}
			rewriteErr = compileErr
			return false
		}
		params := fnType.Params
		names := make([]ast.Expr, 0)
		for _, field := range params.List {
			if len(field.Names) == 0 {
				nameLiteral := &ast.BasicLit{Kind: token.STRING, Value: `""`}
				if origins != nil {
					origins.InheritTree(nameLiteral, field)
				}
				names = append(names, nameLiteral)
				continue
			}
			for _, name := range field.Names {
				nameLiteral := &ast.BasicLit{Kind: token.STRING, Value: `"` + name.Name + `"`}
				if origins != nil {
					origins.InheritTree(nameLiteral, name)
				}
				names = append(names, nameLiteral)
			}
		}
		onName := selector.Sel
		registeredName := ast.NewIdent("OnRegistered")
		parameterNames := &ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("string")}, Elts: names}
		hasReplyValue := ast.NewIdent(fmt.Sprintf("%t", hasReply))
		if origins != nil {
			origins.Inherit(call, onName)
			origins.Inherit(selector, onName)
			origins.InheritTree(registeredName, onName)
			origins.InheritTree(parameterNames, call.Args[1])
			origins.InheritTree(hasReplyValue, call.Args[1])
		}
		selector.Sel = registeredName
		call.Args = []ast.Expr{call.Args[0], parameterNames, hasReplyValue, call.Args[1]}
		return true
	})
	return rewriteErr
}

func websocketHandlerType(file *ast.File, handler ast.Expr) *ast.FuncType {
	switch value := handler.(type) {
	case *ast.FuncLit:
		return value.Type
	case *ast.Ident:
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == value.Name {
				return fn.Type
			}
		}
	}
	return nil
}

func validateWebSocketOnResults(results *ast.FieldList) (bool, error) {
	if results == nil || len(results.List) == 0 {
		return false, nil
	}
	count := 0
	for _, field := range results.List {
		if len(field.Names) == 0 {
			count++
		} else {
			count += len(field.Names)
		}
	}
	if count != 1 {
		return false, fmt.Errorf("WebSocket On handler can return at most one value, declared %d", count)
	}
	selector, ok := results.List[0].Type.(*ast.SelectorExpr)
	if !ok {
		return false, fmt.Errorf("WebSocket On handler return type must be gox.EventReply")
	}
	pkg, packageOK := selector.X.(*ast.Ident)
	if !packageOK || pkg.Name != "gox" || selector.Sel.Name != "EventReply" {
		return false, fmt.Errorf("WebSocket On handler return type must be gox.EventReply")
	}
	return true, nil
}

func moduleHasWebSocketEntrypoint(mod *Module) bool {
	for _, fn := range mod.Funcs {
		if fn.Kind == FuncWebSocket {
			return true
		}
	}
	return false
}

func moduleHasEventEntrypoint(mod *Module) bool {
	for _, fn := range mod.Funcs {
		if fn.Kind == FuncWebSocket || fn.Kind == FuncChannel {
			return true
		}
	}
	return false
}
