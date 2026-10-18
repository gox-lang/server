package gox

import "go/ast"

func TransformModuleTypesWithOrigins(file *ast.File, moduleName string, origins *OriginTable) ([]TypeDecl, error) {
	var types []TypeDecl
	renameMap := map[string]string{}

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok.String() != "type" {
			continue
		}

		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			oldName := typeSpec.Name.Name
			newName := GoxTypeName(moduleName, oldName)

			renameMap[oldName] = newName
			if origins != nil {
				origins.Inherit(typeSpec.Name, typeSpec.Name)
			}
			typeSpec.Name.Name = newName

			types = append(types, TypeDecl{
				Name: oldName,
				Node: typeSpec,
			})
		}
	}

	if len(renameMap) > 0 {
		renameIdentsWithOrigins(file, renameMap, origins)
	}

	return types, nil
}
