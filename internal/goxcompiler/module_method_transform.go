package gox

import "go/ast"

func TransformFunctionsToMethods(file *ast.File, moduleName string) error {
	return TransformFunctionsToMethodsWithOrigins(file, moduleName, nil)
}

func TransformFunctionsToMethodsWithOrigins(file *ast.File, moduleName string, origins *OriginTable) error {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		// Si ya es method, no tocar.
		if fn.Recv != nil {
			continue
		}

		receiver := &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{
						ast.NewIdent("m"),
					},
					Type: &ast.StarExpr{
						X: ast.NewIdent(GoxModuleName(moduleName)),
					},
				},
			},
		}
		fn.Recv = receiver
		if origins != nil {
			origins.InheritTree(receiver, fn.Name)
		}
	}

	return nil
}
