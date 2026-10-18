package gox

import (
	"fmt"
	"go/ast"

	"github.com/gox-lang/server/internal/goxerrors"
)

func ValidateActions(mod *Module) error {
	for _, fn := range mod.Funcs {
		if fn.Kind != FuncAction {
			continue
		}

		if err := validateActionParams(fn.Node); err != nil {
			return newCompilerErrorWithCause(
				"invalid-action-signature",
				fmt.Sprintf("invalid Action %s.%s", mod.Name, fn.Name),
				err,
			)
		}
	}

	return nil
}

func validateActionParams(fn *ast.FuncDecl) error {
	if fn.Type == nil {
		return invalidActionSignatureError(fn)
	}

	if fn.Type.Params == nil {
		return nil
	}

	params := flattenFuncParams(fn.Type.Params)
	if len(params) == 0 {
		return nil
	}

	for i, p := range params {
		if p.Name == "ctx" && isGoxContext(p.Type) {
			if i != len(params)-1 {
				return invalidActionSignatureError(fn)
			}
			continue
		}

		if isInvalidActionParamName(p.Name) {
			return newCompilerErrorWithHint(
				"invalid-action-param-name",
				fmt.Sprintf("invalid Action param name %q", p.Name),
				"Use named action parameters. The compiler appends ctx *gox.Context when it is not declared.",
			)
		}
	}

	return nil
}

func invalidActionSignatureError(fn *ast.FuncDecl) *goxerrors.GoxError {
	return newCompilerErrorWithHint(
		"invalid-action-signature",
		fmt.Sprintf("invalid Action signature. expected: %s", expectedActionSignature(fn)),
		"Use func ActionName(args...) gox.Action, optionally ending with ctx *gox.Context.",
	)
}

func isInvalidActionParamName(name string) bool {
	switch name {
	case "",
		"bool",
		"byte",
		"complex64",
		"complex128",
		"error",
		"float32",
		"float64",
		"int",
		"int8",
		"int16",
		"int32",
		"int64",
		"rune",
		"string",
		"uint",
		"uint8",
		"uint16",
		"uint32",
		"uint64",
		"uintptr",
		"any":
		return true
	default:
		return false
	}
}

func expectedActionSignature(fn *ast.FuncDecl) string {
	name := "Action"
	if fn != nil && fn.Name != nil {
		name = fn.Name.Name
	}

	return fmt.Sprintf("func %s(args...) gox.Action", name)
}
