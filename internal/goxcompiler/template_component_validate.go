package gox

import "go/ast"

type templateComponentSymbols struct {
	funcKinds    map[string]FuncKind
	globalVars   map[string]struct{}
	localsByFunc map[string]map[string]struct{}
}

func ValidateTemplateComponentReferences(mod *Module, templates []TemplateReturn) error {
	if mod == nil || mod.File == nil || len(templates) == 0 {
		return nil
	}

	symbols := collectTemplateComponentSymbols(mod)

	for i := range templates {
		tpl := &templates[i]
		if tpl == nil || tpl.Template == nil || tpl.Template.Root == nil {
			continue
		}

		locals := symbols.localsByFunc[tpl.FuncName]
		if err := validateTemplateComponentNode(tpl.Template.Root, tpl.FuncName, locals, symbols); err != nil {
			return err
		}
	}

	return nil
}

func collectTemplateComponentSymbols(mod *Module) templateComponentSymbols {
	symbols := templateComponentSymbols{
		funcKinds:    map[string]FuncKind{},
		globalVars:   map[string]struct{}{},
		localsByFunc: map[string]map[string]struct{}{},
	}

	for _, fn := range mod.Funcs {
		symbols.funcKinds[fn.Name] = fn.Kind
	}

	for _, decl := range mod.File.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range valueSpec.Names {
					if i < len(valueSpec.Values) && isComponentFuncLiteral(valueSpec.Values[i]) {
						symbols.globalVars[name.Name] = struct{}{}
					}
				}
			}

		case *ast.FuncDecl:
			if d.Name == nil || d.Body == nil {
				continue
			}
			locals := map[string]struct{}{}
			collectLocalComponentFuncLiterals(d.Body, locals)
			if len(locals) > 0 {
				symbols.localsByFunc[d.Name.Name] = locals
			}
		}
	}

	return symbols
}

func collectLocalComponentFuncLiterals(block *ast.BlockStmt, locals map[string]struct{}) {
	if block == nil {
		return
	}

	for _, stmt := range block.List {
		switch s := stmt.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || i >= len(s.Rhs) {
					continue
				}
				if isComponentFuncLiteral(s.Rhs[i]) {
					locals[id.Name] = struct{}{}
				}
			}

		case *ast.DeclStmt:
			gen, ok := s.Decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range valueSpec.Names {
					if i < len(valueSpec.Values) && isComponentFuncLiteral(valueSpec.Values[i]) {
						locals[name.Name] = struct{}{}
					}
				}
			}

		case *ast.IfStmt:
			if initBlock := stmtInitBlock(s.Init); initBlock != nil {
				collectLocalComponentFuncLiterals(initBlock, locals)
			}
			collectLocalComponentFuncLiterals(s.Body, locals)
			collectLocalComponentFuncLiteralsFromStmt(s.Else, locals)

		case *ast.ForStmt:
			if initBlock := stmtInitBlock(s.Init); initBlock != nil {
				collectLocalComponentFuncLiterals(initBlock, locals)
			}
			collectLocalComponentFuncLiterals(s.Body, locals)

		case *ast.RangeStmt:
			collectLocalComponentFuncLiterals(s.Body, locals)

		case *ast.SwitchStmt:
			if initBlock := stmtInitBlock(s.Init); initBlock != nil {
				collectLocalComponentFuncLiterals(initBlock, locals)
			}
			collectLocalComponentFuncLiteralsFromCaseClauses(s.Body, locals)

		case *ast.TypeSwitchStmt:
			if initBlock := stmtInitBlock(s.Init); initBlock != nil {
				collectLocalComponentFuncLiterals(initBlock, locals)
			}
			collectLocalComponentFuncLiteralsFromCaseClauses(s.Body, locals)

		case *ast.SelectStmt:
			if s.Body != nil {
				for _, stmt := range s.Body.List {
					clause, ok := stmt.(*ast.CommClause)
					if !ok {
						continue
					}
					collectLocalComponentFuncLiterals(&ast.BlockStmt{List: clause.Body}, locals)
				}
			}

		case *ast.BlockStmt:
			collectLocalComponentFuncLiterals(s, locals)
		}
	}
}

func collectLocalComponentFuncLiteralsFromStmt(stmt ast.Stmt, locals map[string]struct{}) {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		collectLocalComponentFuncLiterals(s, locals)
	case *ast.IfStmt:
		collectLocalComponentFuncLiterals(s.Body, locals)
		collectLocalComponentFuncLiteralsFromStmt(s.Else, locals)
	}
}

func collectLocalComponentFuncLiteralsFromCaseClauses(block *ast.BlockStmt, locals map[string]struct{}) {
	if block == nil {
		return
	}
	for _, stmt := range block.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		collectLocalComponentFuncLiterals(&ast.BlockStmt{List: clause.Body}, locals)
	}
}

func stmtInitBlock(stmt ast.Stmt) *ast.BlockStmt {
	if stmt == nil {
		return nil
	}
	return &ast.BlockStmt{List: []ast.Stmt{stmt}}
}

func isComponentFuncLiteral(expr ast.Expr) bool {
	fn, ok := expr.(*ast.FuncLit)
	if !ok || fn.Type == nil || fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}

	first := fn.Type.Params.List[0]
	if len(first.Names) == 0 || first.Names[0].Name != "goxWriter" {
		return false
	}

	return isGoxWriterType(first.Type)
}

func isGoxWriterType(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Writer" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "gox"
}

func validateTemplateComponentNode(node TemplateNode, funcName string, locals map[string]struct{}, symbols templateComponentSymbols) error {
	switch n := node.(type) {
	case *FragmentNode:
		for _, child := range n.Children {
			if err := validateTemplateComponentNode(child, funcName, locals, symbols); err != nil {
				return err
			}
		}

	case *ElementNode:
		for _, child := range n.Children {
			if err := validateTemplateComponentNode(child, funcName, locals, symbols); err != nil {
				return err
			}
		}

	case *DirectiveNode:
		for _, child := range n.Children {
			if err := validateTemplateComponentNode(child, funcName, locals, symbols); err != nil {
				return err
			}
		}

	case *ComponentNode:
		if err := validateTemplateComponentRef(n, funcName, locals, symbols); err != nil {
			return err
		}
		for _, child := range n.Children {
			if err := validateTemplateComponentNode(child, funcName, locals, symbols); err != nil {
				return err
			}
		}
	}

	return nil
}

func validateTemplateComponentRef(n *ComponentNode, funcName string, locals map[string]struct{}, symbols templateComponentSymbols) error {
	if n == nil || !isSimpleComponentIdent(n.Name) {
		return nil
	}

	name := n.Name
	if _, ok := locals[name]; ok {
		return nil
	}
	if _, ok := symbols.globalVars[name]; ok {
		return nil
	}
	if kind, ok := symbols.funcKinds[name]; ok {
		if kind == FuncComponent {
			return nil
		}

		err := newTemplateCompileError(
			"template.component.invalid-reference",
			n.Pos.File,
			n.Pos.Line,
			n.Pos.Column,
			"function "+name+" is not a Gox component",
		)
		err.Hint = "Only functions returning gox.Component, gox.HTML, or gox.Html can be rendered with <&" + name + "/>. Use {{" + name + "()}} for a normal Go function result, or change the function return type to gox.Component."
		return err
	}

	return nil
}
