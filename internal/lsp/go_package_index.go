package lsp

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

type GoPackageExport struct {
	Name       string
	Detail     string
	Kind       string
	Definition GoDefinition
	Members    []GoPackageMember
	Type       string
	Underlying string
	Alias      bool
	Value      string
	Results    []string
	Parameters []goxcompiler.SourceParameter
}

type GoPackageMember struct {
	Name       string
	Detail     string
	Kind       string
	Definition GoDefinition
	Type       string
	Underlying string
	Alias      bool
	Results    []string
	Parameters []goxcompiler.SourceParameter
}

type GoPackage struct {
	Path       string
	Name       string
	Definition GoDefinition
	Exports    []GoPackageExport
}

type GoPackageCandidate struct {
	// Path is the canonical Go import path. Name is the identifier declared by
	// the package clause, and is intentionally independent from the last path
	// segment. Consumers must use Name for aliases and code completions.
	Path string
	Name string
}

type GoDefinition struct {
	Path   string
	Offset int
	Length int
}

type goPackageCacheEntry struct {
	packageInfo GoPackage
	files       map[string]goPackageFileStamp
}

type goPackageFileStamp struct {
	size    int64
	modTime int64
}

type goGlobalPackageSource struct {
	Name             string
	Files            []string
	PreserveInternal bool
}

type GoPackageIndex struct {
	mu                     sync.RWMutex
	cache                  map[string]goPackageCacheEntry
	packageNames           map[string]string
	discovery              map[string][]GoPackageCandidate
	exports                map[string][]GoPackage
	exportSources          map[string]map[string]goGlobalPackageSource
	exportDetails          map[string]GoPackage
	projectGraphs          map[string]*projectGoImportGraph
	projectGraphGeneration map[string]uint64
	projectGraphEpoch      uint64
}

const goxInternalAPIMarker = "gox:internal"

const goGlobalExportFileMode packages.LoadMode = packages.NeedName | packages.NeedFiles

func NewGoPackageIndex() *GoPackageIndex {
	return &GoPackageIndex{
		cache:                  make(map[string]goPackageCacheEntry),
		packageNames:           make(map[string]string),
		discovery:              make(map[string][]GoPackageCandidate),
		exports:                make(map[string][]GoPackage),
		exportSources:          make(map[string]map[string]goGlobalPackageSource),
		exportDetails:          make(map[string]GoPackage),
		projectGraphs:          make(map[string]*projectGoImportGraph),
		projectGraphGeneration: make(map[string]uint64),
	}
}

func (i *GoPackageIndex) Discover(root string) ([]GoPackageCandidate, error) {
	i.mu.RLock()
	cached, ok := i.discovery[root]
	i.mu.RUnlock()
	if ok {
		// Workspace file changes invalidate this root through the watcher/server
		// lifecycle. A warm discovery result is therefore authoritative until
		// InvalidateRoot is called; rescanning the workspace here would turn every
		// lexical completion into a filesystem walk.
		return append([]GoPackageCandidate(nil), cached...), nil
	}

	patterns := make([]string, 0)
	if contents, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		if module, parseErr := modfile.Parse("go.mod", contents, nil); parseErr == nil {
			patterns = make([]string, 0, len(module.Require))
			for _, requirement := range module.Require {
				pattern := requirement.Mod.Path
				if isGoxSDKPackage(pattern) {
					pattern += "/..."
				}
				patterns = append(patterns, pattern)
			}
		}
	}

	// Standard-library discovery is intentionally filesystem-owned. Exhaustive
	// equivalence coverage keeps this scanner aligned with the public inventory
	// from packages.Load(NeedName, "std") while avoiding the go-list subprocess
	// on every cold discovery rebuild.
	candidates := discoverStandardLibraryGoPackages()
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		seen[candidate.Path] = true
	}

	var loaded []*packages.Package
	var loadErr error
	if len(patterns) > 0 {
		loaded, loadErr = packages.Load(&packages.Config{Mode: packages.NeedName, Dir: root}, patterns...)
	}
	if loadErr == nil {
		for _, candidate := range loaded {
			if candidate == nil || candidate.PkgPath == "" || candidate.Name == "" || candidate.Name == "main" ||
				candidate.Name == "_" || !goPackageCandidateVisible(candidate.PkgPath) || strings.HasSuffix(candidate.PkgPath, ".test") || seen[candidate.PkgPath] || len(candidate.Errors) > 0 {
				continue
			}
			seen[candidate.PkgPath] = true
			candidates = append(candidates, GoPackageCandidate{Path: candidate.PkgPath, Name: candidate.Name})
		}
	}
	candidates = mergeGoPackageCandidates(candidates, discoverWorkspaceGoPackages(root))
	if loadErr != nil && len(candidates) == 0 {
		return nil, loadErr
	}
	i.mu.Lock()
	i.discovery[root] = append([]GoPackageCandidate(nil), candidates...)
	i.mu.Unlock()
	return candidates, nil
}

