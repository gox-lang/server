package gox

import (
	"go/ast"
)

func RewriteTemplateReturnsWithOrigins(file *ast.File, templates []TemplateReturn, clients []ClientDecl, origins *OriginTable, environments ...*TemplateTypeEnv) ([]TemplateStaticDecl, error) {
	templateMap := make(map[string]*TemplateReturn)
	clientMap := make(map[string]ClientDecl, len(clients))
	for _, client := range clients {
		clientMap[client.Name] = client
	}

	for i := range templates {
		t := &templates[i]
		templateMap[t.ID] = t
	}

	staticIndexByName := make(map[string]int)
	baseTypeEnv := buildTemplateBaseTypeEnv(file)
	if len(environments) > 0 {
		baseTypeEnv = environments[0]
	}
	var statics []TemplateStaticDecl

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		typeEnv := templateTypeEnvForFunc(fn, baseTypeEnv)
		newBody, err := rewriteTemplateReturnsInBlock(
			file,
			fn.Body.List,
			templateMap,
			clientMap,
			staticIndexByName,
			&statics,
			typeEnv,
			templateWriterNameForFunc(file, fn),
			detectGoxReturnKind(file, fn),
			shouldAutoInjectActionsClient(file, fn),
			origins,
		)
		if err != nil {
			return nil, err
		}

		fn.Body.List = newBody
	}

	return statics, nil
}

func rewriteTemplateReturnsInBlock(
	file *ast.File,
	stmts []ast.Stmt,
	templateMap map[string]*TemplateReturn,
	clientMap map[string]ClientDecl,
	staticIndexByName map[string]int,
	statics *[]TemplateStaticDecl,
	typeEnv *TemplateTypeEnv,
	writerName string,
	returnKind GoxReturnKind,
	injectActionsClient bool,
	origins *OriginTable,
) ([]ast.Stmt, error) {
	var out []ast.Stmt

	for _, stmt := range stmts {
		if err := rewriteTemplateReturnsInNestedFuncLiterals(file, stmt, templateMap, clientMap, staticIndexByName, statics, typeEnv, origins); err != nil {
			return nil, err
		}
		switch s := stmt.(type) {

		case *ast.ReturnStmt:
			replacement, ok, err := rewriteTemplateReturnStmt(
				s,
				templateMap,
				clientMap,
				staticIndexByName,
				statics,
				typeEnv,
				writerName,
				returnKind,
				injectActionsClient,
			)
			if err != nil {
				return nil, err
			}

			if ok {
				inheritReturnReplacementOrigins(origins, replacement, s)
				out = append(out, replacement...)
				continue
			}

			replacement, ok, err = rewriteFlexibleReturnStmt(s, typeEnv, writerName, returnKind)
			if err != nil {
				return nil, err
			}
			if ok {
				inheritReturnReplacementOrigins(origins, replacement, s)
				out = append(out, replacement...)
				continue
			}

			out = append(out, stmt)

		case *ast.IfStmt:
			body, err := rewriteTemplateReturnsInBlock(
				file,
				s.Body.List,
				templateMap,
				clientMap,
				staticIndexByName,
				statics,
				typeEnv,
				writerName,
				returnKind,
				injectActionsClient,
				origins,
			)
			if err != nil {
				return nil, err
			}

			s.Body.List = body

			if s.Else != nil {
				if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
					rewrittenElse, err := rewriteTemplateReturnsInBlock(
						file,
						elseBlock.List,
						templateMap,
						clientMap,
						staticIndexByName,
						statics,
						typeEnv,
						writerName,
						returnKind,
						injectActionsClient,
						origins,
					)
					if err != nil {
						return nil, err
					}

					elseBlock.List = rewrittenElse
				}
			}

			out = append(out, s)

		case *ast.ForStmt:
			body, err := rewriteTemplateReturnsInBlock(file, s.Body.List, templateMap, clientMap, staticIndexByName, statics, typeEnv, writerName, returnKind, injectActionsClient, origins)
			if err != nil {
				return nil, err
			}
			s.Body.List = body
			out = append(out, s)

		case *ast.RangeStmt:
			body, err := rewriteTemplateReturnsInBlock(file, s.Body.List, templateMap, clientMap, staticIndexByName, statics, typeEnv, writerName, returnKind, injectActionsClient, origins)
			if err != nil {
				return nil, err
			}
			s.Body.List = body
			out = append(out, s)

		case *ast.SwitchStmt:
			if s.Body != nil {
				for _, clause := range s.Body.List {
					cc, ok := clause.(*ast.CaseClause)
					if !ok {
						continue
					}
					body, err := rewriteTemplateReturnsInBlock(file, cc.Body, templateMap, clientMap, staticIndexByName, statics, typeEnv, writerName, returnKind, injectActionsClient, origins)
					if err != nil {
						return nil, err
					}
					cc.Body = body
				}
			}
			out = append(out, s)

		case *ast.TypeSwitchStmt:
			if s.Body != nil {
				for _, clause := range s.Body.List {
					cc, ok := clause.(*ast.CaseClause)
					if !ok {
						continue
					}
					body, err := rewriteTemplateReturnsInBlock(file, cc.Body, templateMap, clientMap, staticIndexByName, statics, typeEnv, writerName, returnKind, injectActionsClient, origins)
					if err != nil {
						return nil, err
					}
					cc.Body = body
				}
			}
			out = append(out, s)

		case *ast.SelectStmt:
			if s.Body != nil {
				for _, clause := range s.Body.List {
					cc, ok := clause.(*ast.CommClause)
					if !ok {
						continue
					}
					body, err := rewriteTemplateReturnsInBlock(file, cc.Body, templateMap, clientMap, staticIndexByName, statics, typeEnv, writerName, returnKind, injectActionsClient, origins)
					if err != nil {
						return nil, err
					}
					cc.Body = body
				}
			}
			out = append(out, s)

		case *ast.BlockStmt:
			block, err := rewriteTemplateReturnsInBlock(
				file,
				s.List,
				templateMap,
				clientMap,
				staticIndexByName,
				statics,
				typeEnv,
				writerName,
				returnKind,
				injectActionsClient,
				origins,
			)
			if err != nil {
				return nil, err
			}

			s.List = block
			out = append(out, s)

		default:
			out = append(out, stmt)
		}
	}

	return out, nil
}

