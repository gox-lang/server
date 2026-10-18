package gox

import (
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/tools/go/packages"
)

// semanticGoExportImporter owns one canonical go/types universe for a module
// root. go/packages is used only to resolve the module-aware import graph and
// compiled ExportFile paths; go/importer decodes those artifacts lazily into
// *types.Package values as semantic analysis actually requests them.
//
// The importer itself keeps an internal package-identity cache. Calls must stay
// serialized because go/importer's package map is not concurrency-safe.
type semanticGoExportImporter struct {
	root string

	mu          sync.Mutex
	exportFiles map[string]string
	importer    types.Importer
}

func newSemanticGoExportImporter(root string) *semanticGoExportImporter {
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	root = filepath.Clean(root)
	result := &semanticGoExportImporter{
		root:        root,
		exportFiles: make(map[string]string),
	}
	result.importer = importer.ForCompiler(token.NewFileSet(), "gc", result.lookup)
	return result
}

func (i *semanticGoExportImporter) Import(path string) (*types.Package, error) {
	loaded, err := i.ImportMany([]string{path})
	if err != nil {
		return nil, err
	}
	pkg := loaded[path]
	if pkg == nil {
		return nil, fmt.Errorf("Go package %q could not be imported from workspace %q", path, i.root)
	}
	return pkg, nil
}

func (i *semanticGoExportImporter) ImportMany(paths []string) (map[string]*types.Package, error) {
	if i == nil {
		return nil, fmt.Errorf("semantic Go export importer is not configured")
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	patterns := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" || path == "unsafe" || seen[path] {
			continue
		}
		seen[path] = true
		if i.exportFiles[path] == "" {
			patterns = append(patterns, path)
		}
	}
	if len(patterns) != 0 {
		if err := i.loadExportGraphLocked(patterns); err != nil {
			return nil, err
		}
	}

	result := make(map[string]*types.Package, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		pkg, err := i.importer.Import(path)
		if err != nil {
			return nil, err
		}
		if pkg == nil || !pkg.Complete() {
			return nil, fmt.Errorf("Go package %q imported incomplete from workspace %q", path, i.root)
		}
		result[path] = pkg
	}
	return result, nil
}

func (i *semanticGoExportImporter) loadExportGraphLocked(patterns []string) error {
	loaded, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps | packages.NeedExportFile,
		Dir:  i.root,
	}, patterns...)
	if err != nil {
		return err
	}

	seen := make(map[*packages.Package]bool)
	var visit func(*packages.Package) error
	visit = func(pkg *packages.Package) error {
		if pkg == nil || seen[pkg] {
			return nil
		}
		seen[pkg] = true
		if pkg.PkgPath != "" && pkg.PkgPath != "unsafe" {
			if len(pkg.Errors) != 0 {
				return fmt.Errorf("load Go package %q: %s", pkg.PkgPath, pkg.Errors[0].Msg)
			}
			if pkg.ExportFile == "" {
				return fmt.Errorf("Go package %q has no compiled export data", pkg.PkgPath)
			}
			i.exportFiles[pkg.PkgPath] = pkg.ExportFile
		}
		for _, dependency := range pkg.Imports {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	for _, pkg := range loaded {
		if err := visit(pkg); err != nil {
			return err
		}
	}
	for _, pattern := range patterns {
		if pattern != "unsafe" && i.exportFiles[pattern] == "" {
			return fmt.Errorf("Go package %q has no compiled export data in workspace %q", pattern, i.root)
		}
	}
	return nil
}

// lookup is called only by i.importer while i.mu is held by Import/ImportMany.
// Keeping it lock-free avoids recursive locking when the gc importer follows
// transitive references in export data.
func (i *semanticGoExportImporter) lookup(path string) (io.ReadCloser, error) {
	filename := i.exportFiles[path]
	if filename == "" {
		return nil, fmt.Errorf("export file for Go package %q is not loaded", path)
	}
	return os.Open(filename)
}
