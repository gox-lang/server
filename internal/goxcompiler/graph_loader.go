package gox

import (
	"fmt"
	"go/importer"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gox-lang/server/internal/clientframework"
	"github.com/gox-lang/server/internal/goxsource"
)

func LoadGraph(entryPath string) (*ModuleGraph, error) {
	rootDir, err := inferClientProjectRoot(entryPath)
	if err != nil {
		return nil, err
	}

	graph := &ModuleGraph{
		ByPath:  make(map[string]*Module),
		RootDir: rootDir,
	}

	// go/importer caches decoded export data and package identities inside one
	// Importer. Keep that universe compile-scoped: modules in this graph share
	// it, while the next LoadGraph starts fresh and cannot observe stale state.
	semanticImporter := importer.Default()

	entry, err := loadModuleRecursive(entryPath, graph, semanticImporter)
	if err != nil {
		return nil, err
	}

	graph.Entry = entry

	return graph, nil
}

func loadModuleRecursive(path string, graph *ModuleGraph, semanticImporter types.Importer) (*Module, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	if mod, ok := graph.ByPath[absPath]; ok {
		return mod, nil
	}

	loadStart := time.Now()

	srcBytes, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	src := string(srcBytes)

	relPath, relErr := filepath.Rel(graph.RootDir, absPath)
	structure := goxsource.ModuleContext{}
	if relErr == nil {
		structure = goxsource.ClassifyModule(filepath.ToSlash(relPath))
	}
	mod, err := parseModuleWithImporter(absPath, src, structure, semanticImporter)
	if err != nil {
		return nil, err
	}

	if err := resolveClientEntries(graph.RootDir, absPath, src, mod); err != nil {
		return nil, err
	}

	mod.LoadDuration = time.Since(loadStart)

	graph.ByPath[absPath] = mod

	baseDir := filepath.Dir(absPath)

	for i := range mod.GoxImports {
		imp := &mod.GoxImports[i]

		resolvedPath, candidates, err := resolveGoxImport(graph.RootDir, absPath, baseDir, imp.Path)
		if err != nil {
			return nil, err
		}

		imp.ResolvedPath = resolvedPath
		imp.ResolutionCandidates = candidates
		graph.ResolutionDependencies = appendUniquePaths(graph.ResolutionDependencies, candidates...)

		child, err := loadModuleRecursive(resolvedPath, graph, semanticImporter)
		if err != nil {
			return nil, err
		}

		if imp.Name == "" {
			imp.Name = child.Name
		}
	}
	graph.Modules = append(graph.Modules, mod)

	return mod, nil
}

func clientEntryDiagnosticOffset(src string, client *ClientDecl) int {
	start := int(client.Pos) - 1
	if start < 0 {
		start = 0
	}
	end := len(src)
	if closeIdx := strings.IndexByte(src[start:], '}'); closeIdx >= 0 {
		end = start + closeIdx
	}
	if start < end {
		body := src[start:end]
		if idx := strings.Index(body, "src"); idx >= 0 {
			abs := start + idx + len("src")
			for abs < end && (src[abs] == ' ' || src[abs] == '\t') {
				abs++
			}
			if abs < end && src[abs] == ':' {
				abs++
				for abs < end && (src[abs] == ' ' || src[abs] == '\t') {
					abs++
				}
				return abs
			}
		}
	}
	if client.EntryOffset > 0 {
		return client.EntryOffset
	}
	if client.EntryPos > 0 {
		return int(client.EntryPos) - 1
	}
	return start
}