func mergeGoPackageCandidates(base, local []GoPackageCandidate) []GoPackageCandidate {
	seen := make(map[string]bool, len(base)+len(local))
	merged := make([]GoPackageCandidate, 0, len(base)+len(local))
	for _, candidates := range [][]GoPackageCandidate{base, local} {
		for _, candidate := range candidates {
			if !goPackageCandidateVisible(candidate.Path) || candidate.Name == "" || seen[candidate.Path] {
				continue
			}
			seen[candidate.Path] = true
			merged = append(merged, candidate)
		}
	}
	sort.Slice(merged, func(left, right int) bool {
		if merged[left].Name != merged[right].Name {
			return merged[left].Name < merged[right].Name
		}
		return merged[left].Path < merged[right].Path
	})
	return merged
}

// goPackageCandidateVisible excludes implementation-only packages from Go's
// standard-library inventory. Paths containing internal or vendor are not part
// of the public import surface offered to GOX projects.
func goPackageCandidateVisible(importPath string) bool {
	return importPath != "" &&
		!strings.HasPrefix(importPath, "internal/") &&
		!strings.Contains(importPath, "/internal/") &&
		!strings.HasPrefix(importPath, "vendor/") &&
		!strings.Contains(importPath, "/vendor/")
}

// discoverWorkspaceGoPackages indexes the current module directly. Standard
// library discovery is filesystem-owned and go/packages remains responsible for
// declared dependencies. File selection must follow the active Go build
// context; otherwise an inactive GOOS/GOARCH or //go:build file can give the
// workspace package the wrong declared name.
func discoverWorkspaceGoPackages(root string) []GoPackageCandidate {
	return discoverWorkspaceGoPackagesWithContext(root, build.Default)
}

func discoverWorkspaceGoPackagesWithContext(root string, context build.Context) []GoPackageCandidate {
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil
	}
	modulePath := modfile.ModulePath(contents)
	if modulePath == "" {
		return nil
	}
	var candidates []GoPackageCandidate
	_ = filepath.WalkDir(root, func(directory string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || !entry.IsDir() {
			return nil
		}
		if directory != root {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
				return filepath.SkipDir
			}
		}
		entries, readErr := os.ReadDir(directory)
		if readErr != nil {
			return nil
		}
		packageName, ok := workspaceGoPackageName(context, directory, entries)
		if !ok || packageName == "main" || packageName == "_" {
			return nil
		}
		relative, relErr := filepath.Rel(root, directory)
		if relErr != nil {
			return nil
		}
		importPath := modulePath
		if relative != "." {
			importPath += "/" + filepath.ToSlash(relative)
		}
		candidates = append(candidates, GoPackageCandidate{Path: importPath, Name: packageName})
		return nil
	})
	return candidates
}

// workspaceGoPackageName returns the single declared package name among the
// non-test Go files selected by the active build context. A directory with no
// active files, malformed package clauses, or conflicting active package names
// is not a valid workspace package candidate.
func workspaceGoPackageName(context build.Context, directory string, entries []os.DirEntry) (string, bool) {
	packageName := ""
	for _, file := range entries {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		matches, matchErr := context.MatchFile(directory, file.Name())
		if matchErr != nil || !matches {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, file.Name()), nil, parser.PackageClauseOnly)
		if parseErr != nil || parsed.Name == nil || parsed.Name.Name == "" {
			return "", false
		}
		if packageName == "" {
			packageName = parsed.Name.Name
			continue
		}
		if parsed.Name.Name != packageName {
			return "", false
		}
	}
	return packageName, packageName != ""
}

// PackageName resolves only the identifier declared by a Go package. It is
// intentionally separate from Load: alias resolution during completion must
// not type-check the package, parse its dependency graph, or build exports just
// to learn the package clause name.
func (i *GoPackageIndex) PackageName(root, importPath string) (string, error) {
	key := root + "\x00" + importPath
	i.mu.RLock()
	name, ok := i.packageNames[key]
	if !ok {
		for _, candidate := range i.discovery[root] {
			if candidate.Path == importPath && candidate.Name != "" {
				name, ok = candidate.Name, true
				break
			}
		}
	}
	i.mu.RUnlock()
	if ok {
		return name, nil
	}

	loaded, err := packages.Load(&packages.Config{Mode: packages.NeedName, Dir: root}, importPath)
	if err != nil {
		return "", err
	}
	if len(loaded) != 1 || loaded[0] == nil || loaded[0].Name == "" {
		return "", fmt.Errorf("Go package %q could not be loaded", importPath)
	}
	if len(loaded[0].Errors) > 0 {
		return "", fmt.Errorf("Go package %q: %s", importPath, loaded[0].Errors[0].Msg)
	}
	name = loaded[0].Name
	i.mu.Lock()
	i.packageNames[key] = name
	i.mu.Unlock()
	return name, nil
}

