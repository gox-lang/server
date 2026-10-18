package gox

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

func resolveCompileGoPackages(graph *ModuleGraph) ([]CompileGoPackage, error) {
	if graph == nil || strings.TrimSpace(graph.RootDir) == "" {
		return nil, nil
	}

	modulePath, err := projectGoModulePath(graph.RootDir)
	if err != nil || modulePath == "" {
		return nil, err
	}

	directImporters := make(map[string][]string)
	for _, module := range graph.Modules {
		for _, declaration := range module.GoImports {
			importPath := strings.TrimSpace(declaration.Path)
			if !isProjectGoImport(importPath, modulePath) {
				continue
			}
			directImporters[importPath] = append(directImporters[importPath], module.Path)
		}
	}
	if len(directImporters) == 0 {
		return nil, nil
	}

	packages := make(map[string]*CompileGoPackage)
	visiting := make(map[string]bool)
	var visit func(string) error
	visit = func(importPath string) error {
		if _, ok := packages[importPath]; ok || visiting[importPath] {
			return nil
		}
		visiting[importPath] = true
		defer delete(visiting, importPath)

		dir, ok := projectGoPackageDir(graph.RootDir, modulePath, importPath)
		if !ok {
			return nil
		}
		files, imports, err := readProjectGoPackage(dir)
		if err != nil {
			return fmt.Errorf("resolve Go package %q: %w", importPath, err)
		}
		pkg := &CompileGoPackage{
			ImportPath: importPath,
			Dir:        dir,
			Files:      files,
			Imports:    imports,
			ImportedBy: uniqueSortedStrings(directImporters[importPath]),
			Direct:     len(directImporters[importPath]) > 0,
		}
		packages[importPath] = pkg

		for _, imported := range imports {
			if !isProjectGoImport(imported, modulePath) {
				continue
			}
			if err := visit(imported); err != nil {
				return err
			}
			if child := packages[imported]; child != nil {
				child.ImportedBy = append(child.ImportedBy, importPath)
				child.ImportedBy = uniqueSortedStrings(child.ImportedBy)
			}
		}
		return nil
	}

	direct := make([]string, 0, len(directImporters))
	for importPath := range directImporters {
		direct = append(direct, importPath)
	}
	sort.Strings(direct)
	for _, importPath := range direct {
		if err := visit(importPath); err != nil {
			return nil, err
		}
	}

	result := make([]CompileGoPackage, 0, len(packages))
	for _, pkg := range packages {
		result = append(result, *pkg)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ImportPath < result[j].ImportPath
	})
	return result, nil
}

func projectGoModulePath(root string) (string, error) {
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	modulePath := strings.TrimSpace(modfile.ModulePath(contents))
	if modulePath == "" {
		return "", fmt.Errorf("go.mod does not declare a module path")
	}
	return modulePath, nil
}

func isProjectGoImport(importPath, modulePath string) bool {
	return importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")
}

func projectGoPackageDir(root, modulePath, importPath string) (string, bool) {
	relative := strings.TrimPrefix(importPath, modulePath)
	relative = strings.TrimPrefix(relative, "/")
	dir := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel != "." {
		first := strings.Split(filepath.ToSlash(rel), "/")[0]
		switch first {
		case ".build", ".git", "node_modules", ".toolchain", "vendor":
			return "", false
		}
	}
	return dir, true
}

func readProjectGoPackage(dir string) ([]string, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	files := make([]string, 0)
	imports := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, filepath.Clean(path))
		for _, declaration := range parsed.Imports {
			importPath, err := strconv.Unquote(declaration.Path.Value)
			if err != nil {
				return nil, nil, err
			}
			imports = append(imports, importPath)
		}
	}
	return uniqueSortedStrings(files), uniqueSortedStrings(imports), nil
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
