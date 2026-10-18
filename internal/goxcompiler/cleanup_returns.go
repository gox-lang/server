package gox

import "go/ast"

func CleanupRedundantReturns(file *ast.File) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		removeTrailingReturn(fn.Body)
	}
}

func removeTrailingReturn(block *ast.BlockStmt) {
	if len(block.List) == 0 {
		return
	}

	last := block.List[len(block.List)-1]

	ret, ok := last.(*ast.ReturnStmt)
	if !ok {
		return
	}

	if len(ret.Results) != 0 {
		return
	}

	block.List = block.List[:len(block.List)-1]
}
