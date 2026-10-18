package gox

import (
	"fmt"

	"github.com/gox-lang/server/internal/goxsource"
)

// ValidateModuleStructure enforces the declaration boundary between the HTTP
// pages tree and the websocket tree. It restricts GOX entrypoint declarations;
// ordinary use of websocket APIs from Pages remains valid.
func ValidateModuleStructure(mod *Module) error {
	if mod == nil {
		return nil
	}

	if !isWebSocketTreeModule(mod.Structure.Kind) {
		return nil
	}

	for _, fn := range mod.Funcs {
		switch fn.Kind {
		case FuncPage:
			return newCompilerErrorWithHint(
				"page-entrypoint-in-websocket-tree",
				fmt.Sprintf("module %s declares Page entrypoint %s inside websocket/", mod.Name, fn.Name),
				"Move gox.Page entrypoints to pages/.",
			)
		case FuncAction:
			return newCompilerErrorWithHint(
				"action-entrypoint-in-websocket-tree",
				fmt.Sprintf("module %s declares Action %s inside websocket/", mod.Name, fn.Name),
				"Move gox.Action declarations to pages/.",
			)
		}
	}

	return nil
}

func isWebSocketTreeModule(kind goxsource.ModuleKind) bool {
	switch kind {
	case goxsource.ModuleGlobalWebSocket,
		goxsource.ModulePublicChannel,
		goxsource.ModulePrivateWebSocket,
		goxsource.ModuleWebSocketAlias:
		return true
	default:
		return false
	}
}
