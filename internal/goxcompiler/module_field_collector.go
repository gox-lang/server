package gox

import (
	"go/ast"
	"go/token"
)

func CollectModuleFields(file *ast.File, moduleName string) ([]VarDecl, error) {
	return CollectModuleFieldsWithOrigins(file, moduleName, nil)
}

func CollectModuleFieldsWithOrigins(file *ast.File, moduleName string, origins *OriginTable) ([]VarDecl, error) {
	var fields []VarDecl
	var newDecls []ast.Decl

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok.String() != "var" {
			newDecls = append(newDecls, decl)
			continue
		}

		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for i, name := range valueSpec.Names {
				var value ast.Expr
				if i < len(valueSpec.Values) {
					value = valueSpec.Values[i]
				}

				fieldType := valueSpec.Type
				if fieldType == nil && value != nil {
					fieldType = inferVarType(value)
				}

				fields = append(fields, VarDecl{
					Name:  name.Name,
					Type:  fieldType,
					Value: value,
					Spec:  valueSpec,
				})
			}
		}
	}

	file.Decls = newDecls
	insertDeclAfterImports(file, buildModuleStructDeclWithOrigins(moduleName, fields, origins))

	return fields, nil
}

func buildModuleStructDecl(moduleName string, fields []VarDecl) ast.Decl {
	return buildModuleStructDeclWithOrigins(moduleName, fields, nil)
}

func buildModuleStructDeclWithOrigins(moduleName string, fields []VarDecl, origins *OriginTable) ast.Decl {
	structFields := make([]*ast.Field, 0, len(fields))

	for _, field := range fields {
		name := ast.NewIdent(field.Name)
		if original := varDeclNameNode(field); original != nil {
			name = original
		}
		structField := &ast.Field{
			Names: []*ast.Ident{name},
			Type:  field.Type,
		}
		if origins != nil {
			origins.InheritTree(structField, name)
		}
		structFields = append(structFields, structField)
	}

	return &ast.GenDecl{
		Tok: token.TYPE,
		Specs: []ast.Spec{
			&ast.TypeSpec{
				Name: ast.NewIdent(GoxModuleName(moduleName)),
				Type: &ast.StructType{
					Fields: &ast.FieldList{
						List: structFields,
					},
				},
			},
		},
	}
}

func varDeclNameNode(field VarDecl) *ast.Ident {
	if field.Spec == nil {
		return nil
	}
	for _, name := range field.Spec.Names {
		if name != nil && name.Name == field.Name {
			return name
		}
	}
	return nil
}

func inferVarType(value ast.Expr) ast.Expr {
	switch v := value.(type) {
	case *ast.BasicLit:
		switch v.Kind {
		case token.STRING:
			return ast.NewIdent("string")

		case token.INT:
			return ast.NewIdent("int")

		case token.FLOAT:
			return ast.NewIdent("float64")

		case token.CHAR:
			return ast.NewIdent("rune")
		}

	case *ast.Ident:
		switch v.Name {
		case "true", "false":
			return ast.NewIdent("bool")

		case "nil":
			return ast.NewIdent("any")
		}

	case *ast.CallExpr:
		// var x = Type(...)
		// Por ahora no inferimos profundo.
		return ast.NewIdent("any")

	case *ast.CompositeLit:
		if v.Type != nil {
			return v.Type
		}

	case *ast.FuncLit:
		if v.Type != nil {
			return v.Type
		}
	}

	return ast.NewIdent("any")
}

func insertDeclAfterImports(file *ast.File, decl ast.Decl) {
	insertAt := 0

	for i, d := range file.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}

		insertAt = i + 1
	}

	newDecls := make([]ast.Decl, 0, len(file.Decls)+1)
	newDecls = append(newDecls, file.Decls[:insertAt]...)
	newDecls = append(newDecls, decl)
	newDecls = append(newDecls, file.Decls[insertAt:]...)

	file.Decls = newDecls
}
