package gox

import (
	"go/ast"
	"go/token"
)

func AddUserStatementLineMarkers(file *ast.File, fset *token.FileSet, sourceFile string) {
	if file == nil || fset == nil || sourceFile == "" {
		return
	}

	// Function literals are expressions, not statements. Collect them before
	// inserting markers and process deepest literals first so callback bodies
	// keep their own absolute source locations.
	var literals []*ast.FuncLit
	ast.Inspect(file, func(node ast.Node) bool {
		if literal, ok := node.(*ast.FuncLit); ok && literal.Body != nil {
			literals = append(literals, literal)
		}
		return true
	})
	for i := len(literals) - 1; i >= 0; i-- {
		literals[i].Body.List = addUserLineMarkersToStmtList(literals[i].Body.List, fset, sourceFile)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		fn.Body.List = addUserLineMarkersToStmtList(fn.Body.List, fset, sourceFile)
	}
}

func addUserLineMarkersToStmtList(stmts []ast.Stmt, fset *token.FileSet, sourceFile string) []ast.Stmt {
	if len(stmts) == 0 {
		return stmts
	}

	out := make([]ast.Stmt, 0, len(stmts)*3)

	for _, stmt := range stmts {
		stmt = addUserLineMarkersInsideStmt(stmt, fset, sourceFile)

		if line := userStmtSourceLine(stmt, fset); line > 0 {
			out = append(out, buildGoxLineMarkerAST(TemplatePosition{
				File:   sourceFile,
				Line:   line,
				Column: 1,
			}))
		}

		out = append(out, stmt)

		// A //line directive remains active until another directive replaces it.
		// Close the user-owned region explicitly so compiler-generated statements
		// that follow it are not attributed to the last .gox source line.
		out = append(out, buildGoxLineResetMarkerAST())
	}

	return out
}

func addUserLineMarkersInsideStmt(stmt ast.Stmt, fset *token.FileSet, sourceFile string) ast.Stmt {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		s.List = addUserLineMarkersToStmtList(s.List, fset, sourceFile)

	case *ast.IfStmt:
		if s.Body != nil {
			s.Body.List = addUserLineMarkersToStmtList(s.Body.List, fset, sourceFile)
		}
		if s.Else != nil {
			s.Else = addUserLineMarkersInsideStmt(s.Else, fset, sourceFile)
		}

	case *ast.ForStmt:
		if s.Body != nil {
			s.Body.List = addUserLineMarkersToStmtList(s.Body.List, fset, sourceFile)
		}

	case *ast.RangeStmt:
		if s.Body != nil {
			s.Body.List = addUserLineMarkersToStmtList(s.Body.List, fset, sourceFile)
		}

	case *ast.SwitchStmt:
		if s.Body != nil {
			for _, child := range s.Body.List {
				if cc, ok := child.(*ast.CaseClause); ok {
					cc.Body = addUserLineMarkersToStmtList(cc.Body, fset, sourceFile)
				}
			}
		}

	case *ast.TypeSwitchStmt:
		if s.Body != nil {
			for _, child := range s.Body.List {
				if cc, ok := child.(*ast.CaseClause); ok {
					cc.Body = addUserLineMarkersToStmtList(cc.Body, fset, sourceFile)
				}
			}
		}

	case *ast.SelectStmt:
		if s.Body != nil {
			for _, child := range s.Body.List {
				if cc, ok := child.(*ast.CommClause); ok {
					cc.Body = addUserLineMarkersToStmtList(cc.Body, fset, sourceFile)
				}
			}
		}

	case *ast.LabeledStmt:
		s.Stmt = addUserLineMarkersInsideStmt(s.Stmt, fset, sourceFile)
	}

	return stmt
}

func userStmtSourceLine(stmt ast.Stmt, fset *token.FileSet) int {
	if stmt == nil || fset == nil {
		return 0
	}

	if isGoxLineMarkerStmt(stmt) {
		return 0
	}

	pos := stmt.Pos()
	if !pos.IsValid() {
		return 0
	}

	p := fset.Position(pos)
	if p.Line <= 0 {
		return 0
	}

	// ParseModule prepends "package main\n" before parsing the original .gox
	// source. Subtract that synthetic package line to recover the user-facing
	// .gox line.
	line := p.Line - 1
	if line < 1 {
		return p.Line
	}

	return line
}

func isGoxLineMarkerStmt(stmt ast.Stmt) bool {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}

	call, ok := exprStmt.X.(*ast.CallExpr)
	if !ok {
		return false
	}

	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}

	return ident.Name == "__goxLine" || ident.Name == "__goxLineReset"
}
