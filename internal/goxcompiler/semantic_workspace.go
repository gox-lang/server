package gox

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const semanticGoxPackagePrefix = "gox-file:"

// semanticWorkspace is the project-aware import environment used by semantic
// analysis. Each source analysis gets isolated GOX loading/dependency state,
// while Phase 14 may provide a shared cache for immutable Go package identities
// across document versions. Within one analysis Go and GOX imports still share
// one package identity and one resolution policy.
type semanticWorkspace struct {
	root        string
	moduleAware bool
	shared      *SemanticWorkspaceCache

	fallback        types.Importer
	goImporter      *semanticGoExportImporter
	cacheGeneration uint64

	goPackages  map[string]*types.Package
	goxPackages map[string]*types.Package
	goxLoading  map[string]bool

	dependencies     []SemanticDependency
	dependencySource map[string]bool
}

func newSemanticWorkspace(root string, shared *SemanticWorkspaceCache) *semanticWorkspace {
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	root = filepath.Clean(root)
	moduleAware := semanticWorkspaceHasModuleContext(root)
	result := &semanticWorkspace{
		root:             root,
		moduleAware:      moduleAware,
		shared:           shared,
		fallback:         importer.Default(),
		goPackages:       make(map[string]*types.Package),
		goxPackages:      make(map[string]*types.Package),
		goxLoading:       make(map[string]bool),
		dependencySource: make(map[string]bool),
	}
	if shared != nil {
		result.cacheGeneration, result.goImporter = shared.semanticWorkspaceLease(root, moduleAware)
	} else if moduleAware {
		result.goImporter = newSemanticGoExportImporter(root)
	}
	return result
}

