package gox

import (
	"go/ast"
	"strconv"
	"strings"
)

func TransformModuleImports(
	file *ast.File,
	origins *OriginTable,
) ([]GoxImport, error) {
	var goxImports []GoxImport

	var newDecls []ast.Decl

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok.String() != "import" {
			newDecls = append(newDecls, decl)
			continue
		}

		var keptSpecs []ast.Spec

		for _, spec := range gen.Specs {
			importSpec, ok := spec.(*ast.ImportSpec)
			if !ok {
				continue
			}

			path, err := strconv.Unquote(importSpec.Path.Value)
			if err != nil {
				return nil, err
			}

			if strings.HasSuffix(path, ".gox") {
				var name string

				if importSpec.Name != nil {
					name = importSpec.Name.Name
				}

				declaration := GoxImport{Path: path, Name: name}
				if source, ok := exactSourceRange(origins, importSpec.Path); ok {
					declaration.Source = source
				}
				goxImports = append(goxImports, declaration)

				continue
			}

			keptSpecs = append(keptSpecs, spec)
		}

		if len(keptSpecs) == 0 {
			continue
		}

		gen.Specs = keptSpecs
		newDecls = append(newDecls, gen)
	}

	file.Decls = newDecls

	return goxImports, nil
}