func resolveGoxImport(rootDir, importerPath, baseDir, importPath string) (string, []string, error) {
	if importPath == "" {
		return "", nil, newCompilerErrorWithHint("empty-gox-import-path", "gox import path is empty", "Provide a .gox import path.")
	}
	if strings.HasPrefix(importPath, "@") {
		sourceRoot, err := goxsource.AliasRoot(rootDir, importerPath)
		if err != nil {
			return "", nil, newCompilerErrorWithHint("invalid-gox-alias-import", err.Error(), "Use @ imports from the pages or websocket source tree.")
		}
		resolution, err := goxsource.ResolveAlias(sourceRoot, importerPath, importPath)
		if err != nil {
			return "", resolution.Candidates, newCompilerErrorWithHint("invalid-gox-alias-import", err.Error(), "Use an explicit .gox alias visible from the importer or one of its ancestors.")
		}
		return resolution.ResolvedPath, resolution.Candidates, nil
	}

	var fullPath string

	if filepath.IsAbs(importPath) {
		fullPath = importPath
	} else {
		fullPath = filepath.Join(baseDir, importPath)
	}

	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", nil, err
	}

	if filepath.Ext(absPath) != ".gox" {
		return "", nil, newCompilerErrorWithHint("invalid-gox-import-path", fmt.Sprintf("invalid gox import %q: expected .gox file", importPath), "Import another .gox file.")
	}

	if _, err := os.Stat(absPath); err != nil {
		return "", nil, err
	}

	return absPath, nil, nil
}

// ResolveGoxImport resolves one .gox import using the same project and alias
// rules as graph compilation. Editor features use it instead of duplicating
// module resolution.
func ResolveGoxImport(rootDir, importerPath, importPath string) (string, error) {
	resolved, _, err := resolveGoxImport(rootDir, importerPath, filepath.Dir(importerPath), importPath)
	return resolved, err
}

func appendUniquePaths(paths []string, values ...string) []string {
	seen := make(map[string]struct{}, len(paths)+len(values))
	for _, value := range paths {
		seen[filepath.Clean(value)] = struct{}{}
	}
	for _, value := range values {
		value = filepath.Clean(value)
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		paths = append(paths, value)
	}
	return paths
}

func inferClientProjectRoot(entryPath string) (string, error) {
	absEntry, err := filepath.Abs(entryPath)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(absEntry)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Dir(absEntry), nil
		}
		dir = parent
	}
}

