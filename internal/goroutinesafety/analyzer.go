// Package goroutinesafety analyzes goroutines created by project code and
// reports those that do not establish a final panic-recovery barrier.
package goroutinesafety

import (
	"go/ast"
	"go/token"
)

// Diagnostic identifies a goroutine whose panic recovery cannot be verified.
type Diagnostic struct {
	Pos token.Pos
}

// AnalyzeFiles reports unsafe go statements in files that belong to the
// project. A goroutine is safe when the first defer registered by its entry
// function is an effective recovery barrier. Because defers run LIFO, that
// recovery executes last during unwinding.
func AnalyzeFiles(_ *token.FileSet, files []*ast.File) []Diagnostic {
	functions := collectFunctions(files)
	var diagnostics []Diagnostic

	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			goStmt, ok := node.(*ast.GoStmt)
			if !ok {
				return true
			}

			if !goroutineIsSafe(goStmt, functions) {
				diagnostics = append(diagnostics, Diagnostic{Pos: goStmt.Go})
			}
			return true
		})
	}

	return diagnostics
}

func collectFunctions(files []*ast.File) map[string]*ast.FuncDecl {
	functions := make(map[string]*ast.FuncDecl)
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil {
				continue
			}
			functions[fn.Name.Name] = fn
		}
	}
	return functions
}

func goroutineIsSafe(stmt *ast.GoStmt, functions map[string]*ast.FuncDecl) bool {
	if stmt == nil || stmt.Call == nil {
		return false
	}

	switch fn := stmt.Call.Fun.(type) {
	case *ast.FuncLit:
		return bodyHasRecoveryBarrier(fn.Body, functions)
	case *ast.Ident:
		decl := functions[fn.Name]
		return decl != nil && bodyHasRecoveryBarrier(decl.Body, functions)
	default:
		// Selector calls and other dynamically resolved targets are not local
		// function bodies that this analyzer can prove safe.
		return false
	}
}

func bodyHasRecoveryBarrier(body *ast.BlockStmt, functions map[string]*ast.FuncDecl) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}

	// The recovery barrier must be registered before any other executable
	// statement. Otherwise an earlier statement could panic before recover is
	// active. This also makes it necessarily the first registered defer, so LIFO
	// leaves it as the final barrier during unwinding.
	firstDefer, ok := body.List[0].(*ast.DeferStmt)
	if !ok {
		return false
	}
	return deferRecovers(firstDefer, functions)
}

func deferRecovers(stmt *ast.DeferStmt, functions map[string]*ast.FuncDecl) bool {
	if stmt == nil || stmt.Call == nil {
		return false
	}

	switch fn := stmt.Call.Fun.(type) {
	case *ast.Ident:
		// defer recover() does not recover a panic in Go. recover must be
		// called directly by the body of a deferred function.
		if fn.Name == "recover" {
			return false
		}
		decl := functions[fn.Name]
		return decl != nil && directlyCallsRecover(decl.Body)
	case *ast.FuncLit:
		return directlyCallsRecover(fn.Body)
	default:
		return false
	}
}

func directlyCallsRecover(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	return statementDirectlyRecovers(body.List[0])
}

func statementDirectlyRecovers(stmt ast.Stmt) bool {
	switch node := stmt.(type) {
	case *ast.ExprStmt:
		return isRecoverCall(node.X)
	case *ast.AssignStmt:
		for _, expr := range node.Rhs {
			if isRecoverCall(expr) {
				return true
			}
		}
		return false
	case *ast.DeclStmt:
		decl, ok := node.Decl.(*ast.GenDecl)
		if !ok {
			return false
		}
		for _, spec := range decl.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, expr := range value.Values {
				if isRecoverCall(expr) {
					return true
				}
			}
		}
		return false
	case *ast.IfStmt:
		return node.Init != nil && statementDirectlyRecovers(node.Init)
	default:
		return false
	}
}

func isRecoverCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == "recover"
}
