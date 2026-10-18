package gox

import (
	"go/ast"
)

func TransformModuleConstsWithOrigins(file *ast.File, moduleName string, origins *OriginTable) ([]ConstDecl, error) {
	var consts []ConstDecl
	renameMap := map[string]string{}

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok.String() != "const" {
			continue
		}

		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for _, name := range valueSpec.Names {
				oldName := name.Name
				newName := GoxConstName(moduleName, oldName)

				renameMap[oldName] = newName
				if origins != nil {
					origins.Inherit(name, name)
				}
				name.Name = newName

				consts = append(consts, ConstDecl{
					Name:  oldName,
					Type:  valueSpec.Type,
					Value: nil,
					Spec:  valueSpec,
				})
			}
		}
	}

	if len(renameMap) > 0 {
		renameIdentsWithOrigins(file, renameMap, origins)
	}

	return consts, nil
}

func renameIdents(node ast.Node, renameMap map[string]string) {
	renameIdentsWithOrigins(node, renameMap, nil)
}

func renameIdentsWithOrigins(node ast.Node, renameMap map[string]string, origins *OriginTable) {
	ast.Inspect(node, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}

		if newName, ok := renameMap[ident.Name]; ok {
			if origins != nil {
				origins.Inherit(ident, ident)
			}
			ident.Name = newName
		}

		return true
	})
}