func (i *GoPackageIndex) Load(root, importPath string) (GoPackage, error) {
	key := root + "\x00" + importPath
	i.mu.RLock()
	entry, ok := i.cache[key]
	i.mu.RUnlock()
	if ok && goPackageFilesCurrent(entry.files) {
		return entry.packageInfo, nil
	}

	loaded, files, err := loadGoPackage(root, importPath)
	i.mu.Lock()
	if err == nil {
		i.cache[key] = goPackageCacheEntry{packageInfo: loaded, files: files}
	}
	i.mu.Unlock()
	return loaded, err
}

func (i *GoPackageIndex) Invalidate() {
	i.mu.Lock()
	i.cache = make(map[string]goPackageCacheEntry)
	i.packageNames = make(map[string]string)
	i.discovery = make(map[string][]GoPackageCandidate)
	i.exports = make(map[string][]GoPackage)
	i.exportSources = make(map[string]map[string]goGlobalPackageSource)
	i.exportDetails = make(map[string]GoPackage)
	i.projectGraphs = make(map[string]*projectGoImportGraph)
	i.projectGraphGeneration = make(map[string]uint64)
	i.projectGraphEpoch++
	i.mu.Unlock()
}

// InvalidateRoot drops only package data associated with one workspace. Other
// workspace folders keep their warm caches when module metadata changes
// elsewhere. Local .go edits use InvalidateProjectGoPackage instead.
func (i *GoPackageIndex) InvalidateRoot(root string) {
	root = filepath.Clean(root)
	prefix := root + "\x00"
	i.mu.Lock()
	delete(i.discovery, root)
	delete(i.exports, root)
	delete(i.projectGraphs, root)
	i.projectGraphGeneration[root]++
	delete(i.exportSources, root)
	for key := range i.exportDetails {
		if strings.HasPrefix(key, prefix) {
			delete(i.exportDetails, key)
		}
	}
	for key := range i.cache {
		if strings.HasPrefix(key, prefix) {
			delete(i.cache, key)
		}
	}
	for key := range i.packageNames {
		if strings.HasPrefix(key, prefix) {
			delete(i.packageNames, key)
		}
	}
	i.mu.Unlock()
}

// InvalidateProjectGoPackage updates only the index layers that can depend on a
// local module package. Global shallow exports/details describe stdlib and
// declared module dependencies, so a source edit inside the current module
// must not evict those expensive root-wide caches.
//
// affected contains the changed package and every local package that imports it
// transitively. Their typed/source projections may embed types from the changed
// package and are therefore evicted together. discovery is patched in place for
// the changed directory so stdlib/dependency inventory remains warm.
func (i *GoPackageIndex) InvalidateProjectGoPackage(root, importPath string, affected []string, candidate *GoPackageCandidate) {
	if i == nil || importPath == "" {
		return
	}
	root = filepath.Clean(root)
	affectedSet := make(map[string]struct{}, len(affected)+1)
	affectedSet[importPath] = struct{}{}
	for _, path := range affected {
		if path != "" {
			affectedSet[path] = struct{}{}
		}
	}

	i.mu.Lock()
	if cached, ok := i.discovery[root]; ok {
		updated := make([]GoPackageCandidate, 0, len(cached)+1)
		for _, existing := range cached {
			if existing.Path != importPath {
				updated = append(updated, existing)
			}
		}
		if candidate != nil && candidate.Path == importPath && candidate.Name != "" && goPackageCandidateVisible(candidate.Path) {
			updated = append(updated, *candidate)
		}
		sort.Slice(updated, func(left, right int) bool {
			if updated[left].Name != updated[right].Name {
				return updated[left].Name < updated[right].Name
			}
			return updated[left].Path < updated[right].Path
		})
		i.discovery[root] = updated
	}
	prefix := root + "\x00"
	for path := range affectedSet {
		key := prefix + path
		delete(i.cache, key)
		delete(i.packageNames, key)
	}
	i.mu.Unlock()
}

