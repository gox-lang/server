package gox

import (
	"go/ast"
)

func BindModuleFieldIdentifiers(file *ast.File, fields []VarDecl, receiverName string) error {
	return BindModuleFieldIdentifiersWithOrigins(file, fields, receiverName, nil)
}

func BindModuleFieldIdentifiersWithOrigins(file *ast.File, fields []VarDecl, receiverName string, origins *OriginTable) error {
	fieldSet := make(map[string]struct{}, len(fields))

	for _, field := range fields {
		fieldSet[field.Name] = struct{}{}
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Recv == nil {
			continue
		}

		localScopes := newScopeStack()

		// params son locales
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				for _, name := range field.Names {
					localScopes.declare(name.Name)
				}
			}
		}

		bindBlock(fn.Body, fieldSet, receiverName, localScopes)
	}
	if origins != nil {
		origins.AnchorGeneratedSelectors(file, receiverName)
	}

	return nil
}

type scopeStack struct {
	scopes []map[string]struct{}
}

func newScopeStack() *scopeStack {
	s := &scopeStack{}
	s.push()
	return s
}

func (s *scopeStack) push() {
	s.scopes = append(s.scopes, map[string]struct{}{})
}

func (s *scopeStack) pop() {
	s.scopes = s.scopes[:len(s.scopes)-1]
}

func (s *scopeStack) declare(name string) {
	if name == "_" {
		return
	}

	s.scopes[len(s.scopes)-1][name] = struct{}{}
}

func (s *scopeStack) exists(name string) bool {
	for i := len(s.scopes) - 1; i >= 0; i-- {
		if _, ok := s.scopes[i][name]; ok {
			return true
		}
	}
	return false
}

func bindBlock(
	block *ast.BlockStmt,
	fields map[string]struct{},
	receiverName string,
	scopes *scopeStack,
) {
	scopes.push()
	defer scopes.pop()

	for _, stmt := range block.List {
		bindStmt(stmt, fields, receiverName, scopes)
	}
}

func bindStmt(
	stmt ast.Stmt,
	fields map[string]struct{},
	receiverName string,
	scopes *scopeStack,
) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		// Primero procesa RHS.
		for i := range s.Rhs {
			bindExprPtr(&s.Rhs[i], fields, receiverName, scopes)
		}

		if s.Tok.String() == ":=" {
			// En :=, solo los identificadores nuevos del LHS se declaran locales.
			// Pero expresiones no-ident deben procesarse: obj.Field, arr[i], etc.
			for _, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					scopes.declare(id.Name)
					continue
				}

				bindExprPtr(&lhs, fields, receiverName, scopes)
			}
		} else {
			// En =, +=, -=, etc. el LHS también puede ser field global.
			for i := range s.Lhs {
				bindExprPtr(&s.Lhs[i], fields, receiverName, scopes)
			}
		}

	case *ast.IncDecStmt:
		bindExprPtr(&s.X, fields, receiverName, scopes)

	case *ast.ExprStmt:
		bindExprPtr(&s.X, fields, receiverName, scopes)

	case *ast.DeferStmt:
		call := ast.Expr(s.Call)
		bindExprPtr(&call, fields, receiverName, scopes)
		s.Call = call.(*ast.CallExpr)

	case *ast.GoStmt:
		call := ast.Expr(s.Call)
		bindExprPtr(&call, fields, receiverName, scopes)
		s.Call = call.(*ast.CallExpr)

	case *ast.ReturnStmt:
		for i := range s.Results {
			bindExprPtr(&s.Results[i], fields, receiverName, scopes)
		}

	case *ast.IfStmt:
		if s.Init != nil {
			bindStmt(s.Init, fields, receiverName, scopes)
		}

		bindExprPtr(&s.Cond, fields, receiverName, scopes)
		bindBlock(s.Body, fields, receiverName, scopes)

		if s.Else != nil {
			switch elseNode := s.Else.(type) {
			case *ast.BlockStmt:
				bindBlock(elseNode, fields, receiverName, scopes)
			case *ast.IfStmt:
				bindStmt(elseNode, fields, receiverName, scopes)
			}
		}

	case *ast.ForStmt:
		scopes.push()
		defer scopes.pop()

		if s.Init != nil {
			bindStmt(s.Init, fields, receiverName, scopes)
		}
		if s.Cond != nil {
			bindExprPtr(&s.Cond, fields, receiverName, scopes)
		}
		if s.Post != nil {
			bindStmt(s.Post, fields, receiverName, scopes)
		}

		for _, bodyStmt := range s.Body.List {
			bindStmt(bodyStmt, fields, receiverName, scopes)
		}

	case *ast.RangeStmt:
		bindExprPtr(&s.X, fields, receiverName, scopes)

		scopes.push()
		defer scopes.pop()

		if s.Tok.String() == ":=" {
			if id, ok := s.Key.(*ast.Ident); ok {
				scopes.declare(id.Name)
			}
			if id, ok := s.Value.(*ast.Ident); ok {
				scopes.declare(id.Name)
			}
		} else {
			if s.Key != nil {
				bindExprPtr(&s.Key, fields, receiverName, scopes)
			}
			if s.Value != nil {
				bindExprPtr(&s.Value, fields, receiverName, scopes)
			}
		}

		for _, bodyStmt := range s.Body.List {
			bindStmt(bodyStmt, fields, receiverName, scopes)
		}

	case *ast.SwitchStmt:
		scopes.push()
		defer scopes.pop()

		if s.Init != nil {
			bindStmt(s.Init, fields, receiverName, scopes)
		}

		if s.Tag != nil {
			bindExprPtr(&s.Tag, fields, receiverName, scopes)
		}

		if s.Body != nil {
			for _, stmt := range s.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					bindStmt(stmt, fields, receiverName, scopes)
					continue
				}

				for i := range clause.List {
					bindExprPtr(&clause.List[i], fields, receiverName, scopes)
				}

				for _, bodyStmt := range clause.Body {
					bindStmt(bodyStmt, fields, receiverName, scopes)
				}
			}
		}

	case *ast.BranchStmt:
		return

	case *ast.BlockStmt:
		bindBlock(s, fields, receiverName, scopes)
	}
}

