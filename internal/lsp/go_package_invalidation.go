package lsp

import (
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// projectGoPackageInvalidation describes the local module package affected by
// one production .go file change plus every local package whose compiled API
// may depend on it transitively. The closure drives both package-index and GOX
// snapshot invalidation. The semantic go/types importer still rotates as one
// root-scoped universe because its canonical identities cannot be replaced
// piecemeal.
type projectGoPackageInvalidation struct {
	ImportPath string
	Affected   []string
	Candidate  *GoPackageCandidate
}

// projectGoImportGraph is the persistent local-module dependency graph used by
// package-index invalidation. It deliberately records only the information
// needed to patch one package after a .go event: package identity, its local
// imports, and reverse importers. The graph is root-scoped because go.mod/go.sum
// changes can alter module/import resolution and invalidate the whole model.
type projectGoImportGraph struct {
	mu         sync.RWMutex
	modulePath string
	packages   map[string]projectGoImportGraphPackage
	reverse    map[string]map[string]struct{}
}

type projectGoImportGraphPackage struct {
	ImportPath  string
	Directory   string
	PackageName string
	Imports     map[string]struct{}
}

// ProjectGoPackageInvalidationForFile incrementally updates the persistent
// local import graph for root and returns the changed package plus its reverse
// importer closure. The first call for a root builds the graph once; subsequent
// calls rescan only the changed package directory.
func (i *GoPackageIndex) ProjectGoPackageInvalidationForFile(root, filename string) (projectGoPackageInvalidation, bool) {
	if i == nil {
		return projectGoPackageInvalidation{}, false
	}
	root = filepath.Clean(root)
	modulePath := goModulePath(root)
	if modulePath == "" {
		return projectGoPackageInvalidation{}, false
	}
	importPath, ok := projectGoImportPathForDirectory(root, modulePath, filepath.Dir(filename))
	if !ok {
		return projectGoPackageInvalidation{}, false
	}

	graph, ok := i.projectGoImportGraph(root, modulePath, build.Default)
	if !ok {
		return projectGoPackageInvalidation{}, false
	}
	packageInfo, exists, valid := projectGoImportGraphPackageForDirectory(root, modulePath, filepath.Dir(filename), build.Default)
	if !valid {
		return projectGoPackageInvalidation{}, false
	}

	// Keep root metadata invalidation from detaching this graph between the
	// directory rescan and the incremental patch. The index read lock is held
	// only for the in-memory mutation/closure walk, never for filesystem I/O.
	i.mu.RLock()
	if i.projectGraphs[root] != graph {
		i.mu.RUnlock()
		return projectGoPackageInvalidation{}, false
	}
	graph.mu.Lock()
	graph.replacePackage(importPath, packageInfo, exists)
	affected := projectGoReverseClosure(importPath, graph.reverse)
	graph.mu.Unlock()
	i.mu.RUnlock()

	var candidate *GoPackageCandidate
	if exists && packageInfo.PackageName != "" && packageInfo.PackageName != "main" && packageInfo.PackageName != "_" && goPackageCandidateVisible(importPath) {
		value := GoPackageCandidate{Path: importPath, Name: packageInfo.PackageName}
		candidate = &value
	}
	return projectGoPackageInvalidation{ImportPath: importPath, Affected: affected, Candidate: candidate}, true
}

func (i *GoPackageIndex) projectGoImportGraph(root, modulePath string, context build.Context) (*projectGoImportGraph, bool) {
	root = filepath.Clean(root)
	i.mu.RLock()
	cached := i.projectGraphs[root]
	generation := i.projectGraphGeneration[root]
	epoch := i.projectGraphEpoch
	i.mu.RUnlock()
	if cached != nil && cached.modulePath == modulePath {
		return cached, true
	}

	built, ok := buildProjectGoImportGraph(root, modulePath, context)
	if !ok {
		return nil, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.projectGraphEpoch != epoch || i.projectGraphGeneration[root] != generation {
		// Module/workspace metadata changed while the filesystem graph was being
		// built. Never let that stale graph repopulate a newer cache generation.
		return nil, false
	}
	if current := i.projectGraphs[root]; current != nil && current.modulePath == modulePath {
		return current, true
	}
	i.projectGraphs[root] = built
	return built, true
}

func buildProjectGoImportGraph(root, modulePath string, context build.Context) (*projectGoImportGraph, bool) {
	graph := &projectGoImportGraph{
		modulePath: modulePath,
		packages:   make(map[string]projectGoImportGraphPackage),
		reverse:    make(map[string]map[string]struct{}),
	}
	walkOK := true
	_ = filepath.WalkDir(root, func(directory string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			walkOK = false
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if directory != root {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == ".toolchain" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
				return filepath.SkipDir
			}
		}
		packageInfo, exists, valid := projectGoImportGraphPackageForDirectory(root, modulePath, directory, context)
		if !valid {
			walkOK = false
			return nil
		}
		if exists {
			graph.packages[packageInfo.ImportPath] = packageInfo
		}
		return nil
	})
	if !walkOK {
		return nil, false
	}
	for importPath, packageInfo := range graph.packages {
		for imported := range packageInfo.Imports {
			importers := graph.reverse[imported]
			if importers == nil {
				importers = make(map[string]struct{})
				graph.reverse[imported] = importers
			}
			importers[importPath] = struct{}{}
		}
	}
	return graph, true
}

func (g *projectGoImportGraph) replacePackage(importPath string, packageInfo projectGoImportGraphPackage, exists bool) {
	if old, ok := g.packages[importPath]; ok {
		for imported := range old.Imports {
			importers := g.reverse[imported]
			delete(importers, importPath)
			if len(importers) == 0 {
				delete(g.reverse, imported)
			}
		}
	}
	if !exists {
		delete(g.packages, importPath)
		return
	}
	g.packages[importPath] = packageInfo
	for imported := range packageInfo.Imports {
		importers := g.reverse[imported]
		if importers == nil {
			importers = make(map[string]struct{})
			g.reverse[imported] = importers
		}
		importers[importPath] = struct{}{}
	}
}

// projectGoImportGraphPackageForDirectory returns (package, exists, valid).
// exists=false is a valid state for a directory that disappeared or no longer
// contains active non-test Go files. valid=false means the local graph cannot be
// trusted and callers must fall back to root invalidation.
func projectGoImportGraphPackageForDirectory(root, modulePath, directory string, context build.Context) (projectGoImportGraphPackage, bool, bool) {
	importPath, ok := projectGoImportPathForDirectory(root, modulePath, directory)
	if !ok {
		return projectGoImportGraphPackage{}, false, false
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return projectGoImportGraphPackage{}, false, true
		}
		return projectGoImportGraphPackage{}, false, false
	}
	packageName := ""
	imports := make(map[string]struct{})
	for _, file := range entries {
		name := file.Name()
		lower := strings.ToLower(name)
		if file.IsDir() || !strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, "_test.go") {
			continue
		}
		matches, matchErr := context.MatchFile(directory, name)
		if matchErr != nil {
			return projectGoImportGraphPackage{}, false, false
		}
		if !matches {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, parser.ImportsOnly)
		if parseErr != nil || parsed.Name == nil || parsed.Name.Name == "" {
			return projectGoImportGraphPackage{}, false, false
		}
		if packageName == "" {
			packageName = parsed.Name.Name
		} else if packageName != parsed.Name.Name {
			return projectGoImportGraphPackage{}, false, false
		}
		for _, declaration := range parsed.Imports {
			if declaration == nil || declaration.Path == nil {
				continue
			}
			imported, unquoteErr := strconv.Unquote(declaration.Path.Value)
			if unquoteErr != nil {
				return projectGoImportGraphPackage{}, false, false
			}
			if goImportPathInModule(imported, modulePath) {
				imports[imported] = struct{}{}
			}
		}
	}
	if packageName == "" {
		return projectGoImportGraphPackage{}, false, true
	}
	return projectGoImportGraphPackage{
		ImportPath:  importPath,
		Directory:   filepath.Clean(directory),
		PackageName: packageName,
		Imports:     imports,
	}, true, true
}

func projectGoImportPathForDirectory(root, modulePath, directory string) (string, bool) {
	root = filepath.Clean(root)
	directory = filepath.Clean(directory)
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	if relative == "." {
		return modulePath, true
	}
	first := strings.Split(filepath.ToSlash(relative), "/")[0]
	if strings.HasPrefix(first, ".") || first == "node_modules" || first == "vendor" || first == ".toolchain" {
		return "", false
	}
	return strings.TrimSuffix(modulePath, "/") + "/" + filepath.ToSlash(relative), true
}

func projectGoReverseClosure(importPath string, reverse map[string]map[string]struct{}) []string {
	seen := map[string]struct{}{importPath: {}}
	queue := []string{importPath}
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		for importer := range reverse[current] {
			if _, ok := seen[importer]; ok {
				continue
			}
			seen[importer] = struct{}{}
			queue = append(queue, importer)
		}
	}
	result := make([]string, 0, len(seen))
	for path := range seen {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}