// ExportedPackages returns a shallow global export-name index for the standard
// library and declared module dependencies. It deliberately records only the
// package, exported identifier, and coarse declaration kind. Completion can
// therefore reject almost every package using names alone and materialize
// signatures/types only for packages that actually match the typed prefix.
func (i *GoPackageIndex) ExportedPackages(root string) ([]GoPackage, error) {
	i.mu.RLock()
	cached, ok := i.exports[root]
	i.mu.RUnlock()
	if ok {
		return append([]GoPackage(nil), cached...), nil
	}

	patterns := []string{"std"}
	includeGoxSDK := false
	if contents, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		if module, parseErr := modfile.Parse("go.mod", contents, nil); parseErr == nil {
			for _, requirement := range module.Require {
				if isGoxSDKPackage(requirement.Mod.Path) {
					includeGoxSDK = true
					patterns = append(patterns, requirement.Mod.Path+"/...")
					continue
				}
				patterns = append(patterns, requirement.Mod.Path)
			}
		}
	}

	loaded, loadErr := packages.Load(&packages.Config{Mode: goGlobalExportFileMode, Dir: root}, patterns...)
	packagesWithExports := make([]GoPackage, 0, len(loaded))
	sources := make(map[string]goGlobalPackageSource, len(loaded)+1)
	seen := make(map[string]bool, len(loaded)+1)
	for _, loadedPackage := range loaded {
		if loadedPackage == nil || isGoxSDKPackage(loadedPackage.PkgPath) || loadedPackage.Name == "main" || !goPackageCandidateVisible(loadedPackage.PkgPath) ||
			len(loadedPackage.Errors) > 0 || seen[loadedPackage.PkgPath] {
			continue
		}
		seen[loadedPackage.PkgPath] = true
		packageInfo, packageErr := goPackageExportNamesFromScanner(loadedPackage, loadedPackage.PkgPath, false)
		if packageErr != nil || len(packageInfo.Exports) == 0 {
			continue
		}
		packagesWithExports = append(packagesWithExports, packageInfo)
		sources[loadedPackage.PkgPath] = goGlobalPackageSource{
			Name: loadedPackage.Name, Files: append([]string(nil), loadedPackage.GoFiles...),
		}
	}
	if includeGoxSDK && !seen[goxSDKImportPath] {
		if loadedPackage, packageErr := loadGoxSDKGlobalSource(root); packageErr == nil {
			if packageInfo, indexErr := goPackageExportNamesFromScanner(loadedPackage, goxSDKImportPath, true); indexErr == nil && len(packageInfo.Exports) > 0 {
				packagesWithExports = append(packagesWithExports, packageInfo)
				sources[goxSDKImportPath] = goGlobalPackageSource{
					Name: loadedPackage.Name, Files: append([]string(nil), loadedPackage.GoFiles...), PreserveInternal: true,
				}
				seen[goxSDKImportPath] = true
			}
		}
	}
	sort.Slice(packagesWithExports, func(left, right int) bool {
		return packagesWithExports[left].Path < packagesWithExports[right].Path
	})
	i.mu.Lock()
	i.exports[root] = append([]GoPackage(nil), packagesWithExports...)
	i.exportSources[root] = sources
	i.mu.Unlock()
	if loadErr != nil && len(packagesWithExports) == 0 {
		return nil, loadErr
	}
	return append([]GoPackage(nil), packagesWithExports...), nil
}

// ExportedPackageDetails materializes the richer source projection for one
// package from the file manifest captured while building ExportedPackages. It
// is intentionally lazy: a global prefix such as Spl should pay detailed AST
// printing/type-shape extraction only for packages that export matching names.
func (i *GoPackageIndex) ExportedPackageDetails(root, importPath string) (GoPackage, error) {
	key := root + "\x00" + importPath
	i.mu.RLock()
	if cached, ok := i.exportDetails[key]; ok {
		i.mu.RUnlock()
		return cached, nil
	}
	source, ok := i.exportSources[root][importPath]
	i.mu.RUnlock()
	if !ok {
		if _, err := i.ExportedPackages(root); err != nil {
			return GoPackage{}, err
		}
		i.mu.RLock()
		source, ok = i.exportSources[root][importPath]
		i.mu.RUnlock()
		if !ok {
			return GoPackage{}, fmt.Errorf("Go package %q is not present in the global export index", importPath)
		}
	}

	loaded := &packages.Package{Name: source.Name, PkgPath: importPath, GoFiles: append([]string(nil), source.Files...)}
	packageInfo, err := goPackageExportsFromSource(loaded, importPath, source.PreserveInternal)
	if err != nil {
		return GoPackage{}, err
	}
	i.mu.Lock()
	if cached, exists := i.exportDetails[key]; exists {
		packageInfo = cached
	} else {
		i.exportDetails[key] = packageInfo
	}
	i.mu.Unlock()
	return packageInfo, nil
}

