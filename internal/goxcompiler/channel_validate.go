package gox

import (
	"fmt"
	"go/ast"

	"github.com/gox-lang/server/internal/goxsource"
)

// ValidateChannelEntrypoints binds Channel declarations to the filesystem
// identity established by goxsource. A public Channel file owns exactly one
// factory; no other GOX module kind may declare a gox.Channel entrypoint.
func ValidateChannelEntrypoints(mod *Module) error {
	if mod == nil {
		return nil
	}

	count := 0
	for _, fn := range mod.Funcs {
		if fn.Kind != FuncChannel {
			continue
		}
		count++

		if mod.Structure.Kind != goxsource.ModulePublicChannel {
			return newCompilerErrorWithHint(
				"channel-entrypoint-outside-channel",
				fmt.Sprintf("module %s declares a Channel entrypoint outside a public websocket Channel file", mod.Name),
				"Move the Channel entrypoint to a public .gox file under websocket/.",
			)
		}

		expected := mod.Structure.ChannelFunction
		if expected == "" {
			return newCompilerErrorWithHint(
				"invalid-channel-structure",
				fmt.Sprintf("channel module %s has no structural Channel function", mod.Name),
				"Use a public Channel path whose filename defines a valid Go identifier.",
			)
		}
		if fn.Name != expected {
			return newCompilerErrorWithHint(
				"invalid-channel-entrypoint-name",
				fmt.Sprintf("channel entrypoint %s.%s does not match %s", mod.Name, fn.Name, expected),
				fmt.Sprintf("Rename it to func %s(channel *gox.Channel) gox.Channel.", expected),
			)
		}
		if len(fn.Params) != 1 || !isGoxChannel(fn.Params[0].Type) {
			return newCompilerErrorWithHint(
				"invalid-channel-signature",
				fmt.Sprintf("invalid Channel signature in %s.%s", mod.Name, fn.Name),
				fmt.Sprintf("Use func %s(channel *gox.Channel) gox.Channel.", fn.Name),
			)
		}
	}

	if mod.Structure.Kind == goxsource.ModulePublicChannel {
		if count == 0 {
			expected := mod.Structure.ChannelFunction
			return newCompilerErrorWithHint(
				"missing-channel-entrypoint",
				fmt.Sprintf("public Channel %s does not declare its %s entrypoint", mod.Structure.ChannelName, expected),
				fmt.Sprintf("Add func %s(channel *gox.Channel) gox.Channel.", expected),
			)
		}
		if count > 1 {
			return newCompilerErrorWithHint(
				"multiple-channel-entrypoints",
				fmt.Sprintf("module %s declares more than one Channel entrypoint", mod.Name),
				"Keep exactly one function returning gox.Channel in each channel definition file.",
			)
		}
	}
	return nil
}

func isGoxChannel(expr ast.Expr) bool {
	ptr, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := ptr.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "gox" && sel.Sel.Name == "Channel"
}