func rewriteTemplateReturnStmt(
	ret *ast.ReturnStmt,
	templateMap map[string]*TemplateReturn,
	clientMap map[string]ClientDecl,
	staticIndexByName map[string]int,
	statics *[]TemplateStaticDecl,
	typeEnv *TemplateTypeEnv,
	writerName string,
	returnKind GoxReturnKind,
	injectActionsClient bool,
) ([]ast.Stmt, bool, error) {
	if len(ret.Results) != 1 {
		return nil, false, nil
	}

	ident, ok := ret.Results[0].(*ast.Ident)
	if !ok {
		return nil, false, nil
	}

	tplReturn, ok := templateMap[ident.Name]
	if !ok {
		return nil, false, nil
	}

	staticName := GoxTemplateStaticName(tplReturn.ModuleName, tplReturn.FuncName)
	staticIndex, ok := staticIndexByName[staticName]
	if !ok {
		*statics = append(*statics, TemplateStaticDecl{Name: staticName})
		staticIndex = len(*statics) - 1
		staticIndexByName[staticName] = staticIndex
	}

	ctx := &TemplateGenContext{
		WriterName:          writerName,
		StaticVarName:       staticName,
		StaticLiterals:      &(*statics)[staticIndex].Literals,
		TypeEnv:             typeEnv,
		Clients:             clientMap,
		InjectActionsClient: injectActionsClient,
		InjectClientRuntime: len(clientMap) > 0,
	}

	if returnKind == GoxReturnEventReply {
		renderWriter := "goxWriter"
		renderParam := &ast.Field{
			Names: []*ast.Ident{ast.NewIdent("goxWriter")},
			Type:  &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("Writer")},
		}
		// Client islands need a fresh render context with the current asset resolver.
		if len(clientMap) > 0 {
			renderWriter = "ctx.Writer"
			renderParam = &ast.Field{
				Names: []*ast.Ident{ast.NewIdent("ctx")},
				Type:  &ast.StarExpr{X: &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("Context")}},
			}
		}
		stmts, err := GenerateTemplateStmts(tplReturn.Template, &TemplateGenContext{
			WriterName:          renderWriter,
			StaticVarName:       staticName,
			StaticLiterals:      &(*statics)[staticIndex].Literals,
			TypeEnv:             typeEnv,
			Clients:             clientMap,
			InjectActionsClient: false,
			InjectClientRuntime: len(clientMap) > 0,
		})
		if err != nil {
			return nil, false, err
		}
		return []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{&ast.FuncLit{
			Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{renderParam}}},
			Body: &ast.BlockStmt{List: stmts},
		}}}}, true, nil
	}

	stmts, err := GenerateTemplateStmts(tplReturn.Template, ctx)
	if err != nil {
		return nil, false, err
	}

	if returnKind == GoxReturnAction {
		stmts = append(actionTemplateHeaderStmts(), stmts...)
	}

	stmts = append(stmts, &ast.ReturnStmt{})

	return stmts, true, nil
}