func loadGoxSDKGlobalSource(root string) (*packages.Package, error) {
	loaded, err := packages.Load(&packages.Config{Mode: goGlobalExportFileMode, Dir: root}, goxSDKImportPath)
	if err != nil {
		return nil, err
	}
	if len(loaded) != 1 || loaded[0] == nil {
		return nil, fmt.Errorf("Go package %q could not be loaded", goxSDKImportPath)
	}
	if len(loaded[0].Errors) > 0 {
		return nil, fmt.Errorf("Go package %q: %s", goxSDKImportPath, loaded[0].Errors[0].Msg)
	}
	return loaded[0], nil
}

// goPackageExportsFromSource builds the detailed metadata required only after
// the shallow global name index has identified a matching package.
func goPackageExportsFromSource(loaded *packages.Package, importPath string, preserveInternalMarkers bool) (GoPackage, error) {
	if loaded == nil {
		return GoPackage{}, fmt.Errorf("Go package %q could not be loaded", importPath)
	}
	files := loaded.GoFiles
	if len(files) == 0 {
		return GoPackage{}, fmt.Errorf("Go package %q has no source files", importPath)
	}

	fset := token.NewFileSet()
	exports := make(map[string]GoPackageExport)
	var firstParseErr error
	for _, filename := range files {
		mode := parser.SkipObjectResolution
		if preserveInternalMarkers {
			mode |= parser.ParseComments
		}
		file, err := parser.ParseFile(fset, filename, nil, mode)
		if err != nil {
			if firstParseErr == nil {
				firstParseErr = err
			}
			continue
		}
		var internal map[token.Pos]struct{}
		if preserveInternalMarkers {
			internal = goInternalAPIObjects([]*ast.File{file})
		}
		goSourceFileExports(fset, file, internal, exports)
	}
	if len(exports) == 0 && firstParseErr != nil {
		return GoPackage{}, firstParseErr
	}

	result := make([]GoPackageExport, 0, len(exports))
	for _, exported := range exports {
		result = append(result, exported)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return GoPackage{Path: importPath, Name: loaded.Name, Exports: result}, nil
}

func goSourceFileExports(fset *token.FileSet, file *ast.File, internal map[token.Pos]struct{}, exports map[string]GoPackageExport) {
	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			if value.Recv != nil || value.Name == nil || !token.IsExported(value.Name.Name) || goSourcePositionIsInternal(value.Name.Pos(), internal) {
				continue
			}
			exports[value.Name.Name] = goSourceFunctionExport(fset, value)
		case *ast.GenDecl:
			goSourceGenDeclExports(fset, value, internal, exports)
		}
	}
}

func goSourceFunctionExport(fset *token.FileSet, declaration *ast.FuncDecl) GoPackageExport {
	parameters := goSourceParameters(fset, declaration.Type.Params)
	results := goSourceResultTypes(fset, declaration.Type.Results)
	resultType := ""
	if len(results) == 1 {
		resultType = results[0]
	}
	copyDecl := *declaration
	copyDecl.Doc = nil
	copyDecl.Body = nil
	return GoPackageExport{
		Name: declaration.Name.Name, Kind: "func", Detail: goSourceNodeString(fset, &copyDecl),
		Type: resultType, Results: results, Parameters: parameters,
	}
}

