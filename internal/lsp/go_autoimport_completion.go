package lsp

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"
	"golang.org/x/mod/modfile"
)

type autoImportGoPackage struct {
	Alias       string
	ImportPath  string
	PackageInfo GoPackage
	Edit        CompletionTextEdit
}

// goAutoImportCompletionState owns the immutable Go auto-import context for
// one completion/code-action request. Building this state once prevents the
// three lexical auto-import branches from independently rediscovering the same
// packages, reparsing go.mod, rebuilding alias state, and constructing import
// edits for candidates that will later be filtered out by the typed prefix.
type goAutoImportCompletionState struct {
	root            string
	modulePath      string
	importerPath    string
	importRanges    []goxmap.Range
	imported        map[string]bool
	unavailable     map[string]bool
	candidates      []GoPackageCandidate
	candidateByPath map[string]GoPackageCandidate
	prepared        map[string]autoImportGoPackage
}

func (s *Server) newGoAutoImportCompletionState(document Document, offset int) *goAutoImportCompletionState {
	root := s.goImportRoot(document.Path)
	modulePath := goModulePath(root)
	imports, ranges := s.completionGoImportState(document)
	state := &goAutoImportCompletionState{
		root:            root,
		modulePath:      modulePath,
		importerPath:    goImportPathForFileWithModulePath(root, document.Path, modulePath),
		importRanges:    ranges,
		imported:        make(map[string]bool, len(imports)),
		unavailable:     make(map[string]bool),
		candidateByPath: make(map[string]GoPackageCandidate),
		prepared:        make(map[string]autoImportGoPackage),
	}
	candidates, err := s.goPackages.Discover(root)
	if err == nil {
		state.candidates = candidates
		for _, candidate := range candidates {
			state.candidateByPath[candidate.Path] = candidate
		}
	}
	for _, declaration := range imports {
		state.imported[declaration.Path] = true
		name := declaration.Alias
		if name == "" {
			name = s.goAutoImportPackageName(state, root, declaration.Path)
		}
		if name != "" {
			state.unavailable[name] = true
		}
	}
	for _, local := range s.symbols.VisibleLocalSymbols(document.URI, offset) {
		if local.Name != "" {
			state.unavailable[local.Name] = true
		}
	}
	for _, indexed := range s.symbols.DocumentSymbols(document.URI) {
		symbol := indexed.Symbol
		if symbol.Container == "" && symbol.Kind != goxcompiler.SymbolModule && symbol.Name != "" {
			state.unavailable[symbol.Name] = true
		}
	}
	for _, declaration := range s.symbols.Imports(document.URI) {
		name := declaration.Name
		if name == "" {
			name, _, _ = s.inferredGoxImportAlias(document, declaration)
		}
		if name != "" {
			state.unavailable[name] = true
		}
	}
	// completionGoImportState can reflect a live semantic module while the
	// symbol index still contains the last stable document. Keep the indexed
	// imports in the unavailable-alias set too, matching the previous alias
	// validation contract without re-running it once per package candidate.
	indexedRoot := s.projectRoot(document.Path)
	for _, declaration := range s.symbols.GoImports(document.URI) {
		name := declaration.Alias
		if name == "" {
			name = s.goAutoImportPackageName(state, indexedRoot, declaration.Path)
		}
		if name != "" {
			state.unavailable[name] = true
		}
	}
	return state
}

func (s *Server) goAutoImportPackageName(state *goAutoImportCompletionState, root, importPath string) string {
	if state != nil && samePath(root, state.root) {
		if candidate, ok := state.candidateByPath[importPath]; ok && candidate.Name != "" {
			return candidate.Name
		}
	}
	name, err := s.goPackages.PackageName(root, importPath)
	if err != nil {
		return ""
	}
	return name
}

func goAutoImportCandidateAllowed(state *goAutoImportCompletionState, candidate GoPackageCandidate) bool {
	return state != nil && candidate.Name != "" && !state.imported[candidate.Path] && candidate.Path != state.importerPath &&
		goInternalImportAllowed(state.importerPath, candidate.Path) && !state.unavailable[candidate.Name]
}

func (s *Server) prepareGoAutoImportCandidate(document Document, state *goAutoImportCompletionState, candidate GoPackageCandidate) (autoImportGoPackage, bool) {
	if !goAutoImportCandidateAllowed(state, candidate) {
		return autoImportGoPackage{}, false
	}
	if prepared, ok := state.prepared[candidate.Path]; ok {
		return prepared, true
	}
	edit, ok := goxAutoImportEdit(document, state.importRanges, candidate.Path, candidate.Name, true)
	if !ok {
		return autoImportGoPackage{}, false
	}
	prepared := autoImportGoPackage{
		Alias: candidate.Name, ImportPath: candidate.Path,
		PackageInfo: GoPackage{Path: candidate.Path, Name: candidate.Name}, Edit: edit,
	}
	state.prepared[candidate.Path] = prepared
	return prepared, true
}

