package gox

import "go/ast"

func SpecializeRuntimeAnyCalls(decls []ast.Decl) {
	baseEnv := buildTemplateBaseTypeEnv(&ast.File{Decls: decls})

	for _, decl := range decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			env := templateTypeEnvForFunc(d, baseEnv)
			specializeRuntimeAnyCallsInNode(d.Body, env)
		default:
			specializeRuntimeAnyCallsInNode(d, baseEnv)
		}
	}
}

func specializeRuntimeAnyCallsInNode(node ast.Node, typeEnv *TemplateTypeEnv) {
	if node == nil {
		return
	}

	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}

		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}

		var raw bool
		switch id.Name {
		case "GoxWriteTextAny":
			raw = false
		case "GoxWriteRawAny":
			raw = true
		default:
			return true
		}

		helper := runtimeWriteHelperName(typeEnv.ExprType(call.Args[1]), raw)
		if helper != id.Name {
			id.Name = helper
		}

		return true
	})
}
