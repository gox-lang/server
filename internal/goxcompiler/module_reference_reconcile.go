package gox

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
	"unicode"
)

type importedModuleRef struct {
	Module    *Module
	FieldName string
}

func ReconcileModuleReferences(graph *ModuleGraph) error {
	for _, mod := range graph.Modules {
		if err := reconcileModuleDependencies(mod, graph); err != nil {
			return err
		}

		if err := reconcileModuleSelectors(mod, graph); err != nil {
			return err
		}
	}

	return nil
}

func isExportedName(name string) bool {
	if name == "" {
		return false
	}

	r := []rune(name)[0]
	return unicode.IsUpper(r)
}

func reconcileModuleDependencies(mod *Module, graph *ModuleGraph) error {
	for _, imp := range mod.GoxImports {
		child := graph.ByPath[imp.ResolvedPath]
		if child == nil {
			return newCompilerErrorWithHint("missing-resolved-module", fmt.Sprintf("missing resolved module for import %q", imp.Path), "Check that the imported .gox file exists and can be resolved.")
		}

		fieldName := imp.Name
		if fieldName == "" {
			fieldName = child.Name
		}

		fieldName = lowerFirst(fieldName)

		if err := addDependencyField(mod.File, mod.Name, fieldName, child.Name); err != nil {
			return err
		}

		if err := addDependencyConstructorParam(mod.File, mod.Name, fieldName, child.Name); err != nil {
			return err
		}

		if err := addDependencyConstructorValue(mod.File, mod.Name, fieldName, child.Name); err != nil {
			return err
		}
	}

	return nil
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}

	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func upperFirst(s string) string {
	if s == "" {
		return ""
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

func addDependencyConstructorParam(
	file *ast.File,
	moduleName string,
	fieldName string,
	childModuleName string,
) error {
	constructor := findConstructor(file, moduleName)
	if constructor == nil {
		return newCompilerErrorWithHint("module-constructor-missing", fmt.Sprintf("constructor %s not found", GoxModuleConstructorName(moduleName)), "Regenerate the module wrapper or check the module declaration.")
	}

	if constructor.Type.Params == nil {
		constructor.Type.Params = &ast.FieldList{}
	}

	constructor.Type.Params.List = append(constructor.Type.Params.List, &ast.Field{
		Names: []*ast.Ident{
			ast.NewIdent(fieldName),
		},
		Type: &ast.StarExpr{
			X: ast.NewIdent(GoxModuleName(childModuleName)),
		},
	})

	return nil
}

func addDependencyField(
	file *ast.File,
	moduleName string,
	fieldName string,
	childModuleName string,
) error {
	structType := findModuleStruct(file, moduleName)
	if structType == nil {
		return newCompilerErrorWithHint("module-struct-missing", fmt.Sprintf("module struct %s not found", GoxModuleName(moduleName)), "Regenerate the module wrapper or check the module declaration.")
	}

	structType.Fields.List = append(structType.Fields.List, &ast.Field{
		Names: []*ast.Ident{
			ast.NewIdent(fieldName),
		},
		Type: &ast.StarExpr{
			X: ast.NewIdent(GoxModuleName(childModuleName)),
		},
	})

	return nil
}
func findModuleStruct(file *ast.File, moduleName string) *ast.StructType {
	targetName := GoxModuleName(moduleName)

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}

		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			if ts.Name.Name != targetName {
				continue
			}

			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return nil
			}

			return st
		}
	}

	return nil
}

func addDependencyConstructorValue(
	file *ast.File,
	moduleName string,
	fieldName string,
	childModuleName string,
) error {
	constructor := findConstructor(file, moduleName)
	if constructor == nil {
		return newCompilerErrorWithHint("module-constructor-missing", fmt.Sprintf("constructor New%s not found", moduleName), "Regenerate the module wrapper or check the module declaration.")
	}

	lit := findConstructorCompositeLit(constructor, moduleName)
	if lit == nil {
		return newCompilerErrorWithHint(
			"invalid-module-constructor",
			fmt.Sprintf("constructor %s does not return &%s literal", GoxModuleConstructorName(moduleName), GoxModuleName(moduleName)),
			"Regenerate the module wrapper or check the module constructor.",
		)
	}

	lit.Elts = append(lit.Elts, &ast.KeyValueExpr{
		Key:   ast.NewIdent(fieldName),
		Value: ast.NewIdent(fieldName),
	})

	return nil
}

func findConstructor(file *ast.File, moduleName string) *ast.FuncDecl {
	name := GoxModuleConstructorName(moduleName)

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}

	return nil
}

