package gox

import "go/ast"

// RewriteWebSocketEmitAwait preserves the public Emit(...).Await(&out) syntax
// while compiling it into one atomic request operation.
func RewriteWebSocketEmitAwait(mod *Module) {
	RewriteWebSocketEmitAwaitWithOrigins(mod, nil)
}

func RewriteWebSocketEmitAwaitWithOrigins(mod *Module, origins *OriginTable) {
	if mod == nil || mod.File == nil {
		return
	}
	webSocketModule := moduleHasWebSocketEntrypoint(mod)
	serverSockets := serverSocketIdentifiers(mod.File)
	ast.Inspect(mod.File, func(node ast.Node) bool {
		awaitCall, ok := node.(*ast.CallExpr)
		if !ok || len(awaitCall.Args) != 1 {
			return true
		}
		awaitSelector, ok := awaitCall.Fun.(*ast.SelectorExpr)
		if !ok || awaitSelector.Sel.Name != "Await" {
			return true
		}
		emitCall, ok := awaitSelector.X.(*ast.CallExpr)
		if !ok || len(emitCall.Args) == 0 {
			return true
		}
		emitSelector, ok := emitCall.Fun.(*ast.SelectorExpr)
		if !ok || emitSelector.Sel.Name != "Emit" {
			return true
		}
		if !webSocketModule && !isServerSocketAwaitReceiver(emitSelector.X, serverSockets) {
			return true
		}
		awaitName := awaitSelector.Sel
		combinedSelector := &ast.SelectorExpr{X: emitSelector.X, Sel: ast.NewIdent("EmitAwait")}
		if origins != nil {
			origins.Inherit(awaitCall, awaitName)
			origins.InheritTree(combinedSelector, awaitName)
		}
		awaitCall.Fun = combinedSelector
		args := make([]ast.Expr, 0, len(emitCall.Args)+1)
		args = append(args, awaitCall.Args[0], emitCall.Args[0])
		args = append(args, emitCall.Args[1:]...)
		awaitCall.Args = args
		return true
	})
}

func isServerSocketAwaitReceiver(expression ast.Expr, identifiers map[string]struct{}) bool {
	if isServerSocketSelectionCall(expression) {
		return true
	}
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = identifiers[identifier.Name]
	return ok
}

func serverSocketIdentifiers(root ast.Node) map[string]struct{} {
	identifiers := make(map[string]struct{})
	ast.Inspect(root, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for index, expression := range value.Rhs {
				if index < len(value.Lhs) && isServerSocketSelectionCall(expression) {
					if identifier, ok := value.Lhs[index].(*ast.Ident); ok {
						identifiers[identifier.Name] = struct{}{}
					}
				}
			}
		case *ast.ValueSpec:
			for index, expression := range value.Values {
				if index < len(value.Names) && isServerSocketSelectionCall(expression) {
					identifiers[value.Names[index].Name] = struct{}{}
				}
			}
		}
		return true
	})
	return identifiers
}

func isServerSocketSelectionCall(expression ast.Expr) bool {
	selectionCall, ok := expression.(*ast.CallExpr)
	if !ok || len(selectionCall.Args) != 1 {
		return false
	}
	selectionSelector, ok := selectionCall.Fun.(*ast.SelectorExpr)
	if !ok || selectionSelector.Sel.Name != "GetByID" {
		return false
	}
	socketsSelector, ok := selectionSelector.X.(*ast.SelectorExpr)
	if !ok || socketsSelector.Sel.Name != "Sockets" {
		return false
	}
	packageName, ok := socketsSelector.X.(*ast.Ident)
	return ok && packageName.Name == "gox"
}
