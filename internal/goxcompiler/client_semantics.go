package gox

import (
	"fmt"
	"go/ast"
	"go/token"
)

type clientNameDecl struct {
	line   int
	column int
}

func validateClientTopLevelNames(path string, src string, fset *token.FileSet, file *ast.File, clients []ClientDecl) error {
	clientNames := make(map[string]clientNameDecl, len(clients))
	for _, client := range clients {
		line, column := lineColAt(src, int(client.NamePos)-1)
		if previous, exists := clientNames[client.Name]; exists {
			return clientRedeclarationError(path, client.Name, line, column, previous)
		}
		clientNames[client.Name] = clientNameDecl{
			line:   line,
			column: column,
		}
	}

	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range node.Specs {
				switch item := spec.(type) {
				case *ast.ValueSpec:
					for _, name := range item.Names {
						if err := clientTopLevelConflict(path, fset, name, clientNames); err != nil {
							return err
						}
					}
				case *ast.TypeSpec:
					if err := clientTopLevelConflict(path, fset, item.Name, clientNames); err != nil {
						return err
					}
				case *ast.ImportSpec:
					if item.Name == nil || item.Name.Name == "_" || item.Name.Name == "." {
						continue
					}
					if err := clientTopLevelConflict(path, fset, item.Name, clientNames); err != nil {
						return err
					}
				}
			}
		case *ast.FuncDecl:
			if node.Recv != nil {
				continue
			}
			if err := clientTopLevelConflict(path, fset, node.Name, clientNames); err != nil {
				return err
			}
		}
	}

	return nil
}

func clientTopLevelConflict(path string, fset *token.FileSet, ident *ast.Ident, clients map[string]clientNameDecl) error {
	if ident == nil {
		return nil
	}
	client, exists := clients[ident.Name]
	if !exists {
		return nil
	}

	position := fset.Position(ident.Pos())
	line := position.Line - 1 // ParseModule prepends "package main\n".
	if line < 1 {
		line = 1
	}

	err := newCompilerErrorWithHint(
		diagnosticClientNameConflict,
		fmt.Sprintf(
			"identifier %q conflicts with client component declared at %s:%d:%d",
			ident.Name,
			normalizeCompileErrorPath(path),
			client.line,
			client.column,
		),
		"Rename either the client component or the top-level declaration.",
	)
	err.File = normalizeCompileErrorPath(path)
	err.Line = line
	err.Column = position.Column
	return err
}

func clientRedeclarationError(path string, name string, line int, column int, previous clientNameDecl) error {
	err := newCompilerErrorWithHint(
		diagnosticDuplicateClientComponent,
		fmt.Sprintf(
			"client component %q redeclared; previous declaration at %s:%d:%d",
			name,
			normalizeCompileErrorPath(path),
			previous.line,
			previous.column,
		),
		"Declare each client component name only once per module.",
	)
	err.File = normalizeCompileErrorPath(path)
	err.Line = line
	err.Column = column
	return err
}
