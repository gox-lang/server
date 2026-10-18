package gox

import (
	"fmt"
	"go/ast"
	"go/token"

	"github.com/gox-lang/server/internal/goxerrors"
)

type invalidActionReturnError struct {
	core *goxerrors.GoxError
	pos  token.Pos
}

func (e *invalidActionReturnError) Error() string {
	if e == nil || e.core == nil {
		return ""
	}
	return e.core.Error()
}

func EnsureActionReturns(file *ast.File, fset *token.FileSet, mod *Module) error {
	actionNames := map[string]FuncDecl{}

	for _, fn := range mod.Funcs {
		if fn.Kind != FuncAction {
			continue
		}

		actionNames[GoxActionName(mod.Name, fn.Name)] = fn
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		meta, ok := actionNames[fn.Name.Name]
		if !ok {
			continue
		}

		if err := ensureActionReturnsInFunc(fn); err != nil {
			return newInvalidActionReturnError(mod, meta, fn, fset, err)
		}
	}

	return nil
}

func newInvalidActionReturnError(mod *Module, meta FuncDecl, generatedFn *ast.FuncDecl, fset *token.FileSet, cause error) *goxerrors.GoxError {
	actionName := meta.Name
	if actionName == "" && generatedFn != nil && generatedFn.Name != nil {
		actionName = generatedFn.Name.Name
	}

	err := newCompilerErrorWithCause("invalid-action-return", fmt.Sprintf("invalid Action %s", actionName), cause)
	var causePos token.Pos
	if actionReturnErr, ok := cause.(*invalidActionReturnError); ok {
		causePos = actionReturnErr.pos
		if actionReturnErr.core != nil {
			err.Hint = actionReturnErr.core.Hint
			err.Expected = actionReturnErr.core.Expected
			err.Found = actionReturnErr.core.Found
		}
	} else if causeCore, ok := cause.(*goxerrors.GoxError); ok {
		err.Hint = causeCore.Hint
		err.Expected = causeCore.Expected
		err.Found = causeCore.Found
	}

	if mod != nil {
		err.File = normalizeCompileErrorPath(mod.Path)
	}

	if fset != nil && causePos.IsValid() {
		pos := fset.Position(causePos)
		if pos.IsValid() {
			line := pos.Line - 1
			if line < 1 {
				line = pos.Line
			}
			err.Line = line
			err.Column = pos.Column
		}
	}
	if err.Line == 0 {
		posNode := meta.Node
		if posNode == nil {
			posNode = generatedFn
		}
		if fset != nil && posNode != nil && posNode.Name != nil && posNode.Name.Pos().IsValid() {
			pos := fset.Position(posNode.Name.Pos())
			if pos.IsValid() {
				line := pos.Line - 1
				if line < 1 {
					line = pos.Line
				}
				err.Line = line
				err.Column = pos.Column
			}
		}
	}

	err.AddDetail("action", actionName)
	if mod != nil {
		err.AddDetail("module", mod.Name)
	}
	if generatedFn != nil && generatedFn.Name != nil {
		err.AddDetail("generatedAction", generatedFn.Name.Name)
	}

	return err
}

func ensureActionReturnsInFunc(fn *ast.FuncDecl) error {
	return ensureActionReturnsInBlock(fn.Body)
}

func ensureActionReturnsInBlock(block *ast.BlockStmt) error {
	for _, stmt := range block.List {
		if err := ensureActionReturnsInStmt(stmt); err != nil {
			return err
		}
	}

	return nil
}

func ensureActionReturnsInStmt(stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return normalizeActionReturn(s)

	case *ast.IfStmt:
		if s.Body != nil {
			if err := ensureActionReturnsInBlock(s.Body); err != nil {
				return err
			}
		}

		if s.Else != nil {
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				if err := ensureActionReturnsInBlock(e); err != nil {
					return err
				}
			case *ast.IfStmt:
				if err := ensureActionReturnsInStmt(e); err != nil {
					return err
				}
			}
		}

	case *ast.ForStmt:
		if s.Body != nil {
			if err := ensureActionReturnsInBlock(s.Body); err != nil {
				return err
			}
		}

	case *ast.RangeStmt:
		if s.Body != nil {
			if err := ensureActionReturnsInBlock(s.Body); err != nil {
				return err
			}
		}

	case *ast.SwitchStmt:
		for _, bodyStmt := range s.Body.List {
			cc, ok := bodyStmt.(*ast.CaseClause)
			if !ok {
				continue
			}

			for _, caseStmt := range cc.Body {
				if err := ensureActionReturnsInStmt(caseStmt); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func normalizeActionReturn(ret *ast.ReturnStmt) error {
	if len(ret.Results) == 0 {
		return nil
	}

	core := newCompilerErrorWithHint("invalid-action-return", fmt.Sprintf("action return must be compiled into a response before this phase; got %d value(s)", len(ret.Results)), "Return a single value from an Action. The compiler will turn that value into an Action response.")
	return &invalidActionReturnError{core: core, pos: ret.Pos()}
}

func blockEndsWithReturn(block *ast.BlockStmt) bool {
	if block == nil || len(block.List) == 0 {
		return false
	}

	_, ok := block.List[len(block.List)-1].(*ast.ReturnStmt)
	return ok
}

func actionNilReturn() *ast.ReturnStmt {
	return &ast.ReturnStmt{
		Results: []ast.Expr{ast.NewIdent("nil")},
	}
}