func semanticWorkspaceHasModuleContext(root string) bool {
	for _, name := range []string{"go.mod", "go.work"} {
		if info, err := os.Stat(filepath.Join(root, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func (w *semanticWorkspace) importerFor(sourcePath string) types.Importer {
	return &semanticWorkspaceImporter{workspace: w, sourcePath: w.sourcePath(sourcePath)}
}

func (w *semanticWorkspace) sourcePath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(w.root, path))
}

func (w *semanticWorkspace) buildModel(sourcePath string, projection *SemanticProjection) *SemanticModel {
	if projection == nil {
		return nil
	}
	w.primeGoImports(projection.File)
	w.recordDependencies(w.sourcePath(sourcePath), projection.File)
	model := buildSemanticModelWithImporter(
		projection.File,
		projection.FileSet,
		projection.Origins,
		projection.SourceMap,
		"",
		w.importerFor(sourcePath),
	)
	if model != nil {
		model.Dependencies = w.semanticDependencies()
	}
	return model
}

func (w *semanticWorkspace) recordDependencies(sourcePath string, file *ast.File) {
	if w == nil || file == nil {
		return
	}
	sourcePath = w.sourcePath(sourcePath)
	if w.dependencySource[sourcePath] {
		return
	}
	w.dependencySource[sourcePath] = true
	w.dependencies = append(w.dependencies, w.dependenciesFor(sourcePath, file)...)
}

func (w *semanticWorkspace) semanticDependencies() []SemanticDependency {
	if w == nil || len(w.dependencies) == 0 {
		return nil
	}
	return append([]SemanticDependency(nil), w.dependencies...)
}

func (w *semanticWorkspace) dependenciesFor(sourcePath string, file *ast.File) []SemanticDependency {
	if file == nil {
		return nil
	}
	dependencies := make([]SemanticDependency, 0, len(file.Imports))
	for _, spec := range file.Imports {
		if spec == nil || spec.Path == nil {
			continue
		}
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath == "" || strings.HasSuffix(importPath, ".css") {
			continue
		}
		if semanticGoxImportPath(importPath) {
			resolved, candidates, resolveErr := resolveGoxImport(w.root, sourcePath, filepath.Dir(sourcePath), importPath)
			if resolveErr != nil && len(candidates) == 0 && !strings.HasPrefix(importPath, "@") {
				candidate := importPath
				if !filepath.IsAbs(candidate) {
					candidate = filepath.Join(filepath.Dir(sourcePath), candidate)
				}
				if absolute, absErr := filepath.Abs(candidate); absErr == nil {
					candidates = []string{filepath.Clean(absolute)}
				}
			}
			dependency := SemanticDependency{
				SourcePath:           sourcePath,
				Kind:                 SemanticDependencyGoxModule,
				ImportPath:           importPath,
				ResolutionCandidates: append([]string(nil), candidates...),
			}
			if resolveErr == nil {
				dependency.ResolvedPath = filepath.Clean(resolved)
				dependency.PackagePath = semanticGoxPackagePath(resolved)
			}
			dependencies = append(dependencies, dependency)
			continue
		}
		packagePath := importPath
		if pkg := w.goPackages[importPath]; pkg != nil && pkg.Path() != "" {
			packagePath = pkg.Path()
		}
		dependencies = append(dependencies, SemanticDependency{
			SourcePath:  sourcePath,
			Kind:        SemanticDependencyGoPackage,
			ImportPath:  importPath,
			PackagePath: packagePath,
		})
	}
	return dependencies
}

type semanticWorkspaceImporter struct {
	workspace  *semanticWorkspace
	sourcePath string
}

func (i *semanticWorkspaceImporter) Import(path string) (*types.Package, error) {
	return i.ImportFrom(path, "", 0)
}

func (i *semanticWorkspaceImporter) ImportFrom(path, _ string, _ types.ImportMode) (*types.Package, error) {
	if i == nil || i.workspace == nil {
		return nil, fmt.Errorf("semantic workspace importer is not configured")
	}
	if semanticGoxImportPath(path) {
		return i.workspace.loadGoxPackage(i.sourcePath, path)
	}
	return i.workspace.loadGoPackage(path)
}

func semanticGoxImportPath(importPath string) bool {
	return strings.HasSuffix(strings.TrimSpace(importPath), ".gox")
}

// primeGoImports batches the direct Go imports of one semantic projection.
// go/types asks its Importer for packages one at a time; without this warm-up a
// source file with N imports could trigger N module-graph resolutions. The
// shared export-data importer keeps one canonical go/types universe per root,
// while the local map only records the direct packages used by this analysis.
func (w *semanticWorkspace) primeGoImports(file *ast.File) {
	if w == nil || file == nil || !w.moduleAware || w.goImporter == nil {
		return
	}
	patterns := make([]string, 0, len(file.Imports))
	seen := make(map[string]bool, len(file.Imports))
	for _, spec := range file.Imports {
		if spec == nil || spec.Path == nil {
			continue
		}
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath == "" || importPath == "unsafe" || semanticGoxImportPath(importPath) || strings.HasSuffix(importPath, ".css") {
			continue
		}
		if w.goPackages[importPath] != nil || seen[importPath] {
			continue
		}
		if pkg, ok := w.shared.goPackageForGeneration(w.root, importPath, w.cacheGeneration); ok {
			w.goPackages[importPath] = pkg
			continue
		}
		seen[importPath] = true
		patterns = append(patterns, importPath)
	}
	if len(patterns) == 0 {
		return
	}

	loaded, err := w.goImporter.ImportMany(patterns)
	if err != nil {
		// Keep primeGoImports as an optimization only. The type checker will ask
		// loadGoPackage for each direct import and receive the concrete error there.
		return
	}
	for _, importPath := range patterns {
		pkg := loaded[importPath]
		if pkg == nil {
			continue
		}
		w.goPackages[importPath] = pkg
		w.shared.storeGoPackageForGeneration(w.root, importPath, pkg, w.cacheGeneration)
	}
}

func (w *semanticWorkspace) loadGoPackage(importPath string) (*types.Package, error) {
	if importPath == "unsafe" {
		return types.Unsafe, nil
	}
	if pkg := w.goPackages[importPath]; pkg != nil {
		return pkg, nil
	}
	if pkg, ok := w.shared.goPackageForGeneration(w.root, importPath, w.cacheGeneration); ok {
		w.goPackages[importPath] = pkg
		return pkg, nil
	}
	if !w.moduleAware {
		return w.loadFallbackPackage(importPath)
	}
	if w.goImporter == nil {
		return nil, fmt.Errorf("Go package %q could not be loaded: semantic module importer is not configured", importPath)
	}

	pkg, err := w.goImporter.Import(importPath)
	if err != nil {
		return nil, err
	}
	w.goPackages[importPath] = pkg
	w.shared.storeGoPackageForGeneration(w.root, importPath, pkg, w.cacheGeneration)
	return pkg, nil
}

func (w *semanticWorkspace) loadFallbackPackage(importPath string) (*types.Package, error) {
	if w.fallback == nil {
		return nil, fmt.Errorf("Go package %q could not be loaded without module context", importPath)
	}
	pkg, err := w.fallback.Import(importPath)
	if err != nil {
		return nil, err
	}
	if pkg != nil {
		w.goPackages[importPath] = pkg
		w.shared.storeGoPackageForGeneration(w.root, importPath, pkg, w.cacheGeneration)
	}
	return pkg, nil
}

func (w *semanticWorkspace) loadGoxPackage(importerPath, importPath string) (*types.Package, error) {
	resolved, err := ResolveGoxImport(w.root, importerPath, importPath)
	if err != nil {
		return nil, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, err
	}
	resolved = filepath.Clean(resolved)
	if pkg := w.goxPackages[resolved]; pkg != nil {
		return pkg, nil
	}
	if w.goxLoading[resolved] {
		return nil, fmt.Errorf("GOX import cycle while loading %q", resolved)
	}
	w.goxLoading[resolved] = true
	defer delete(w.goxLoading, resolved)

	source, err := os.ReadFile(resolved)
	if err != nil {
		return nil, err
	}
	projection, err := buildImportedSemanticProjectionForSource(resolved, string(source))
	if err != nil {
		return nil, err
	}
	if projection == nil || projection.File == nil || projection.File.Name == nil {
		return nil, fmt.Errorf("build semantic projection for GOX import %q", importPath)
	}

	w.primeGoImports(projection.File)
	w.recordDependencies(resolved, projection.File)

	// Imported GOX modules behave as package-like namespaces in source. Their
	// semantic projection uses the declared module name as the Go package name,
	// so an unaliased import is bound to the public GOX module namespace rather
	// than to the filename or the runtime-normalized module type name.
	model := buildSemanticModelWithImporter(
		projection.File,
		projection.FileSet,
		projection.Origins,
		projection.SourceMap,
		semanticGoxPackagePath(resolved),
		w.importerFor(resolved),
	)
	if model == nil || model.Package == nil {
		return nil, fmt.Errorf("type-check GOX import %q", importPath)
	}
	w.goxPackages[resolved] = model.Package
	return model.Package, nil
}

func semanticGoxPackagePath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return semanticGoxPackagePrefix + filepath.ToSlash(filepath.Clean(path))
}

// SemanticGoxPackageSource maps a synthetic go/types package path created for
// an imported .gox module back to its source file. Later navigation phases can
// use this without teaching go/types about runtime bundle rewrites.
func SemanticGoxPackageSource(packagePath string) (string, bool) {
	if !strings.HasPrefix(packagePath, semanticGoxPackagePrefix) {
		return "", false
	}
	path := strings.TrimPrefix(packagePath, semanticGoxPackagePrefix)
	if path == "" {
		return "", false
	}
	return filepath.Clean(filepath.FromSlash(path)), true
}
