package gox

import "go/ast"

func BindModuleMethodCallsWithOrigins(file *ast.File, receiverName string, origins *OriginTable) error {
	methodSet := collectModuleMethodNames(file)
	if len(methodSet) == 0 {
		return nil
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Recv == nil {
			continue
		}

		scopes := newScopeStack()
		if fn.Type.Params != nil {
			declareFieldNames(scopes, fn.Type.Params.List)
		}

		bindMethodCallsInBlock(fn.Body, methodSet, receiverName, scopes)
	}
	if origins != nil {
		origins.AnchorGeneratedSelectors(file, receiverName)
	}

	return nil
}

func collectModuleMethodNames(file *ast.File) map[string]struct{} {
	methods := map[string]struct{}{}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}

		// TransformFunctionsToMethods has already attached receivers to all module
		// functions. Only those functions need receiver-qualified calls.
		if fn.Recv == nil {
			continue
		}

		methods[fn.Name.Name] = struct{}{}
	}

	return methods
}

func bindMethodCallsInBlock(block *ast.BlockStmt, methods map[string]struct{}, receiverName string, scopes *scopeStack) {
	if block == nil {
		return
	}

	scopes.push()
	defer scopes.pop()

	for _, stmt := range block.List {
		bindMethodCallsInStmt(stmt, methods, receiverName, scopes)
	}
}

func bindMethodCallsInStmt(stmt ast.Stmt, methods map[string]struct{}, receiverName string, scopes *scopeStack) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		for i := range s.Rhs {
			bindMethodCallsInExprPtr(&s.Rhs[i], methods, receiverName, scopes)
		}

		if s.Tok.String() == ":=" {
			for _, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					scopes.declare(id.Name)
					continue
				}
				bindMethodCallsInExpr(lhs, methods, receiverName, scopes)
			}
			return
		}

		for _, lhs := range s.Lhs {
			bindMethodCallsInExpr(lhs, methods, receiverName, scopes)
		}

	case *ast.DeclStmt:
		bindMethodCallsInDecl(s.Decl, methods, receiverName, scopes)

	case *ast.ExprStmt:
		bindMethodCallsInExprPtr(&s.X, methods, receiverName, scopes)

	case *ast.ReturnStmt:
		for i := range s.Results {
			bindMethodCallsInExprPtr(&s.Results[i], methods, receiverName, scopes)
		}

	case *ast.IncDecStmt:
		bindMethodCallsInExpr(s.X, methods, receiverName, scopes)

	case *ast.IfStmt:
		scopes.push()
		defer scopes.pop()

		if s.Init != nil {
			bindMethodCallsInStmt(s.Init, methods, receiverName, scopes)
		}
		bindMethodCallsInExpr(s.Cond, methods, receiverName, scopes)
		bindMethodCallsInBlock(s.Body, methods, receiverName, scopes)
		if s.Else != nil {
			bindMethodCallsInStmt(s.Else, methods, receiverName, scopes)
		}

	case *ast.ForStmt:
		scopes.push()
		defer scopes.pop()

		if s.Init != nil {
			bindMethodCallsInStmt(s.Init, methods, receiverName, scopes)
		}
		bindMethodCallsInExpr(s.Cond, methods, receiverName, scopes)
		if s.Post != nil {
			bindMethodCallsInStmt(s.Post, methods, receiverName, scopes)
		}
		bindMethodCallsInBlock(s.Body, methods, receiverName, scopes)

	case *ast.RangeStmt:
		bindMethodCallsInExpr(s.X, methods, receiverName, scopes)

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
				bindMethodCallsInExpr(s.Key, methods, receiverName, scopes)
			}
			if s.Value != nil {
				bindMethodCallsInExpr(s.Value, methods, receiverName, scopes)
			}
		}
		bindMethodCallsInBlock(s.Body, methods, receiverName, scopes)

	case *ast.SwitchStmt:
		scopes.push()
		defer scopes.pop()

		if s.Init != nil {
			bindMethodCallsInStmt(s.Init, methods, receiverName, scopes)
		}
		bindMethodCallsInExpr(s.Tag, methods, receiverName, scopes)
		if s.Body != nil {
			for _, stmt := range s.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					bindMethodCallsInStmt(stmt, methods, receiverName, scopes)
					continue
				}
				for _, expr := range clause.List {
					bindMethodCallsInExpr(expr, methods, receiverName, scopes)
				}
				for _, bodyStmt := range clause.Body {
					bindMethodCallsInStmt(bodyStmt, methods, receiverName, scopes)
				}
			}
		}

	case *ast.TypeSwitchStmt:
		if s.Init != nil {
			bindMethodCallsInStmt(s.Init, methods, receiverName, scopes)
		}
		if s.Assign != nil {
			bindMethodCallsInStmt(s.Assign, methods, receiverName, scopes)
		}
		if s.Body != nil {
			for _, stmt := range s.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					bindMethodCallsInStmt(stmt, methods, receiverName, scopes)
					continue
				}
				for _, bodyStmt := range clause.Body {
					bindMethodCallsInStmt(bodyStmt, methods, receiverName, scopes)
				}
			}
		}

	case *ast.SelectStmt:
		if s.Body != nil {
			for _, stmt := range s.Body.List {
				clause, ok := stmt.(*ast.CommClause)
				if !ok {
					bindMethodCallsInStmt(stmt, methods, receiverName, scopes)
					continue
				}
				if clause.Comm != nil {
					bindMethodCallsInStmt(clause.Comm, methods, receiverName, scopes)
				}
				for _, bodyStmt := range clause.Body {
					bindMethodCallsInStmt(bodyStmt, methods, receiverName, scopes)
				}
			}
		}

	case *ast.GoStmt:
		bindMethodCallsInExpr(s.Call, methods, receiverName, scopes)

	case *ast.DeferStmt:
		bindMethodCallsInExpr(s.Call, methods, receiverName, scopes)

	case *ast.BlockStmt:
		bindMethodCallsInBlock(s, methods, receiverName, scopes)
	}
}