func goSourceGenDeclExports(fset *token.FileSet, declaration *ast.GenDecl, internal map[token.Pos]struct{}, exports map[string]GoPackageExport) {
	switch declaration.Tok {
	case token.TYPE:
		for _, raw := range declaration.Specs {
			specification, ok := raw.(*ast.TypeSpec)
			if !ok || specification.Name == nil || !token.IsExported(specification.Name.Name) || goSourcePositionIsInternal(specification.Name.Pos(), internal) {
				continue
			}
			kind := "type"
			switch specification.Type.(type) {
			case *ast.StructType:
				kind = "struct"
			case *ast.InterfaceType:
				kind = "interface"
			}
			copySpec := *specification
			copySpec.Doc = nil
			copySpec.Comment = nil
			copyDecl := &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&copySpec}}
			exports[specification.Name.Name] = GoPackageExport{
				Name: specification.Name.Name, Kind: kind, Detail: goSourceNodeString(fset, copyDecl),
				Underlying: goSourceNodeString(fset, specification.Type), Alias: specification.Assign.IsValid(),
			}
		}
	case token.CONST, token.VAR:
		var inheritedType ast.Expr
		var inheritedValues []ast.Expr
		for _, raw := range declaration.Specs {
			specification, ok := raw.(*ast.ValueSpec)
			if !ok {
				continue
			}
			effectiveType := specification.Type
			effectiveValues := specification.Values
			if declaration.Tok == token.CONST {
				if effectiveType == nil && len(effectiveValues) == 0 {
					effectiveType = inheritedType
					effectiveValues = inheritedValues
				} else {
					inheritedType = effectiveType
					inheritedValues = effectiveValues
				}
			}
			for index, name := range specification.Names {
				if name == nil || !token.IsExported(name.Name) || goSourcePositionIsInternal(name.Pos(), internal) {
					continue
				}
				var expression ast.Expr
				if len(effectiveValues) == 1 {
					expression = effectiveValues[0]
				} else if index < len(effectiveValues) {
					expression = effectiveValues[index]
				}
				typeName := goSourceNodeString(fset, effectiveType)
				if typeName == "" {
					typeName = goSourceExpressionType(expression)
				}
				valueText := ""
				if declaration.Tok == token.CONST {
					valueText = goSourceNodeString(fset, expression)
				}
				kind := "var"
				if declaration.Tok == token.CONST {
					kind = "const"
				}
				detail := kind + " " + name.Name
				if typeName != "" {
					detail += " " + typeName
				}
				if expressionText := goSourceNodeString(fset, expression); expressionText != "" {
					detail += " = " + expressionText
				}
				exports[name.Name] = GoPackageExport{Name: name.Name, Kind: kind, Detail: detail, Type: typeName, Value: valueText}
			}
		}
	}
}

func goSourceParameters(fset *token.FileSet, fields *ast.FieldList) []goxcompiler.SourceParameter {
	if fields == nil {
		return nil
	}
	var parameters []goxcompiler.SourceParameter
	for _, field := range fields.List {
		typeName := goSourceNodeString(fset, field.Type)
		if len(field.Names) == 0 {
			parameters = append(parameters, goxcompiler.SourceParameter{Type: typeName})
			continue
		}
		for _, name := range field.Names {
			parameters = append(parameters, goxcompiler.SourceParameter{Name: name.Name, Type: typeName})
		}
	}
	return parameters
}

func goSourceResultTypes(fset *token.FileSet, fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	var results []string
	for _, field := range fields.List {
		typeName := goSourceNodeString(fset, field.Type)
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			results = append(results, typeName)
		}
	}
	return results
}

func goSourceNodeString(fset *token.FileSet, node any) string {
	if node == nil {
		return ""
	}
	var builder strings.Builder
	if err := printer.Fprint(&builder, fset, node); err != nil {
		return ""
	}
	return strings.TrimSpace(builder.String())
}

func goSourceExpressionType(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.BasicLit:
		switch value.Kind {
		case token.INT:
			return "untyped int"
		case token.FLOAT:
			return "untyped float"
		case token.IMAG:
			return "untyped complex"
		case token.CHAR:
			return "untyped rune"
		case token.STRING:
			return "untyped string"
		}
	case *ast.Ident:
		if value.Name == "true" || value.Name == "false" {
			return "untyped bool"
		}
	case *ast.ParenExpr:
		return goSourceExpressionType(value.X)
	case *ast.UnaryExpr:
		return goSourceExpressionType(value.X)
	case *ast.BinaryExpr:
		left := goSourceExpressionType(value.X)
		right := goSourceExpressionType(value.Y)
		if left == right {
			return left
		}
	}
	return ""
}

func goSourcePositionIsInternal(position token.Pos, internal map[token.Pos]struct{}) bool {
	if len(internal) == 0 {
		return false
	}
	_, ok := internal[position]
	return ok
}

func loadGoPackage(root, importPath string) (GoPackage, map[string]goPackageFileStamp, error) {
	configuration := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports | packages.NeedDeps |
			packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax,
		Dir: root,
	}
	loaded, err := packages.Load(configuration, importPath)
	if err != nil {
		return GoPackage{}, nil, err
	}
	if len(loaded) != 1 || loaded[0].Types == nil {
		return GoPackage{}, nil, fmt.Errorf("Go package %q could not be loaded", importPath)
	}
	if len(loaded[0].Errors) > 0 {
		return GoPackage{}, nil, fmt.Errorf("Go package %q: %s", importPath, loaded[0].Errors[0].Msg)
	}
	packageInfo, packageErr := goPackageFromLoaded(loaded[0], importPath)
	if packageErr != nil {
		return GoPackage{}, nil, packageErr
	}
	files := append([]string(nil), loaded[0].CompiledGoFiles...)
	files = append(files, filepath.Join(root, "go.mod"), filepath.Join(root, "go.sum"))
	return packageInfo, goPackageFileStamps(files), nil
}

