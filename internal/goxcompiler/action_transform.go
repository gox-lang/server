package gox

import "go/ast"

func RenameActionFunctions(file *ast.File, mod *Module) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		for _, meta := range mod.Funcs {
			if meta.Kind != FuncAction {
				continue
			}

			if fn.Name.Name != meta.Name {
				continue
			}

			if mod.Origins != nil {
				mod.Origins.Inherit(fn.Name, fn.Name)
			}
			fn.Name.Name = GoxActionName(mod.Name, meta.Name)
		}
	}
}