func findConstructorCompositeLit(fn *ast.FuncDecl, moduleName string) *ast.CompositeLit {
	if fn.Body == nil {
		return nil
	}

	targetType := GoxModuleName(moduleName)

	for _, stmt := range fn.Body.List {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			continue
		}

		unary, ok := ret.Results[0].(*ast.UnaryExpr)
		if !ok || unary.Op != token.AND {
			continue
		}

		lit, ok := unary.X.(*ast.CompositeLit)
		if !ok {
			continue
		}

		id, ok := lit.Type.(*ast.Ident)
		if !ok || id.Name != targetType {
			continue
		}

		return lit
	}

	return nil
}

func reconcileModuleSelectors(mod *Module, graph *ModuleGraph) error {
	importsByName := map[string]*importedModuleRef{}

	for _, imp := range mod.GoxImports {
		child := graph.ByPath[imp.ResolvedPath]
		if child == nil {
			return newCompilerErrorWithHint("missing-resolved-module", fmt.Sprintf("missing resolved module for import %q", imp.Path), "Check that the imported .gox file exists and can be resolved.")
		}

		name := imp.Name
		if name == "" {
			name = child.Name
		}

		fieldName := lowerFirst(name)

		ref := &importedModuleRef{
			Module:    child,
			FieldName: fieldName,
		}

		importsByName[name] = ref
		importsByName[lowerFirst(name)] = ref
	}

	if len(importsByName) == 0 {
		return nil
	}

	rewriteImportedSelectorsInFile(mod.File, importsByName)

	return nil
}

func rewriteImportedSelectorsInFile(file *ast.File, importsByName map[string]*importedModuleRef) {
	for _, decl := range file.Decls {
		rewriteImportedSelectorsInDecl(decl, importsByName)
	}
}

func rewriteImportedSelectorsInDecl(decl ast.Decl, importsByName map[string]*importedModuleRef) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Type != nil {
			rewriteImportedSelectorsInFuncType(d.Type, importsByName)
		}
		if d.Body != nil {
			bodyImports := importsByName
			if d.Type != nil && d.Type.Params != nil {
				bodyImports = copyImportedModuleRefs(importsByName)
				for _, p := range flattenFuncParams(d.Type.Params) {
					if p.Name != "" {
						delete(bodyImports, p.Name)
					}
				}
			}
			rewriteImportedSelectorsInBlock(d.Body, bodyImports)
		}

	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.ValueSpec:
				if s.Type != nil {
					rewriteImportedSelectorExprPtr(&s.Type, importsByName)
				}
				for i := range s.Values {
					rewriteImportedSelectorExprPtr(&s.Values[i], importsByName)
				}

			case *ast.TypeSpec:
				if s.Type != nil {
					rewriteImportedSelectorExprPtr(&s.Type, importsByName)
				}
			}
		}
	}
}

func copyImportedModuleRefs(in map[string]*importedModuleRef) map[string]*importedModuleRef {
	out := make(map[string]*importedModuleRef, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func rewriteImportedSelectorsInBlock(block *ast.BlockStmt, importsByName map[string]*importedModuleRef) {
	for _, stmt := range block.List {
		rewriteImportedSelectorsInStmt(stmt, importsByName)
	}
}

func rewriteImportedSelectorsInStmt(stmt ast.Stmt, importsByName map[string]*importedModuleRef) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		rewriteImportedSelectorExprPtr(&s.X, importsByName)

	case *ast.AssignStmt:
		for i := range s.Lhs {
			rewriteImportedSelectorExprPtr(&s.Lhs[i], importsByName)
		}
		for i := range s.Rhs {
			rewriteImportedSelectorExprPtr(&s.Rhs[i], importsByName)
		}

	case *ast.ReturnStmt:
		for i := range s.Results {
			rewriteImportedSelectorExprPtr(&s.Results[i], importsByName)
		}

	case *ast.IfStmt:
		if s.Init != nil {
			rewriteImportedSelectorsInStmt(s.Init, importsByName)
		}
		if s.Cond != nil {
			rewriteImportedSelectorExprPtr(&s.Cond, importsByName)
		}
		rewriteImportedSelectorsInBlock(s.Body, importsByName)

		if s.Else != nil {
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				rewriteImportedSelectorsInBlock(e, importsByName)
			case *ast.IfStmt:
				rewriteImportedSelectorsInStmt(e, importsByName)
			}
		}

	case *ast.ForStmt:
		if s.Init != nil {
			rewriteImportedSelectorsInStmt(s.Init, importsByName)
		}
		if s.Cond != nil {
			rewriteImportedSelectorExprPtr(&s.Cond, importsByName)
		}
		if s.Post != nil {
			rewriteImportedSelectorsInStmt(s.Post, importsByName)
		}
		rewriteImportedSelectorsInBlock(s.Body, importsByName)

	case *ast.RangeStmt:
		if s.Key != nil {
			rewriteImportedSelectorExprPtr(&s.Key, importsByName)
		}
		if s.Value != nil {
			rewriteImportedSelectorExprPtr(&s.Value, importsByName)
		}
		rewriteImportedSelectorExprPtr(&s.X, importsByName)
		rewriteImportedSelectorsInBlock(s.Body, importsByName)

	case *ast.BlockStmt:
		rewriteImportedSelectorsInBlock(s, importsByName)
	}
}