func goPackageFromLoaded(loaded *packages.Package, importPath string) (GoPackage, error) {
	if loaded == nil || loaded.Types == nil {
		return GoPackage{}, fmt.Errorf("Go package %q could not be loaded", importPath)
	}
	pkg := loaded.Types
	internalObjects := goInternalAPIObjects(loaded.Syntax)
	scope := pkg.Scope()
	names := scope.Names()
	sort.Strings(names)
	exports := make([]GoPackageExport, 0, len(names))
	qualifier := func(other *types.Package) string {
		if other == pkg {
			return ""
		}
		return other.Name()
	}
	for _, name := range names {
		if !token.IsExported(name) {
			continue
		}
		object := scope.Lookup(name)
		if goObjectIsInternal(object, internalObjects) {
			continue
		}
		exported := GoPackageExport{
			Name: name, Detail: goObjectDetail(object, qualifier), Kind: goObjectKind(object),
			Definition: goDefinitionAt(loaded.Fset, object.Pos(), len(name)),
		}
		exported.Type, exported.Results, exported.Parameters = goObjectCompletionTypes(object, qualifier)
		exported.Underlying, exported.Alias = goObjectUnderlyingType(object, qualifier)
		if constant, ok := object.(*types.Const); ok {
			exported.Value = constant.Val().ExactString()
		}
		if typeName, ok := object.(*types.TypeName); ok {
			exported.Members = goTypeMembers(typeName, qualifier, internalObjects, loaded.Fset)
		}
		exports = append(exports, exported)
	}
	return GoPackage{
		Path: importPath, Name: pkg.Name(),
		Definition: goPackageDefinition(loaded.Fset, loaded.Syntax, pkg.Name()),
		Exports:    exports,
	}, nil
}

func goTypeMembers(object *types.TypeName, qualifier types.Qualifier, internalObjects map[token.Pos]struct{}, fset *token.FileSet) []GoPackageMember {
	if object == nil {
		return nil
	}
	typeValue := types.Unalias(object.Type())
	named, ok := typeValue.(*types.Named)
	if !ok {
		return nil
	}
	members := make(map[string]GoPackageMember)
	if structure, ok := named.Underlying().(*types.Struct); ok {
		for index := 0; index < structure.NumFields(); index++ {
			field := structure.Field(index)
			if !field.Exported() || goObjectIsInternal(field, internalObjects) {
				continue
			}
			members[field.Name()] = GoPackageMember{
				Name: field.Name(), Kind: "field", Definition: goDefinitionAt(fset, field.Pos(), len(field.Name())),
				Detail:     "field " + field.Name() + " " + types.TypeString(field.Type(), qualifier),
				Type:       types.TypeString(field.Type(), qualifier),
				Underlying: types.TypeString(field.Type().Underlying(), qualifier),
			}
		}
	}
	appendMethods := func(methodSet *types.MethodSet) {
		for index := 0; index < methodSet.Len(); index++ {
			method, ok := methodSet.At(index).Obj().(*types.Func)
			if !ok || !method.Exported() || goObjectIsInternal(method, internalObjects) {
				continue
			}
			member := GoPackageMember{Name: method.Name(), Kind: "method", Detail: goObjectDetail(method, qualifier), Definition: goDefinitionAt(fset, method.Pos(), len(method.Name()))}
			member.Type, member.Results, member.Parameters = goObjectCompletionTypes(method, qualifier)
			members[method.Name()] = member
		}
	}
	appendMethods(types.NewMethodSet(named))
	appendMethods(types.NewMethodSet(types.NewPointer(named)))
	result := make([]GoPackageMember, 0, len(members))
	for _, member := range members {
		result = append(result, member)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func goObjectIsInternal(object types.Object, internalObjects map[token.Pos]struct{}) bool {
	if object == nil {
		return false
	}
	_, internal := internalObjects[object.Pos()]
	return internal
}

func goInternalAPIObjects(files []*ast.File) map[token.Pos]struct{} {
	internal := make(map[token.Pos]struct{})
	for _, file := range files {
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if goCommentHasInternalMarker(value.Doc) {
					internal[value.Name.Pos()] = struct{}{}
				}
			case *ast.GenDecl:
				declarationInternal := goCommentHasInternalMarker(value.Doc)
				for _, specification := range value.Specs {
					switch spec := specification.(type) {
					case *ast.TypeSpec:
						if declarationInternal || goCommentHasInternalMarker(spec.Doc) || goCommentHasInternalMarker(spec.Comment) {
							internal[spec.Name.Pos()] = struct{}{}
						}
						goInternalTypeMembers(spec.Type, internal)
					case *ast.ValueSpec:
						if declarationInternal || goCommentHasInternalMarker(spec.Doc) || goCommentHasInternalMarker(spec.Comment) {
							for _, name := range spec.Names {
								internal[name.Pos()] = struct{}{}
							}
						}
					}
				}
			}
		}
	}
	return internal
}

