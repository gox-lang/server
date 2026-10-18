package gox

import "go/ast"

func BuildActionParamsDecls(bundle *Bundle) []ast.Decl { return nil }

func actionUserParams(fn FuncDecl) []flatParam {
	if len(fn.Params) == 0 {
		return nil
	}

	out := make([]flatParam, 0, len(fn.Params))
	for _, param := range fn.Params {
		if param.Name == "ctx" && isGoxContext(param.Type) {
			continue
		}
		out = append(out, param)
	}

	return out
}
