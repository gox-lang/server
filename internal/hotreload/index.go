package hotreload

import (
	"path/filepath"
	"strings"
	"sync"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type ReloadKind string

const (
	ReloadConfig ReloadKind = "config"
	ReloadPage   ReloadKind = "page"
	ReloadError  ReloadKind = "error"
	ReloadClient ReloadKind = "client"
)

type ReloadEntry struct {
	Kind         ReloadKind
	EntryPath    string
	EntryAbsPath string
	OutPath      string
	OutAbsPath   string
}

type FileDependents struct {
	FileAbsPath string
	Entries     []ReloadEntry
}

type Index struct {
	mu sync.RWMutex

	byFile       map[string]FileDependents
	filesByEntry map[string][]string
}

func NewIndex() *Index {
	return &Index{
		byFile:       make(map[string]FileDependents),
		filesByEntry: make(map[string][]string),
	}
}

func (idx *Index) RegisterClientBundle(bundle goxcompiler.CompileClientBundle) {
	if idx == nil {
		return
	}

	entryAbsPath := cleanAbs(bundle.Graph.EntryFile)
	if entryAbsPath == "" {
		return
	}

	entry := ReloadEntry{
		Kind:         ReloadClient,
		EntryPath:    bundle.Path,
		EntryAbsPath: entryAbsPath,
		OutPath:      bundle.EntryURL,
		OutAbsPath:   cleanAbs(bundle.EntryFile),
	}

	files := clientBundleDependencyFiles(bundle)
	if len(files) == 0 {
		return
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.removeEntryLocked(entry.EntryAbsPath)
	idx.registerEntryFilesLocked(entry, files)
}

func (idx *Index) RegisterClientBundles(bundles []goxcompiler.CompileClientBundle) {
	for _, bundle := range bundles {
		idx.RegisterClientBundle(bundle)
	}
}

func (idx *Index) RegisterCompileResult(kind ReloadKind, result *goxcompiler.CompileResult) {
	if idx == nil || result == nil {
		return
	}

	entry := ReloadEntry{
		Kind:         kind,
		EntryPath:    result.EntryPath,
		EntryAbsPath: cleanAbs(result.EntryAbsPath),
		OutPath:      result.OutPath,
		OutAbsPath:   cleanAbs(result.OutAbsPath),
	}

	if entry.EntryAbsPath == "" {
		return
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.removeEntryLocked(entry.EntryAbsPath)

	files := make([]string, 0, len(result.Dependencies)+len(result.GoPackages))
	for _, dep := range result.Dependencies {
		if fileAbsPath := cleanAbs(dep.AbsPath); fileAbsPath != "" {
			files = append(files, fileAbsPath)
		}
	}
	for _, candidate := range result.ResolutionDependencies {
		if fileAbsPath := cleanAbs(candidate); fileAbsPath != "" {
			files = append(files, fileAbsPath)
		}
	}
	for _, pkg := range result.GoPackages {
		if dir := cleanAbs(pkg.Dir); dir != "" {
			files = append(files, dir)
		}
		for _, file := range pkg.Files {
			if fileAbsPath := cleanAbs(file); fileAbsPath != "" {
				files = append(files, fileAbsPath)
			}
		}
	}

	idx.registerEntryFilesLocked(entry, files)
}

func (idx *Index) registerEntryFilesLocked(entry ReloadEntry, files []string) {
	seenFiles := make(map[string]struct{}, len(files))
	registered := make([]string, 0, len(files))

	for _, fileAbsPath := range files {
		fileAbsPath = cleanAbs(fileAbsPath)
		if fileAbsPath == "" {
			continue
		}
		if _, ok := seenFiles[fileAbsPath]; ok {
			continue
		}

		seenFiles[fileAbsPath] = struct{}{}
		registered = append(registered, fileAbsPath)

		dependents := idx.byFile[fileAbsPath]
		dependents.FileAbsPath = fileAbsPath
		dependents.Entries = appendUniqueEntry(dependents.Entries, entry)
		idx.byFile[fileAbsPath] = dependents
	}

	idx.filesByEntry[entry.EntryAbsPath] = registered
}

func (idx *Index) Dependents(filePath string) (FileDependents, bool) {
	if idx == nil {
		return FileDependents{}, false
	}

	fileAbsPath := cleanAbs(filePath)
	if fileAbsPath == "" {
		return FileDependents{}, false
	}

	idx.mu.RLock()
	entries := make([]ReloadEntry, 0)
	if dependents, ok := idx.byFile[fileAbsPath]; ok {
		entries = append(entries, dependents.Entries...)
	}
	if dependents, ok := idx.byFile[filepath.Dir(fileAbsPath)]; ok {
		for _, entry := range dependents.Entries {
			entries = appendUniqueEntry(entries, entry)
		}
	}
	idx.mu.RUnlock()

	if len(entries) == 0 {
		return FileDependents{}, false
	}

	out := FileDependents{
		FileAbsPath: fileAbsPath,
		Entries:     entries,
	}

	return out, true
}

func (idx *Index) RemoveEntry(entryAbsPath string) {
	if idx == nil {
		return
	}

	entryAbsPath = cleanAbs(entryAbsPath)
	if entryAbsPath == "" {
		return
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.removeEntryLocked(entryAbsPath)
}

func (idx *Index) removeEntryLocked(entryAbsPath string) {
	files := idx.filesByEntry[entryAbsPath]
	if len(files) == 0 {
		return
	}

	for _, fileAbsPath := range files {
		dependents := idx.byFile[fileAbsPath]
		if len(dependents.Entries) == 0 {
			continue
		}

		next := dependents.Entries[:0]
		for _, entry := range dependents.Entries {
			if entry.EntryAbsPath != entryAbsPath {
				next = append(next, entry)
			}
		}

		if len(next) == 0 {
			delete(idx.byFile, fileAbsPath)
			continue
		}

		dependents.Entries = next
		idx.byFile[fileAbsPath] = dependents
	}

	delete(idx.filesByEntry, entryAbsPath)
}

func appendUniqueEntry(entries []ReloadEntry, entry ReloadEntry) []ReloadEntry {
	for _, existing := range entries {
		if existing.Kind == entry.Kind && existing.EntryAbsPath == entry.EntryAbsPath {
			return entries
		}
	}

	return append(entries, entry)
}

func cleanAbs(path string) string {
	if path == "" {
		return ""
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}

	return filepath.Clean(abs)
}

func clientBundleDependencyFiles(bundle goxcompiler.CompileClientBundle) []string {
	files := make([]string, 0, len(bundle.Graph.ModuleFiles)+len(bundle.Graph.Imports)+len(bundle.Graph.DynamicImports)+3)
	seen := map[string]struct{}{}
	add := func(file string) {
		file = normalizeClientDependencyPath(file)
		if file == "" || isClientVendorFile(file) || isClientBuildOutputFile(file) {
			return
		}
		if _, ok := seen[file]; ok {
			return
		}
		seen[file] = struct{}{}
		files = append(files, file)
	}

	add(bundle.Graph.EntryFile)
	for _, file := range bundle.Graph.ModuleFiles {
		add(file)
	}
	for _, file := range bundle.Graph.Imports {
		add(file)
	}
	for _, file := range bundle.Graph.DynamicImports {
		add(file)
	}
	for _, node := range bundle.Graph.Nodes {
		add(node.File)
		for _, file := range node.Imports {
			add(file)
		}
		for _, file := range node.DynamicImports {
			add(file)
		}
	}
	if root := clientFrameworkRoot(bundle); root != "" {
		add(filepath.Join(root, "package.json"))
		add(filepath.Join(root, "pnpm-lock.yaml"))
		add(filepath.Join(root, "pnpm-workspace.yaml"))
	}
	return files
}

func normalizeClientDependencyPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "\x00") {
		return ""
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	return cleanAbs(path)
}

func isClientVendorFile(path string) bool {
	path = filepath.ToSlash(path)
	return strings.Contains(path, "/node_modules/") || strings.Contains(path, "/.pnpm/")
}

func isClientBuildOutputFile(path string) bool {
	path = filepath.ToSlash(path)
	return strings.Contains(path, "/.build/client/") || strings.HasPrefix(path, ".build/client/")
}

func clientFrameworkRoot(bundle goxcompiler.CompileClientBundle) string {
	entry := cleanAbs(bundle.Graph.EntryFile)
	framework := strings.ToLower(strings.TrimSpace(bundle.Framework))
	if entry == "" || framework == "" {
		return ""
	}
	entrySlash := filepath.ToSlash(entry)
	marker := "/client/" + framework + "/"
	idx := strings.Index(strings.ToLower(entrySlash), marker)
	if idx < 0 {
		return ""
	}
	rootSlash := entrySlash[:idx+len(marker)-1]
	return filepath.FromSlash(rootSlash)
}
