package gox

import (
	"go/ast"
	"go/token"
)

func AddModuleConstructor(file *ast.File, moduleName string, fields []VarDecl) error {
	return AddModuleConstructorWithOrigins(file, moduleName, fields, nil)
}

func AddModuleConstructorWithOrigins(file *ast.File, moduleName string, fields []VarDecl, origins *OriginTable) error {
	constructor := buildModuleConstructorWithOrigins(moduleName, fields, origins)
	file.Decls = append(file.Decls, constructor)
	return nil
}

func buildModuleConstructor(moduleName string, fields []VarDecl) ast.Decl {
	return buildModuleConstructorWithOrigins(moduleName, fields, nil)
}

func buildModuleConstructorWithOrigins(moduleName string, fields []VarDecl, origins *OriginTable) ast.Decl {
	elts := make([]ast.Expr, 0, len(fields))

	for _, field := range fields {
		if field.Value == nil {
			continue
		}

		entry := &ast.KeyValueExpr{
			Key:   ast.NewIdent(field.Name),
			Value: field.Value,
		}
		if origins != nil {
			origins.InheritTree(entry, varDeclNameNode(field))
		}
		elts = append(elts, entry)
	}

	return &ast.FuncDecl{
		Name: ast.NewIdent(GoxModuleConstructorName(moduleName)),
		Type: &ast.FuncType{
			Params: &ast.FieldList{},
			Results: &ast.FieldList{
				List: []*ast.Field{
					{
						Type: &ast.StarExpr{
							X: ast.NewIdent(GoxModuleName(moduleName)),
						},
					},
				},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{
						&ast.UnaryExpr{
							Op: token.AND,
							X: &ast.CompositeLit{
								Type: ast.NewIdent(GoxModuleName(moduleName)),
								Elts: elts,
							},
						},
					},
				},
			},
		},
	}
}
