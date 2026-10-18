package gox

import (
	"go/ast"
	"go/token"
)

func BundleModules(graph *ModuleGraph) (*Bundle, error) {
	if err := bindImportedComponentCalls(graph); err != nil {
		return nil, err
	}
	if err := ReconcileModuleReferences(graph); err != nil {
		return nil, err
	}

	bundle := &Bundle{
		Entry:     graph.Entry,
		Modules:   graph.Modules,
		GoPackage: "main",
		Origins:   NewOriginTable(),
		Sources:   make(map[string]string, len(graph.Modules)),
	}
	for _, mod := range graph.Modules {
		bundle.Origins.Merge(mod.Origins)
		bundle.Sources[mod.Path] = mod.Source
	}

	imports := collectBundleImports(graph.Modules)
	bundle.GoImports = imports
	if importDecl := buildImportDecl(imports); importDecl != nil {
		bundle.Decls = append(bundle.Decls, importDecl)
	}

	for _, mod := range graph.Modules {
		for _, decl := range mod.File.Decls {
			if isImportDecl(decl) {
				continue
			}

			bundle.Decls = append(bundle.Decls, decl)
		}
	}

	AddBundleConstructor(bundle)

	bundle.Decls = append(bundle.Decls, BuildActionParamsDecls(bundle)...)

	if err := AddBundleExports(bundle); err != nil {
		return nil, err
	}

	SpecializeRuntimeAnyCalls(bundle.Decls)

	// Rebuild import declaration after adding generated runtime imports.
	// User import bindings are preserved semantically; redundant explicit natural
	// aliases may be canonicalized while modules are merged. Only runtime imports
	// are injected by the compiler. Unused user imports should fail like normal Go
	// code instead of being silently removed.
	bundle.Decls = replaceImportDecl(bundle.Decls, buildImportDecl(bundle.GoImports))
	return bundle, nil
}

func collectBundleImports(modules []*Module) []ImportDecl {
	seen := map[string]ImportDecl{}

	for _, mod := range modules {
		for _, imp := range mod.GoImports {
			imp = normalizeRedundantImportAlias(mod, imp)
			key := imp.Alias + "|" + imp.Path

			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = imp
		}
	}

	imports := make([]ImportDecl, 0, len(seen))

	for _, imp := range seen {
		imports = append(imports, imp)
	}

	return imports
}

// normalizeRedundantImportAlias canonicalizes an explicit alias that is equal
// to the imported package's declared name. In Go these two forms bind the same
// identifier:
//
//	import "time"
//	import time "time"
//
// GOX bundles multiple source modules into one Go file, so keeping both forms
// would produce duplicate bindings after module imports are merged. Custom
// aliases, dot imports and blank imports remain distinct because their binding
// semantics are different.
func normalizeRedundantImportAlias(mod *Module, imp ImportDecl) ImportDecl {
	if imp.Alias == "" || imp.Alias == "_" || imp.Alias == "." || mod == nil || mod.Semantic == nil || mod.Semantic.Package == nil {
		return imp
	}

	for _, pkg := range mod.Semantic.Package.Imports() {
		if pkg.Path() == imp.Path && pkg.Name() == imp.Alias {
			imp.Alias = ""
			return imp
		}
	}

	return imp
}

func buildImportDecl(imports []ImportDecl) ast.Decl {
	if len(imports) == 0 {
		return nil
	}

	specs := make([]ast.Spec, 0, len(imports))

	for _, imp := range imports {
		importSpec := &ast.ImportSpec{
			Path: &ast.BasicLit{
				Kind:  token.STRING,
				Value: `"` + imp.Path + `"`,
			},
		}

		if imp.Alias != "" {
			importSpec.Name = ast.NewIdent(imp.Alias)
		}

		specs = append(specs, importSpec)
	}

	return &ast.GenDecl{
		Tok:   token.IMPORT,
		Specs: specs,
	}
}

func isImportDecl(decl ast.Decl) bool {
	gen, ok := decl.(*ast.GenDecl)
	return ok && gen.Tok == token.IMPORT
}

func insertDeclsAfterImports(decls []ast.Decl, insert []ast.Decl) []ast.Decl {
	if len(insert) == 0 {
		return decls
	}

	insertAt := 0

	for i, decl := range decls {
		if isImportDecl(decl) {
			insertAt = i + 1
		}
	}

	out := make([]ast.Decl, 0, len(decls)+len(insert))
	out = append(out, decls[:insertAt]...)
	out = append(out, insert...)
	out = append(out, decls[insertAt:]...)

	return out
}

func replaceImportDecl(decls []ast.Decl, importDecl ast.Decl) []ast.Decl {
	out := []ast.Decl{}
	replaced := false

	for _, decl := range decls {
		if isImportDecl(decl) {
			if !replaced && importDecl != nil {
				out = append(out, importDecl)
			}
			replaced = true
			continue
		}

		out = append(out, decl)
	}

	if !replaced && importDecl != nil {
		out = append([]ast.Decl{importDecl}, out...)
	}

	return out
}

func pruneUnusedImports(imports []ImportDecl, decls []ast.Decl) []ImportDecl {
	used := map[string]struct{}{}

	ast.Inspect(&ast.File{Decls: decls}, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		used[id.Name] = struct{}{}
		return true
	})

	out := make([]ImportDecl, 0, len(imports))
	for _, imp := range imports {
		if imp.Alias == "_" || imp.Alias == "." {
			out = append(out, imp)
			continue
		}

		name := imp.Alias
		if name == "" {
			name = defaultImportName(imp.Path)
		}

		if _, ok := used[name]; ok {
			out = append(out, imp)
		}
	}

	return out
}

func defaultImportName(path string) string {
	name := path
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			name = path[i+1:]
			break
		}
	}
	return name
}

func bundleUsesGeneratedIO(bundle *Bundle) bool {
	for _, mod := range bundle.Modules {
		for _, fn := range mod.Funcs {
			if fn.Kind == FuncComponent {
				return true
			}
		}
	}

	return false
}
