package gox

import (
	"fmt"
	"go/ast"

	"github.com/gox-lang/server/internal/goxsource"
)

func ValidateWebSocketEntrypoints(mod *Module) error {
	var webSockets []FuncDecl
	for _, fn := range mod.Funcs {
		if fn.Kind == FuncWebSocket {
			webSockets = append(webSockets, fn)
		}
	}

	if mod.Structure.Kind != goxsource.ModuleGlobalWebSocket {
		if len(webSockets) == 0 {
			return nil
		}
		return newCompilerErrorWithHint(
			"invalid-websocket-location",
			fmt.Sprintf("WebSocket entrypoint %s.%s is declared outside websocket/index.gox", mod.Name, webSockets[0].Name),
			"Declare the global WebSocket only in websocket/index.gox.",
		)
	}

	if mod.DeclaredName != "websocket" {
		return newCompilerErrorWithHint(
			"invalid-websocket-module",
			fmt.Sprintf("websocket/index.gox must declare module websocket, got module %s", mod.DeclaredName),
			"Use module websocket in websocket/index.gox.",
		)
	}

	if len(webSockets) == 0 {
		return newCompilerErrorWithHint(
			"missing-websocket-entrypoint",
			"websocket/index.gox does not declare the global WebSocket entrypoint",
			"Add func Index(socket *gox.Socket) gox.WebSocket.",
		)
	}
	if len(webSockets) != 1 {
		return newCompilerErrorWithHint(
			"multiple-websocket-entrypoints",
			"websocket/index.gox must declare exactly one global WebSocket entrypoint",
			"Keep only func Index(socket *gox.Socket) gox.WebSocket.",
		)
	}

	fn := webSockets[0]
	if fn.Name != "Index" || len(fn.Params) != 1 || !isGoxSocket(fn.Params[0].Type) {
		return newCompilerErrorWithHint(
			"invalid-websocket-signature",
			fmt.Sprintf("invalid WebSocket signature in %s.%s", mod.Name, fn.Name),
			"Use func Index(socket *gox.Socket) gox.WebSocket.",
		)
	}
	return nil
}

func isGoxSocket(expr ast.Expr) bool {
	ptr, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := ptr.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "gox" && sel.Sel.Name == "Socket"
}