func bindMethodCallsInDecl(decl ast.Decl, methods map[string]struct{}, receiverName string, scopes *scopeStack) {
	gen, ok := decl.(*ast.GenDecl)
	if !ok {
		return
	}

	for _, spec := range gen.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}

		for i := range valueSpec.Values {
			bindMethodCallsInExprPtr(&valueSpec.Values[i], methods, receiverName, scopes)
		}
		for _, name := range valueSpec.Names {
			scopes.declare(name.Name)
		}
	}
}

func bindMethodCallsInExprPtr(expr *ast.Expr, methods map[string]struct{}, receiverName string, scopes *scopeStack) {
	if expr == nil || *expr == nil {
		return
	}

	if id, ok := (*expr).(*ast.Ident); ok && shouldBindMethodCall(id.Name, methods, scopes) {
		*expr = &ast.SelectorExpr{
			X:   ast.NewIdent(receiverName),
			Sel: id,
		}
		return
	}

	bindMethodCallsInExpr(*expr, methods, receiverName, scopes)
}

func bindMethodCallsInExpr(expr ast.Expr, methods map[string]struct{}, receiverName string, scopes *scopeStack) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *ast.CallExpr:
		if id, ok := e.Fun.(*ast.Ident); ok && shouldBindMethodCall(id.Name, methods, scopes) {
			e.Fun = &ast.SelectorExpr{
				X:   ast.NewIdent(receiverName),
				Sel: id,
			}
		} else {
			bindMethodCallsInExpr(e.Fun, methods, receiverName, scopes)
		}

		for i := range e.Args {
			bindMethodCallsInExprPtr(&e.Args[i], methods, receiverName, scopes)
		}

	case *ast.SelectorExpr:
		bindMethodCallsInExpr(e.X, methods, receiverName, scopes)

	case *ast.BinaryExpr:
		bindMethodCallsInExprPtr(&e.X, methods, receiverName, scopes)
		bindMethodCallsInExprPtr(&e.Y, methods, receiverName, scopes)

	case *ast.UnaryExpr:
		bindMethodCallsInExprPtr(&e.X, methods, receiverName, scopes)

	case *ast.ParenExpr:
		bindMethodCallsInExprPtr(&e.X, methods, receiverName, scopes)

	case *ast.IndexExpr:
		bindMethodCallsInExprPtr(&e.X, methods, receiverName, scopes)
		bindMethodCallsInExprPtr(&e.Index, methods, receiverName, scopes)

	case *ast.SliceExpr:
		bindMethodCallsInExprPtr(&e.X, methods, receiverName, scopes)
		bindMethodCallsInExprPtr(&e.Low, methods, receiverName, scopes)
		bindMethodCallsInExprPtr(&e.High, methods, receiverName, scopes)
		bindMethodCallsInExprPtr(&e.Max, methods, receiverName, scopes)

	case *ast.CompositeLit:
		for i := range e.Elts {
			bindMethodCallsInExprPtr(&e.Elts[i], methods, receiverName, scopes)
		}

	case *ast.KeyValueExpr:
		bindMethodCallsInExprPtr(&e.Key, methods, receiverName, scopes)
		bindMethodCallsInExprPtr(&e.Value, methods, receiverName, scopes)

	case *ast.FuncLit:
		scopes.push()
		defer scopes.pop()
		if e.Type.Params != nil {
			declareFieldNames(scopes, e.Type.Params.List)
		}
		bindMethodCallsInBlock(e.Body, methods, receiverName, scopes)
	}
}

func shouldBindMethodCall(name string, methods map[string]struct{}, scopes *scopeStack) bool {
	if name == "_" || name == "nil" || name == "true" || name == "false" {
		return false
	}
	if scopes.exists(name) {
		return false
	}
	_, ok := methods[name]
	return ok
}

func declareFieldNames(scopes *scopeStack, fields []*ast.Field) {
	for _, field := range fields {
		for _, name := range field.Names {
			scopes.declare(name.Name)
		}
	}
}