func (s *Server) autoImportGoPackageCompletions(document Document, context lexicalCompletionContext, state *goAutoImportCompletionState) []CompletionItem {
	if context.Prefix == "" || state == nil {
		return nil
	}
	var items []CompletionItem
	for _, discovered := range state.candidates {
		// Prefix filtering must happen before alias validation/import-edit
		// construction. Most packages can be rejected using only their cheap
		// discovery metadata.
		if !strings.HasPrefix(discovered.Name, context.Prefix) {
			continue
		}
		candidate, ok := s.prepareGoAutoImportCandidate(document, state, discovered)
		if !ok {
			continue
		}
		items = append(items, CompletionItem{
			Label: candidate.Alias, Kind: 9, Detail: "Auto-import Go package from " + candidate.ImportPath,
			LabelDetails: &CompletionItemLabelDetails{Description: candidate.ImportPath},
			InsertText:   candidate.Alias, SortText: goAutoImportSortPrefix(candidate.ImportPath) + candidate.Alias + "_" + candidate.ImportPath,
			AdditionalTextEdits: []CompletionTextEdit{candidate.Edit},
		})
	}
	return items
}

// autoImportWorkspaceGoExportCompletions finds exported declarations from Go
// packages in the current module. Selecting Create from package register, for
// example, writes register.Create and adds the declared package alias import.
// Dependencies are intentionally excluded here: their unqualified symbols
// require a separate global export index rather than loading every dependency
// while the user types.
func (s *Server) autoImportWorkspaceGoExportCompletions(document Document, context lexicalCompletionContext, state *goAutoImportCompletionState) []CompletionItem {
	if context.Prefix == "" || !token.IsExported(context.Prefix) || state == nil || state.modulePath == "" {
		return nil
	}
	var items []CompletionItem
	for _, discovered := range state.candidates {
		if !goImportPathInModule(discovered.Path, state.modulePath) || !goAutoImportCandidateAllowed(state, discovered) {
			continue
		}
		packageInfo, err := s.goPackages.Load(state.root, discovered.Path)
		if err != nil {
			continue
		}
		var matching []GoPackageExport
		for _, exported := range packageInfo.Exports {
			if strings.HasPrefix(exported.Name, context.Prefix) {
				matching = append(matching, exported)
			}
		}
		if len(matching) == 0 {
			continue
		}
		candidate, ok := s.prepareGoAutoImportCandidate(document, state, discovered)
		if !ok {
			continue
		}
		for _, exported := range matching {
			items = append(items, CompletionItem{
				Label:               exported.Name,
				LabelDetails:        &CompletionItemLabelDetails{Description: candidate.ImportPath},
				Kind:                goExportCompletionKind(exported.Kind),
				Detail:              exported.Detail + " — auto-import " + candidate.ImportPath,
				InsertText:          candidate.Alias + "." + exported.Name,
				SortText:            "4_" + exported.Name + "_" + candidate.ImportPath,
				CandidateType:       qualifyGoPackageType(packageInfo, candidate.Alias, exported.Type),
				CandidateUnderlying: qualifyGoPackageType(packageInfo, candidate.Alias, exported.Underlying),
				CandidateAlias:      exported.Alias,
				CandidateValue:      exported.Value,
				CandidateResults:    qualifyGoPackageTypes(packageInfo, candidate.Alias, exported.Results),
				CandidateParameters: exported.Parameters,
				AdditionalTextEdits: []CompletionTextEdit{candidate.Edit},
			})
		}
	}
	return items
}