func inheritReturnReplacementOrigins(origins *OriginTable, replacement []ast.Stmt, ret *ast.ReturnStmt) {
	if origins == nil || ret == nil {
		return
	}
	source := ast.Node(ret)
	for _, result := range ret.Results {
		if _, ok := origins.Get(result); ok {
			source = result
			break
		}
	}
	for _, stmt := range replacement {
		origins.InheritTree(stmt, source)
	}
}

func rewriteTemplateReturnsInNestedFuncLiterals(
	file *ast.File,
	node ast.Node,
	templateMap map[string]*TemplateReturn,
	clientMap map[string]ClientDecl,
	staticIndexByName map[string]int,
	statics *[]TemplateStaticDecl,
	typeEnv *TemplateTypeEnv,
	origins *OriginTable,
) error {
	var rewriteErr error
	ast.Inspect(node, func(current ast.Node) bool {
		if rewriteErr != nil {
			return false
		}
		literal, ok := current.(*ast.FuncLit)
		if !ok {
			return true
		}
		kind := GoxReturnNone
		if literal.Type != nil && literal.Type.Results != nil && len(literal.Type.Results.List) == 1 {
			if path, name, ok := goxSourceTypeIdentity(file, literal.Type.Results.List[0].Type); ok && path == goxSDKImportPath && name == "EventReply" {
				kind = GoxReturnEventReply
			}
		}
		literalTypeEnv := NewTemplateTypeEnv()
		if typeEnv != nil {
			literalTypeEnv = typeEnv.Clone()
		}
		if literal.Type != nil && literal.Type.Params != nil {
			for _, parameter := range flattenFuncParams(literal.Type.Params) {
				if parameter.Name != "" {
					literalTypeEnv.Vars[parameter.Name] = normalizeTemplateType(parameter.Type)
				}
			}
		}
		collectTemplateLocalTypes(literal.Body.List, literalTypeEnv)
		body, err := rewriteTemplateReturnsInBlock(
			file,
			literal.Body.List,
			templateMap,
			clientMap,
			staticIndexByName,
			statics,
			literalTypeEnv,
			"goxWriter",
			kind,
			false,
			origins,
		)
		if err != nil {
			rewriteErr = err
			return false
		}
		literal.Body.List = body
		return false
	})
	return rewriteErr
}

func templateWriterNameForFunc(file *ast.File, fn *ast.FuncDecl) string {
	if fn == nil {
		return "ctx.Writer"
	}

	if detectGoxReturnName(file, fn) == "Component" {
		return "goxWriter"
	}

	return "ctx.Writer"
}

func shouldAutoInjectActionsClient(file *ast.File, fn *ast.FuncDecl) bool {
	return detectGoxReturnName(file, fn) == "Page"
}