func goInternalTypeMembers(expression ast.Expr, internal map[token.Pos]struct{}) {
	var fields *ast.FieldList
	switch value := expression.(type) {
	case *ast.StructType:
		fields = value.Fields
	case *ast.InterfaceType:
		fields = value.Methods
	}
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		if !goCommentHasInternalMarker(field.Doc) && !goCommentHasInternalMarker(field.Comment) {
			continue
		}
		for _, name := range field.Names {
			internal[name.Pos()] = struct{}{}
		}
	}
}

func goCommentHasInternalMarker(group *ast.CommentGroup) bool {
	if group == nil {
		return false
	}
	for _, comment := range group.List {
		if strings.Contains(comment.Text, goxInternalAPIMarker) {
			return true
		}
	}
	return false
}

func goObjectUnderlyingType(object types.Object, qualifier types.Qualifier) (string, bool) {
	typeName, ok := object.(*types.TypeName)
	if !ok {
		return "", false
	}
	if alias, ok := typeName.Type().(*types.Alias); ok {
		return types.TypeString(alias.Rhs(), qualifier), true
	}
	return types.TypeString(typeName.Type().Underlying(), qualifier), false
}

func goObjectCompletionTypes(object types.Object, qualifier types.Qualifier) (string, []string, []goxcompiler.SourceParameter) {
	switch value := object.(type) {
	case *types.Func:
		signature, ok := value.Type().(*types.Signature)
		if !ok {
			return "", nil, nil
		}
		var results []string
		for index := 0; index < signature.Results().Len(); index++ {
			results = append(results, types.TypeString(signature.Results().At(index).Type(), qualifier))
		}
		var parameters []goxcompiler.SourceParameter
		for index := 0; index < signature.Params().Len(); index++ {
			parameter := signature.Params().At(index)
			parameterType := types.TypeString(parameter.Type(), qualifier)
			if signature.Variadic() && index == signature.Params().Len()-1 {
				if slice, ok := parameter.Type().(*types.Slice); ok {
					parameterType = "..." + types.TypeString(slice.Elem(), qualifier)
				}
			}
			parameters = append(parameters, goxcompiler.SourceParameter{Name: parameter.Name(), Type: parameterType})
		}
		typeName := ""
		if len(results) == 1 {
			typeName = results[0]
		}
		return typeName, results, parameters
	case *types.Var:
		return types.TypeString(value.Type(), qualifier), nil, nil
	case *types.Const:
		return types.TypeString(value.Type(), qualifier), nil, nil
	}
	return "", nil, nil
}

func goPackageDefinition(files *token.FileSet, syntax []*ast.File, name string) GoDefinition {
	for _, file := range syntax {
		if file != nil && file.Name != nil {
			return goDefinitionAt(files, file.Name.Pos(), len(name))
		}
	}
	return GoDefinition{}
}

func goDefinitionAt(files *token.FileSet, position token.Pos, length int) GoDefinition {
	if files == nil || !position.IsValid() {
		return GoDefinition{}
	}
	resolved := files.PositionFor(position, true)
	if resolved.Filename == "" || resolved.Offset < 0 {
		return GoDefinition{}
	}
	return GoDefinition{Path: resolved.Filename, Offset: resolved.Offset, Length: length}
}

func goPackageFileStamps(paths []string) map[string]goPackageFileStamp {
	stamps := make(map[string]goPackageFileStamp, len(paths))
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil {
			stamps[path] = goPackageFileStamp{size: info.Size(), modTime: info.ModTime().UnixNano()}
		}
	}
	return stamps
}

func goPackageFilesCurrent(stamps map[string]goPackageFileStamp) bool {
	for path, stamp := range stamps {
		info, err := os.Stat(path)
		if err != nil || info.Size() != stamp.size || info.ModTime().UnixNano() != stamp.modTime {
			return false
		}
	}
	return true
}

func goObjectKind(object types.Object) string {
	switch value := object.(type) {
	case *types.TypeName:
		switch value.Type().Underlying().(type) {
		case *types.Struct:
			return "struct"
		case *types.Interface:
			return "interface"
		default:
			return "type"
		}
	case *types.Func:
		return "func"
	case *types.Const:
		return "const"
	case *types.Var:
		return "var"
	default:
		return "symbol"
	}
}