// autoImportDependencyGoExportCompletions completes unqualified public
// symbols from the standard library and declared module dependencies. The
// expensive global source/export scan is performed once by GoPackageIndex and
// subsequent completion requests only filter that in-memory index.
func (s *Server) autoImportDependencyGoExportCompletions(document Document, context lexicalCompletionContext, state *goAutoImportCompletionState) []CompletionItem {
	if context.Prefix == "" || !token.IsExported(context.Prefix) || state == nil {
		return nil
	}
	packages, err := s.goPackages.ExportedPackages(state.root)
	if err != nil {
		return nil
	}
	var items []CompletionItem
	for _, packageIndex := range packages {
		if isGoxCompletionPackage(packageIndex.Path) || goImportPathInModule(packageIndex.Path, state.modulePath) {
			continue
		}
		discovered, ok := state.candidateByPath[packageIndex.Path]
		if !ok || !goAutoImportCandidateAllowed(state, discovered) {
			continue
		}
		hasMatchingName := false
		for _, exported := range packageIndex.Exports {
			if strings.HasPrefix(exported.Name, context.Prefix) {
				hasMatchingName = true
				break
			}
		}
		if !hasMatchingName {
			continue
		}

		packageInfo, err := s.goPackages.ExportedPackageDetails(state.root, packageIndex.Path)
		if err != nil {
			continue
		}
		var matching []GoPackageExport
		for _, exported := range packageInfo.Exports {
			if strings.HasPrefix(exported.Name, context.Prefix) {
				matching = append(matching, exported)
			}
		}
		if len(matching) == 0 {
			continue
		}
		candidate, ok := s.prepareGoAutoImportCandidate(document, state, discovered)
		if !ok {
			continue
		}
		for _, exported := range matching {
			items = append(items, CompletionItem{
				Label:               exported.Name,
				LabelDetails:        &CompletionItemLabelDetails{Description: candidate.ImportPath},
				Kind:                goExportCompletionKind(exported.Kind),
				Detail:              exported.Detail + " — auto-import " + candidate.ImportPath,
				InsertText:          candidate.Alias + "." + exported.Name,
				SortText:            "5_" + exported.Name + "_" + candidate.ImportPath,
				CandidateType:       qualifyGoPackageType(packageInfo, candidate.Alias, exported.Type),
				CandidateUnderlying: qualifyGoPackageType(packageInfo, candidate.Alias, exported.Underlying),
				CandidateAlias:      exported.Alias,
				CandidateValue:      exported.Value,
				CandidateResults:    qualifyGoPackageTypes(packageInfo, candidate.Alias, exported.Results),
				CandidateParameters: exported.Parameters,
				AdditionalTextEdits: []CompletionTextEdit{candidate.Edit},
			})
		}
	}
	return items
}

func (s *Server) autoImportGoPackageMemberCompletions(document Document, context qualifiedCompletionContext, offset int) []CompletionItem {
	state := s.newGoAutoImportCompletionState(document, offset)
	var items []CompletionItem
	for _, discovered := range state.candidates {
		if discovered.Name != context.Qualifier {
			continue
		}
		candidate, ok := s.prepareGoAutoImportCandidate(document, state, discovered)
		if !ok {
			continue
		}
		packageInfo, err := s.goPackages.Load(state.root, discovered.Path)
		if err != nil {
			continue
		}
		candidate.PackageInfo = packageInfo
		for _, exported := range candidate.PackageInfo.Exports {
			if !strings.HasPrefix(exported.Name, context.Prefix) {
				continue
			}
			item := CompletionItem{
				Label: exported.Name, Kind: goExportCompletionKind(exported.Kind),
				LabelDetails: &CompletionItemLabelDetails{Description: candidate.ImportPath},
				Detail:       exported.Detail + " — auto-import " + candidate.ImportPath,
				InsertText:   exported.Name, SortText: goAutoImportSortPrefix(candidate.ImportPath) + exported.Name + "_" + candidate.ImportPath,
				CandidateType:       qualifyGoPackageType(candidate.PackageInfo, candidate.Alias, exported.Type),
				CandidateUnderlying: qualifyGoPackageType(candidate.PackageInfo, candidate.Alias, exported.Underlying),
				CandidateAlias:      exported.Alias, CandidateValue: exported.Value,
				CandidateResults:    qualifyGoPackageTypes(candidate.PackageInfo, candidate.Alias, exported.Results),
				CandidateParameters: exported.Parameters,
				AdditionalTextEdits: []CompletionTextEdit{candidate.Edit},
			}
			items = append(items, item)
		}
	}
	return items
}

func goAutoImportSortPrefix(importPath string) string {
	if isGoxCompletionPackage(importPath) {
		return "3_"
	}
	return "4_"
}

// autoImportGoPackages remains the shared entry point used by code actions.
// Lexical completion builds one request state and reuses it across its three
// auto-import branches instead of calling this wrapper repeatedly.
func (s *Server) autoImportGoPackages(document Document, offset int, qualifier string) []autoImportGoPackage {
	state := s.newGoAutoImportCompletionState(document, offset)
	var result []autoImportGoPackage
	for _, discovered := range state.candidates {
		if qualifier != "" && discovered.Name != qualifier {
			continue
		}
		candidate, ok := s.prepareGoAutoImportCandidate(document, state, discovered)
		if !ok {
			continue
		}
		if qualifier != "" {
			packageInfo, err := s.goPackages.Load(state.root, discovered.Path)
			if err != nil {
				continue
			}
			candidate.PackageInfo = packageInfo
		}
		result = append(result, candidate)
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Alias != result[right].Alias {
			return result[left].Alias < result[right].Alias
		}
		return result[left].ImportPath < result[right].ImportPath
	})
	return result
}