func bindExprPtr(
	expr *ast.Expr,
	fields map[string]struct{},
	receiverName string,
	scopes *scopeStack,
) {
	if expr == nil || *expr == nil {
		return
	}

	switch e := (*expr).(type) {
	case *ast.Ident:
		if shouldBindIdent(e.Name, fields, scopes) {
			*expr = &ast.SelectorExpr{
				X:   ast.NewIdent(receiverName),
				Sel: e,
			}
		}

	case *ast.SelectorExpr:
		bindExprPtr(&e.X, fields, receiverName, scopes)

	case *ast.CallExpr:
		bindExprPtr(&e.Fun, fields, receiverName, scopes)
		for i := range e.Args {
			bindExprPtr(&e.Args[i], fields, receiverName, scopes)
		}

	case *ast.BinaryExpr:
		bindExprPtr(&e.X, fields, receiverName, scopes)
		bindExprPtr(&e.Y, fields, receiverName, scopes)

	case *ast.UnaryExpr:
		bindExprPtr(&e.X, fields, receiverName, scopes)

	case *ast.ParenExpr:
		bindExprPtr(&e.X, fields, receiverName, scopes)

	case *ast.IndexExpr:
		bindExprPtr(&e.X, fields, receiverName, scopes)
		bindExprPtr(&e.Index, fields, receiverName, scopes)

	case *ast.SliceExpr:
		bindExprPtr(&e.X, fields, receiverName, scopes)
		if e.Low != nil {
			bindExprPtr(&e.Low, fields, receiverName, scopes)
		}
		if e.High != nil {
			bindExprPtr(&e.High, fields, receiverName, scopes)
		}
		if e.Max != nil {
			bindExprPtr(&e.Max, fields, receiverName, scopes)
		}

	case *ast.CompositeLit:
		for i := range e.Elts {
			bindExprPtr(&e.Elts[i], fields, receiverName, scopes)
		}

	case *ast.KeyValueExpr:
		bindExprPtr(&e.Value, fields, receiverName, scopes)

	case *ast.FuncLit:
		// Nuevo scope para closures.
		scopes.push()
		defer scopes.pop()

		if e.Type.Params != nil {
			for _, field := range e.Type.Params.List {
				for _, name := range field.Names {
					scopes.declare(name.Name)
				}
			}
		}

		bindBlock(e.Body, fields, receiverName, scopes)
	}
}

func shouldBindIdent(
	name string,
	fields map[string]struct{},
	scopes *scopeStack,
) bool {
	if name == "_" || name == "nil" || name == "true" || name == "false" {
		return false
	}

	if scopes.exists(name) {
		return false
	}

	_, ok := fields[name]
	return ok
}
