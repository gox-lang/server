package gox

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"unicode"
)

type TemplateStaticDecl struct {
	Name     string
	Literals []string
}

func GoxTemplateStaticName(moduleName string, funcName string) string {
	return "goxStatic" + sanitizeGoIdentPart(moduleName) + sanitizeGoIdentPart(funcName)
}

func sanitizeGoIdentPart(s string) string {
	var out strings.Builder
	capitalizeNext := true

	for _, r := range s {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			if out.Len() == 0 && unicode.IsDigit(r) {
				out.WriteByte('_')
			}

			if capitalizeNext && unicode.IsLetter(r) {
				out.WriteRune(unicode.ToUpper(r))
			} else {
				out.WriteRune(r)
			}
			capitalizeNext = false
			continue
		}

		capitalizeNext = true
	}

	if out.Len() == 0 {
		return "Template"
	}

	return out.String()
}

func BuildTemplateStaticDecls(statics []TemplateStaticDecl) []ast.Decl {
	decls := make([]ast.Decl, 0, len(statics))

	for _, static := range statics {
		if len(static.Literals) == 0 {
			continue
		}

		values := make([]ast.Expr, 0, len(static.Literals))
		for _, literal := range static.Literals {
			values = append(values, byteSliceConversion(&ast.BasicLit{
				Kind:  token.STRING,
				Value: strconv.Quote(literal),
			}))
		}

		decls = append(decls, &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{
				&ast.ValueSpec{
					Names: []*ast.Ident{ast.NewIdent(static.Name)},
					Values: []ast.Expr{
						&ast.CompositeLit{
							Type: &ast.ArrayType{
								Len: &ast.Ellipsis{},
								Elt: &ast.ArrayType{Elt: ast.NewIdent("byte")},
							},
							Elts: values,
						},
					},
				},
			},
		})
	}

	return decls
}

func AddTemplateStaticDecls(file *ast.File, statics []TemplateStaticDecl) {
	AddTemplateStaticDeclsWithOrigins(file, statics, nil)
}

func AddTemplateStaticDeclsWithOrigins(file *ast.File, statics []TemplateStaticDecl, origins *OriginTable) {
	sources := make(map[string]ast.Node, len(statics))
	if origins != nil {
		for _, static := range statics {
			sources[static.Name] = findIdentWithOrigin(file, static.Name, origins)
		}
	}
	decls := BuildTemplateStaticDecls(statics)
	if len(decls) == 0 {
		return
	}
	if origins != nil {
		for _, decl := range decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || len(gen.Specs) != 1 {
				continue
			}
			spec, ok := gen.Specs[0].(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 {
				continue
			}
			origins.InheritTree(decl, sources[spec.Names[0].Name])
		}
	}

	insertAt := 0
	for i, decl := range file.Decls {
		if isImportDecl(decl) {
			insertAt = i + 1
		}
	}

	out := make([]ast.Decl, 0, len(file.Decls)+len(decls))
	out = append(out, file.Decls[:insertAt]...)
	out = append(out, decls...)
	out = append(out, file.Decls[insertAt:]...)
	file.Decls = out
}

func findIdentWithOrigin(node ast.Node, name string, origins *OriginTable) ast.Node {
	var found ast.Node
	ast.Inspect(node, func(current ast.Node) bool {
		if found != nil {
			return false
		}
		ident, ok := current.(*ast.Ident)
		if !ok || ident.Name != name {
			return true
		}
		if _, ok := origins.Get(ident); ok {
			found = ident
			return false
		}
		return true
	})
	return found
}