func goImportPathForFileWithModulePath(root, filename, modulePath string) string {
	if modulePath == "" {
		return ""
	}
	relative, err := filepath.Rel(root, filepath.Dir(filename))
	if err != nil || relative == "." {
		return modulePath
	}
	return strings.TrimSuffix(modulePath, "/") + "/" + filepath.ToSlash(relative)
}

func goModulePath(root string) string {
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	return modfile.ModulePath(contents)
}

func goImportPathInModule(importPath, modulePath string) bool {
	return importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")
}

func goInternalImportAllowed(importerPath, importPath string) bool {
	if strings.HasPrefix(importPath, "internal/") {
		return false
	}
	marker := "/internal/"
	index := strings.LastIndex(importPath, marker)
	if index < 0 {
		return true
	}
	parent := importPath[:index]
	return importerPath == parent || strings.HasPrefix(importerPath, parent+"/")
}

func (s *Server) completionGoImportState(document Document) ([]goxcompiler.ImportDecl, []goxmap.Range) {
	if module, err := s.analysisModule(document); err == nil {
		return module.GoImports, goxImportSourceRanges(module.GoxImports, module.GoImports)
	}
	return s.symbols.GoImports(document.URI), goxImportSourceRanges(s.symbols.Imports(document.URI), s.symbols.GoImports(document.URI))
}

// Include SDK subpackages without changing the root SDK's public API lowering.
func isGoxCompletionPackage(path string) bool {
	return path == goxSDKImportPath || strings.HasPrefix(path, goxSDKImportPath+"/")
}

func (s *Server) goxSDKExportCompletions(document Document, context lexicalCompletionContext, state *goAutoImportCompletionState, offset int) []CompletionItem {
	if state == nil || !token.IsExported(context.Prefix) {
		return nil
	}
	packages, err := s.goPackages.ExportedPackages(state.root)
	if err != nil {
		return nil
	}
	imports, _ := s.completionGoImportState(document)
	aliases := map[string]string{}
	for _, declaration := range imports {
		alias := declaration.Alias
		if alias == "" {
			alias = s.goAutoImportPackageName(state, state.root, declaration.Path)
		}
		aliases[declaration.Path] = alias
	}
	// Runtime lowering canonicalizes the SDK alias; completion must use the
	// alias written by the user, before lowering.
	if file, _ := parser.ParseFile(token.NewFileSet(), document.Path, strings.Replace(document.Text, "module ", "package ", 1), parser.ImportsOnly); file != nil {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err == nil && spec.Name != nil {
				aliases[path] = spec.Name.Name
			}
		}
	}
	var items []CompletionItem
	for _, indexed := range packages {
		if !isGoxCompletionPackage(indexed.Path) || !goInternalImportAllowed(state.importerPath, indexed.Path) {
			continue
		}
		match := false
		for _, export := range indexed.Exports {
			if strings.HasPrefix(strings.ToLower(export.Name), strings.ToLower(context.Prefix)) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		alias, imported := aliases[indexed.Path]
		var edits []CompletionTextEdit
		if imported {
			if alias == "_" || alias == "." || alias == "" {
				continue
			}
			shadowed := false
			for _, local := range s.symbols.VisibleLocalSymbols(document.URI, offset) {
				if local.Name == alias {
					shadowed = true
					break
				}
			}
			if shadowed {
				continue
			}
		} else {
			candidate, ok := state.candidateByPath[indexed.Path]
			if !ok {
				continue
			}
			prepared, ok := s.prepareGoAutoImportCandidate(document, state, candidate)
			if !ok {
				continue
			}
			alias = prepared.Alias
			edits = []CompletionTextEdit{prepared.Edit}
		}
		info, err := s.goPackages.ExportedPackageDetails(state.root, indexed.Path)
		if err != nil {
			continue
		}
		for _, exported := range info.Exports {
			if !strings.HasPrefix(strings.ToLower(exported.Name), strings.ToLower(context.Prefix)) {
				continue
			}
			items = append(items, CompletionItem{
				Label: exported.Name, LabelDetails: &CompletionItemLabelDetails{Description: indexed.Path},
				Kind: goExportCompletionKind(exported.Kind), Detail: exported.Detail + " — " + indexed.Path,
				InsertText: alias + "." + exported.Name, SortText: "3_" + exported.Name + "_" + indexed.Path,
				CandidateType:       qualifyGoPackageType(info, alias, exported.Type),
				CandidateUnderlying: qualifyGoPackageType(info, alias, exported.Underlying),
				CandidateAlias:      exported.Alias, CandidateValue: exported.Value,
				CandidateResults: qualifyGoPackageTypes(info, alias, exported.Results), CandidateParameters: exported.Parameters,
				AdditionalTextEdits: edits,
			})
		}
	}
	return items
}
