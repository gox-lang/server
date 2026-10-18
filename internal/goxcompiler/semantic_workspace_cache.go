package gox

import (
	"go/types"
	"path/filepath"
	"sync"
)

// SemanticWorkspaceCache owns immutable Go package identities that are safe to
// reuse across semantic analyses of different GOX document versions. GOX
// modules themselves are intentionally not cached here: their source may be
// edited independently and is tracked by the LSP dependency graph.
//
// Each semantic workspace also receives a generation token for its root. Root
// invalidation advances that generation by replacing the active lease. Analyses
// that were already in flight may finish against their old importer universe,
// but generation-aware cache reads/stores prevent them from mixing old
// *types.Package identities into the new universe.
type SemanticWorkspaceCache struct {
	mu sync.RWMutex

	goPackages  map[string]map[string]*types.Package
	goImporters map[string]*semanticGoExportImporter
	generations map[string]uint64
	nextGen     uint64
}

func NewSemanticWorkspaceCache() *SemanticWorkspaceCache {
	return &SemanticWorkspaceCache{
		goPackages:  make(map[string]map[string]*types.Package),
		goImporters: make(map[string]*semanticGoExportImporter),
		generations: make(map[string]uint64),
	}
}

func (c *SemanticWorkspaceCache) GoPackage(root, importPath string) (*types.Package, bool) {
	if c == nil || importPath == "" {
		return nil, false
	}
	root = semanticCacheRoot(root)
	c.mu.RLock()
	pkg := c.goPackages[root][importPath]
	c.mu.RUnlock()
	return pkg, pkg != nil
}

func (c *SemanticWorkspaceCache) StoreGoPackage(root, importPath string, pkg *types.Package) {
	if c == nil || importPath == "" || pkg == nil {
		return
	}
	root = semanticCacheRoot(root)
	c.mu.Lock()
	c.ensureGenerationLocked(root)
	packages := c.goPackages[root]
	if packages == nil {
		packages = make(map[string]*types.Package)
		c.goPackages[root] = packages
	}
	packages[importPath] = pkg
	c.mu.Unlock()
}

// semanticWorkspaceLease returns the currently active cache generation for a
// root and, when requested, the canonical export-data importer owned by that
// generation. Both values are captured atomically so an invalidation cannot
// pair an old generation with a new importer (or vice versa).
func (c *SemanticWorkspaceCache) semanticWorkspaceLease(root string, needImporter bool) (uint64, *semanticGoExportImporter) {
	if c == nil {
		return 0, nil
	}
	root = semanticCacheRoot(root)
	c.mu.Lock()
	defer c.mu.Unlock()

	generation := c.ensureGenerationLocked(root)
	if !needImporter {
		return generation, nil
	}
	if c.goImporters == nil {
		c.goImporters = make(map[string]*semanticGoExportImporter)
	}
	result := c.goImporters[root]
	if result == nil {
		result = newSemanticGoExportImporter(root)
		c.goImporters[root] = result
	}
	return generation, result
}

func (c *SemanticWorkspaceCache) ensureGenerationLocked(root string) uint64 {
	if c.generations == nil {
		c.generations = make(map[string]uint64)
	}
	if generation := c.generations[root]; generation != 0 {
		return generation
	}
	c.nextGen++
	if c.nextGen == 0 {
		// uint64 wrap is practically unreachable, but zero is reserved as the
		// "no shared cache" generation used by semanticWorkspace.
		c.nextGen++
	}
	c.generations[root] = c.nextGen
	return c.nextGen
}

func (c *SemanticWorkspaceCache) goPackageForGeneration(root, importPath string, generation uint64) (*types.Package, bool) {
	if c == nil || importPath == "" || generation == 0 {
		return nil, false
	}
	root = semanticCacheRoot(root)
	c.mu.RLock()
	if c.generations[root] != generation {
		c.mu.RUnlock()
		return nil, false
	}
	pkg := c.goPackages[root][importPath]
	c.mu.RUnlock()
	return pkg, pkg != nil
}

func (c *SemanticWorkspaceCache) storeGoPackageForGeneration(root, importPath string, pkg *types.Package, generation uint64) bool {
	if c == nil || importPath == "" || pkg == nil || generation == 0 {
		return false
	}
	root = semanticCacheRoot(root)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generations[root] != generation {
		return false
	}
	packages := c.goPackages[root]
	if packages == nil {
		packages = make(map[string]*types.Package)
		c.goPackages[root] = packages
	}
	packages[importPath] = pkg
	return true
}

func (c *SemanticWorkspaceCache) InvalidateRoot(root string) {
	if c == nil {
		return
	}
	root = semanticCacheRoot(root)
	c.mu.Lock()
	delete(c.goPackages, root)
	delete(c.goImporters, root)
	delete(c.generations, root)
	c.mu.Unlock()
}

func (c *SemanticWorkspaceCache) InvalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.goPackages = make(map[string]map[string]*types.Package)
	c.goImporters = make(map[string]*semanticGoExportImporter)
	c.generations = make(map[string]uint64)
	// nextGen intentionally remains monotonic. Reusing a generation after
	// InvalidateAll would allow an old in-flight workspace to appear current.
	c.mu.Unlock()
}

func semanticCacheRoot(root string) string {
	if root == "" {
		return ""
	}
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	return filepath.Clean(root)
}
