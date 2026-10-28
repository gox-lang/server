package gox

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gox-lang/server/internal/goroutinesafety"
)

var ignoredProjectGoDirs = map[string]struct{}{
	".build":       {},
	".git":         {},
	".toolchain":   {},
	"node_modules": {},
	"vendor":       {},
}

const projectGoSafetyCacheMaxRoots = 32

type projectGoSafetyFileState struct {
	path    string
	size    int64
	modTime time.Time
}

type projectGoSafetyDirState struct {
	files []projectGoSafetyFileState
	err   error
}

type projectGoSafetyRootState struct {
	dirs       map[string]projectGoSafetyDirState
	generation uint64
	lastUsed   uint64
}

var projectGoSafetyCache = struct {
	sync.Mutex
	roots map[string]*projectGoSafetyRootState
	clock uint64
}{roots: make(map[string]*projectGoSafetyRootState)}

// ValidateProjectGoGoroutineSafety checks project Go sources without building or
// loading a plugin. Hot reload uses the same gate before stopping a healthy
// runtime, so a rejected edit cannot turn a restart into a startup failure.
func ValidateProjectGoGoroutineSafety(root string) error {
	return validateProjectGoGoroutineSafety(root)
}

func validateProjectGoGoroutineSafety(root string) error {
	root = normalizeProjectGoSafetyRoot(root)
	if root == "" {
		return nil
	}

	filesByDir, err := scanProjectGoSafetyFiles(root)
	if err != nil {
		return err
	}

	lease, generation, cached := projectGoSafetyRootSnapshot(root)
	dirs := make([]string, 0, len(filesByDir))
	for dir := range filesByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	next := make(map[string]projectGoSafetyDirState, len(dirs))
	for _, dir := range dirs {
		files := filesByDir[dir]
		if previous, ok := cached[dir]; ok && sameProjectGoSafetyFiles(previous.files, files) {
			next[dir] = previous
			if previous.err != nil {
				projectGoSafetyStoreRoot(root, lease, generation, next)
				return previous.err
			}
			continue
		}

		dirErr := analyzeProjectGoSafetyDir(root, files)
		state := projectGoSafetyDirState{files: cloneProjectGoSafetyFiles(files), err: dirErr}
		next[dir] = state
		if dirErr != nil {
			projectGoSafetyStoreRoot(root, lease, generation, next)
			return dirErr
		}
	}

	projectGoSafetyStoreRoot(root, lease, generation, next)
	return nil
}

// InvalidateProjectGoGoroutineSafety evicts cached safety analysis for the
// directory containing changedPath. Passing an empty path evicts the whole
// project root. Compile and Analyze also verify filesystem metadata, so this
// invalidation is an eager hot-reload optimization rather than a correctness
// requirement for callers outside the server runtime.
func InvalidateProjectGoGoroutineSafety(root, changedPath string) {
	root = normalizeProjectGoSafetyRoot(root)
	if root == "" {
		return
	}

	projectGoSafetyCache.Lock()
	defer projectGoSafetyCache.Unlock()
	state := projectGoSafetyCache.roots[root]
	if state == nil {
		projectGoSafetyCache.clock++
		state = &projectGoSafetyRootState{dirs: make(map[string]projectGoSafetyDirState), lastUsed: projectGoSafetyCache.clock}
		projectGoSafetyCache.roots[root] = state
	}
	state.generation++
	projectGoSafetyCache.clock++
	state.lastUsed = projectGoSafetyCache.clock
	if strings.TrimSpace(changedPath) == "" {
		state.dirs = make(map[string]projectGoSafetyDirState)
		projectGoSafetyEvictRootsLocked(root)
		return
	}

	path := filepath.Clean(changedPath)
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	changedDir := filepath.Dir(path)
	next := make(map[string]projectGoSafetyDirState, len(state.dirs))
	for dir, cached := range state.dirs {
		if dir != changedDir {
			next[dir] = cached
		}
	}
	state.dirs = next
	projectGoSafetyEvictRootsLocked(root)
}

func normalizeProjectGoSafetyRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	return filepath.Clean(root)
}