func rewriteImportedSelectorExprPtr(expr *ast.Expr, importsByName map[string]*importedModuleRef) {
	if expr == nil || *expr == nil {
		return
	}

	switch e := (*expr).(type) {
	case *ast.SelectorExpr:
		left, ok := e.X.(*ast.Ident)
		if ok {
			if ref, exists := importsByName[left.Name]; exists {
				child := ref.Module
				member := e.Sel.Name

				if moduleHasExportedFunc(child, member) || moduleHasExportedVar(child, member) {
					*expr = &ast.SelectorExpr{
						X: &ast.SelectorExpr{
							X:   ast.NewIdent("m"),
							Sel: ast.NewIdent(ref.FieldName),
						},
						Sel: ast.NewIdent(member),
					}
					return
				}

				if moduleHasExportedConst(child, member) {
					*expr = ast.NewIdent(GoxConstName(child.Name, member))
					return
				}

				if moduleHasExportedType(child, member) {
					*expr = ast.NewIdent(GoxTypeName(child.Name, member))
					return
				}
			}
		}

		rewriteImportedSelectorExprPtr(&e.X, importsByName)

	case *ast.CallExpr:
		rewriteImportedSelectorExprPtr(&e.Fun, importsByName)
		for i := range e.Args {
			rewriteImportedSelectorExprPtr(&e.Args[i], importsByName)
		}

	case *ast.BinaryExpr:
		rewriteImportedSelectorExprPtr(&e.X, importsByName)
		rewriteImportedSelectorExprPtr(&e.Y, importsByName)

	case *ast.UnaryExpr:
		rewriteImportedSelectorExprPtr(&e.X, importsByName)

	case *ast.ParenExpr:
		rewriteImportedSelectorExprPtr(&e.X, importsByName)

	case *ast.IndexExpr:
		rewriteImportedSelectorExprPtr(&e.X, importsByName)
		rewriteImportedSelectorExprPtr(&e.Index, importsByName)

	case *ast.SliceExpr:
		rewriteImportedSelectorExprPtr(&e.X, importsByName)
		if e.Low != nil {
			rewriteImportedSelectorExprPtr(&e.Low, importsByName)
		}
		if e.High != nil {
			rewriteImportedSelectorExprPtr(&e.High, importsByName)
		}
		if e.Max != nil {
			rewriteImportedSelectorExprPtr(&e.Max, importsByName)
		}

	case *ast.CompositeLit:
		if e.Type != nil {
			rewriteImportedSelectorExprPtr(&e.Type, importsByName)
		}
		for i := range e.Elts {
			rewriteImportedSelectorExprPtr(&e.Elts[i], importsByName)
		}

	case *ast.KeyValueExpr:
		rewriteImportedSelectorExprPtr(&e.Value, importsByName)

	case *ast.FuncLit:
		if e.Type != nil {
			rewriteImportedSelectorsInFuncType(e.Type, importsByName)
		}
		if e.Body != nil {
			rewriteImportedSelectorsInBlock(e.Body, importsByName)
		}
	}
}

func rewriteImportedSelectorsInFuncType(ft *ast.FuncType, importsByName map[string]*importedModuleRef) {
	if ft.Params != nil {
		for _, field := range ft.Params.List {
			if field.Type != nil {
				rewriteImportedSelectorExprPtr(&field.Type, importsByName)
			}
		}
	}

	if ft.Results != nil {
		for _, field := range ft.Results.List {
			if field.Type != nil {
				rewriteImportedSelectorExprPtr(&field.Type, importsByName)
			}
		}
	}
}

func moduleHasExportedFunc(mod *Module, name string) bool {
	if !isExportedName(name) {
		return false
	}

	for _, fn := range mod.Funcs {
		if fn.Name == name {
			return true
		}
	}

	return false
}

func moduleHasExportedVar(mod *Module, name string) bool {
	if !isExportedName(name) {
		return false
	}

	for _, v := range mod.Vars {
		if v.Name == name {
			return true
		}
	}

	return false
}

func moduleHasExportedConst(mod *Module, name string) bool {
	if !isExportedName(name) {
		return false
	}

	for _, c := range mod.Consts {
		if c.Name == name {
			return true
		}
	}

	return false
}

func moduleHasExportedType(mod *Module, name string) bool {
	if !isExportedName(name) {
		return false
	}

	for _, t := range mod.Types {
		if t.Name == name {
			return true
		}
	}

	return false
}