func resolveClientEntries(rootDir string, modulePath string, src string, mod *Module) error {
	if mod == nil || len(mod.Clients) == 0 {
		return nil
	}
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return err
	}
	clientRoot := filepath.Join(rootAbs, "client")
	for i := range mod.Clients {
		client := &mod.Clients[i]
		entry := strings.TrimSpace(client.Entry)
		framework := strings.ToLower(strings.TrimSpace(string(client.Framework)))
		pos := clientEntryDiagnosticOffset(src, client)
		if entry == "" {
			return compilerErrorAt(modulePath, src, pos, diagnosticMissingClientSrc, "client declaration requires src", "Add src: \"menu/menu.jsx\".")
		}
		if strings.Contains(entry, "\\") {
			return compilerErrorAt(modulePath, src, pos, diagnosticInvalidClientEntryPath, fmt.Sprintf("client entry %q must use slash-separated relative paths", client.Entry), "Use a path inside client/<framework>, such as src: \"menu/menu.jsx\".")
		}
		if filepath.IsAbs(entry) || strings.HasPrefix(entry, "/") {
			return compilerErrorAt(modulePath, src, pos, diagnosticInvalidClientEntryPath, "client src must be relative", "Use a path inside client/<framework>, such as src: \"menu/menu.jsx\".")
		}

		clean := path.Clean(entry)
		if clean == "." || clean == "" || clean == "/" {
			return compilerErrorAt(modulePath, src, pos, diagnosticInvalidClientEntryPath, fmt.Sprintf("invalid client entry %q", client.Entry), "Use a file path such as src: \"menu/menu.jsx\".")
		}
		if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
			return compilerErrorAt(modulePath, src, pos, diagnosticInvalidClientEntryPath, "client src cannot escape the framework directory", "Client entrypoints must stay inside the project client/<framework> directory.")
		}

		relInsideFramework := clean
		if strings.HasPrefix(relInsideFramework, "client/") {
			relInsideFramework = strings.TrimPrefix(relInsideFramework, "client/")
		}
		if framework != "" && strings.HasPrefix(relInsideFramework, framework+"/") {
			relInsideFramework = strings.TrimPrefix(relInsideFramework, framework+"/")
		}
		if relInsideFramework == framework || relInsideFramework == "client" || relInsideFramework == "." || relInsideFramework == "" || relInsideFramework == ".." || strings.HasPrefix(relInsideFramework, "../") || strings.Contains(relInsideFramework, "/../") {
			return compilerErrorAt(modulePath, src, pos, diagnosticInvalidClientEntryPath, fmt.Sprintf("invalid client entry %q", client.Entry), "Use a file path such as src: \"menu/menu.jsx\".")
		}

		frameworkRoot := filepath.Join(clientRoot, framework)
		absEntry := filepath.Join(frameworkRoot, filepath.FromSlash(relInsideFramework))
		absEntry, err = filepath.Abs(absEntry)
		if err != nil {
			return err
		}
		relToFrameworkRoot, err := filepath.Rel(frameworkRoot, absEntry)
		if err != nil {
			return err
		}
		if relToFrameworkRoot == ".." || strings.HasPrefix(relToFrameworkRoot, ".."+string(filepath.Separator)) {
			return compilerErrorAt(modulePath, src, pos, diagnosticInvalidClientEntryPath, "client src cannot escape the framework directory", "Client entrypoints must stay inside the project client/<framework> directory.")
		}

		clientRel := filepath.ToSlash(filepath.Join("client", framework, relToFrameworkRoot))
		info, err := os.Stat(absEntry)
		if err != nil {
			if os.IsNotExist(err) {
				return compilerErrorAt(modulePath, src, pos, diagnosticMissingClientEntry, "client source file does not exist", "Create the client source file under the project client/<framework> directory or update src.")
			}
			return compilerErrorAt(modulePath, src, pos, diagnosticClientSourceAccessError, fmt.Sprintf("cannot inspect client source file: %v", err), "Check that the client source path is accessible.")
		}
		if info.IsDir() {
			return compilerErrorAt(modulePath, src, pos, diagnosticInvalidClientEntryPath, "client source path is a directory", "Use a file path such as src: \"menu/menu.jsx\".")
		}
		if escapes, err := pathEscapesDirectory(frameworkRoot, absEntry); err != nil {
			return compilerErrorAt(modulePath, src, pos, diagnosticClientSourceAccessError, fmt.Sprintf("cannot resolve client source file: %v", err), "Check that the client source path and its symbolic links are accessible.")
		} else if escapes {
			return compilerErrorAt(modulePath, src, pos, diagnosticInvalidClientEntryPath, "client src cannot escape the framework directory", "Client entrypoints must stay inside the project client/<framework> directory.")
		}
		def, ok := clientframework.Lookup(framework)
		if !ok || !clientframework.SupportsExtension(framework, filepath.Ext(absEntry)) {
			return compilerErrorAt(modulePath, src, pos, diagnosticInvalidClientSourceExtension, frameworkExtensionMessage(framework, def.Extensions), "Use a source file with an extension supported by the selected framework.")
		}
		client.EntryPath = clientRel
		client.EntryAbsPath = absEntry
		client.Key = ClientDeclarationKey(modulePath, client.Name, strings.ToLower(string(client.Framework)), client.Entry, client.Dev)
	}
	return nil
}

func frameworkExtensionMessage(framework string, extensions []string) string {
	if len(extensions) == 1 {
		return fmt.Sprintf("framework %q requires a %s source file", framework, extensions[0])
	}
	return fmt.Sprintf("framework %q requires a %s source file", framework, strings.Join(extensions, " or "))
}

func pathEscapesDirectory(root string, target string) (bool, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, err
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil {
		return false, err
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}