func scanProjectGoSafetyFiles(root string) (map[string][]projectGoSafetyFileState, error) {
	filesByDir := make(map[string][]projectGoSafetyFileState)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root {
				if _, ignored := ignoredProjectGoDirs[entry.Name()]; ignored {
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		filesByDir[dir] = append(filesByDir[dir], projectGoSafetyFileState{
			path:    path,
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan project Go files: %w", err)
	}
	for dir := range filesByDir {
		sort.Slice(filesByDir[dir], func(i, j int) bool {
			return filesByDir[dir][i].path < filesByDir[dir][j].path
		})
	}
	return filesByDir, nil
}

func analyzeProjectGoSafetyDir(root string, files []projectGoSafetyFileState) error {
	fset := token.NewFileSet()
	filesByPackage := make(map[string][]*ast.File)
	for _, state := range files {
		source, err := os.ReadFile(state.path)
		if err != nil {
			return fmt.Errorf("read project Go file %q: %w", state.path, err)
		}
		file, err := parser.ParseFile(fset, state.path, source, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse project Go file %q: %w", state.path, err)
		}
		filesByPackage[file.Name.Name] = append(filesByPackage[file.Name.Name], file)
	}

	names := make([]string, 0, len(filesByPackage))
	for name := range filesByPackage {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		diagnostics := goroutinesafety.AnalyzeFiles(fset, filesByPackage[name])
		if len(diagnostics) == 0 {
			continue
		}
		diagnostic := diagnostics[0]
		err := newCompilerErrorWithHint(
			diagnosticUnsafeGoroutine,
			"goroutine has no panic recovery and may terminate GOX Server",
			"Register a deferred recover barrier before any other statement in the goroutine entry function.",
		)
		if diagnostic.Pos.IsValid() {
			position := fset.PositionFor(diagnostic.Pos, false)
			err.File = normalizeProjectGoErrorPath(root, position.Filename)
			err.Line = position.Line
			err.Column = position.Column
		}
		return err
	}
	return nil
}

func sameProjectGoSafetyFiles(a, b []projectGoSafetyFileState) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].path != b[i].path || a[i].size != b[i].size || !a[i].modTime.Equal(b[i].modTime) {
			return false
		}
	}
	return true
}

func cloneProjectGoSafetyFiles(files []projectGoSafetyFileState) []projectGoSafetyFileState {
	return append([]projectGoSafetyFileState(nil), files...)
}

func projectGoSafetyRootSnapshot(root string) (*projectGoSafetyRootState, uint64, map[string]projectGoSafetyDirState) {
	projectGoSafetyCache.Lock()
	defer projectGoSafetyCache.Unlock()
	state := projectGoSafetyCache.roots[root]
	if state == nil {
		projectGoSafetyCache.clock++
		state = &projectGoSafetyRootState{dirs: make(map[string]projectGoSafetyDirState), lastUsed: projectGoSafetyCache.clock}
		projectGoSafetyCache.roots[root] = state
		projectGoSafetyEvictRootsLocked(root)
	} else {
		projectGoSafetyCache.clock++
		state.lastUsed = projectGoSafetyCache.clock
	}
	return state, state.generation, state.dirs
}

func projectGoSafetyStoreRoot(root string, lease *projectGoSafetyRootState, generation uint64, dirs map[string]projectGoSafetyDirState) {
	projectGoSafetyCache.Lock()
	defer projectGoSafetyCache.Unlock()
	state := projectGoSafetyCache.roots[root]
	if state != lease || state == nil || state.generation != generation {
		return
	}
	state.dirs = dirs
	projectGoSafetyCache.clock++
	state.lastUsed = projectGoSafetyCache.clock
	projectGoSafetyEvictRootsLocked(root)
}

func projectGoSafetyEvictRootsLocked(preserveRoot string) {
	for len(projectGoSafetyCache.roots) > projectGoSafetyCacheMaxRoots {
		var oldestRoot string
		var oldest uint64
		for candidate, state := range projectGoSafetyCache.roots {
			if candidate == preserveRoot {
				continue
			}
			if oldestRoot == "" || state.lastUsed < oldest {
				oldestRoot = candidate
				oldest = state.lastUsed
			}
		}
		if oldestRoot == "" {
			return
		}
		delete(projectGoSafetyCache.roots, oldestRoot)
	}
}

func normalizeProjectGoErrorPath(root, path string) string {
	if path == "" {
		return ""
	}
	relative, err := filepath.Rel(root, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(path)
}
