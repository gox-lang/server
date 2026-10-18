package gox

import (
	"go/ast"
	"go/token"
)

func AddBundleConstructor(bundle *Bundle) {
	bundle.Decls = append(bundle.Decls, buildGoxBundleStruct(bundle))
	bundle.Decls = append(bundle.Decls, buildGoxBundleConstructor(bundle))
}

func buildGoxBundleStruct(bundle *Bundle) ast.Decl {
	fields := make([]*ast.Field, 0, len(bundle.Modules))

	for _, mod := range bundle.Modules {
		fields = append(fields, &ast.Field{
			Names: []*ast.Ident{
				ast.NewIdent(mod.Name),
			},
			Type: &ast.StarExpr{
				X: ast.NewIdent(GoxModuleName(mod.Name)),
			},
		})
	}

	return &ast.GenDecl{
		Tok: token.TYPE,
		Specs: []ast.Spec{
			&ast.TypeSpec{
				Name: ast.NewIdent("GoxBundle"),
				Type: &ast.StructType{
					Fields: &ast.FieldList{
						List: fields,
					},
				},
			},
		},
	}
}

func buildGoxBundleConstructor(bundle *Bundle) ast.Decl {
	var stmts []ast.Stmt

	moduleVarNames := map[string]string{}

	for _, mod := range bundle.Modules {
		varName := lowerFirst(mod.Name)
		moduleVarNames[mod.Name] = varName

		args := make([]ast.Expr, 0, len(mod.GoxImports))

		for _, imp := range mod.GoxImports {
			child := bundleModuleByResolvedPath(bundle, imp.ResolvedPath)
			if child == nil {
				continue
			}

			args = append(args, ast.NewIdent(moduleVarNames[child.Name]))
		}

		stmts = append(stmts, &ast.AssignStmt{
			Lhs: []ast.Expr{
				ast.NewIdent(varName),
			},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{
				&ast.CallExpr{
					Fun:  ast.NewIdent(GoxModuleConstructorName(mod.Name)),
					Args: args,
				},
			},
		})
	}

	elts := make([]ast.Expr, 0, len(bundle.Modules))

	for _, mod := range bundle.Modules {
		elts = append(elts, &ast.KeyValueExpr{
			Key:   ast.NewIdent(mod.Name),
			Value: ast.NewIdent(moduleVarNames[mod.Name]),
		})
	}

	stmts = append(stmts, &ast.ReturnStmt{
		Results: []ast.Expr{
			&ast.UnaryExpr{
				Op: token.AND,
				X: &ast.CompositeLit{
					Type: ast.NewIdent("GoxBundle"),
					Elts: elts,
				},
			},
		},
	})

	return &ast.FuncDecl{
		Name: ast.NewIdent("NewGoxBundle"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{},
			Results: &ast.FieldList{
				List: []*ast.Field{
					{
						Type: &ast.StarExpr{
							X: ast.NewIdent("GoxBundle"),
						},
					},
				},
			},
		},
		Body: &ast.BlockStmt{
			List: stmts,
		},
	}
}

func bundleModuleByResolvedPath(bundle *Bundle, path string) *Module {
	for _, mod := range bundle.Modules {
		if mod.Path == path {
			return mod
		}
	}

	return nil
}
