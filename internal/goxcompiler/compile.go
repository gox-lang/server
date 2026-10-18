package gox

import (
	"github.com/gox-lang/server/internal/clientbuilder"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/projecttoolchain"
	websocketmeta "github.com/gox-lang/server/internal/websocket"

	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func Compile(entryPath string, outPath string) (*CompileResult, error) {
	startedAt := time.Now()

	result := &CompileResult{
		EntryPath:     entryPath,
		OutPath:       outPath,
		SourceMapPath: BundleSourceMapPath(outPath),
		StartedAt:     startedAt,
	}

	if absEntry, err := filepath.Abs(entryPath); err == nil {
		result.EntryAbsPath = absEntry
	}

	if absOut, err := filepath.Abs(outPath); err == nil {
		result.OutAbsPath = absOut
	}

	defer func() {
		result.FinishedAt = time.Now()
		result.Duration = result.FinishedAt.Sub(result.StartedAt)
	}()

	loadStart := time.Now()
	graph, err := LoadGraph(entryPath)
	result.LoadGraphDuration = time.Since(loadStart)
	if err != nil {
		return result, err
	}

	if err := validateProjectGoGoroutineSafety(graph.RootDir); err != nil {
		return result, err
	}

	result.Dependencies = buildCompileDependencies(graph)
	result.GoPackages, err = resolveCompileGoPackages(graph)
	if err != nil {
		return result, err
	}
	result.ResolutionDependencies = append([]string(nil), graph.ResolutionDependencies...)
	result.Modules = buildCompileModules(graph)
	result.ClientComponents = buildCompileClientComponents(graph)
	result.ModuleCount = len(graph.Modules)
	result.DependencyCount = len(result.Dependencies)

	// Materialize managed client workspaces before building the Go bundle.
	// The bundle embeds the framework import map, so framework discovery must
	// observe workspaces required by this compile, including on the first build.
	clientAssets := compileClientBuilderAssets(graph.RootDir, result.ClientComponents)
	if _, err := clientbuilder.EnsureFrameworkWorkspaces(graph.RootDir, clientAssets); err != nil {
		return result, err
	}

	bundleStart := time.Now()
	bundle, err := BundleModules(graph)
	result.BundleDuration = time.Since(bundleStart)
	if err != nil {
		return result, err
	}

	result.GoPackage = bundle.GoPackage

	var actionsRuntimeURL string
	if bundleHasActions(bundle) {
		actionsRuntimeAsset, err := clientbuilder.EnsureGoxActionsRuntime(graph.RootDir)
		if err != nil {
			return result, err
		}
		if result.ClientAssetMap == nil {
			result.ClientAssetMap = make(map[string]string)
		}
		result.ClientAssetMap[actionsRuntimeAsset.URL] = actionsRuntimeAsset.File
		result.ActionsRuntimeURL = actionsRuntimeAsset.URL
		actionsRuntimeURL = actionsRuntimeAsset.URL
	}
	_, channelBundle := bundleChannelHandler(bundle)
	if !bundleHasWebSocketHandler(bundle) && !channelBundle {
		installedFrameworks := clientbuilder.DetectFrameworkWorkspaces(graph.RootDir)
		AddActionsClientScriptDecl(bundle, actionsRuntimeURL, clientFrameworkImportMapEntries(graph.RootDir, installedFrameworks))
	}

	writeStart := time.Now()
	err = WriteBundle(bundle, outPath)
	result.WriteDuration = time.Since(writeStart)
	if err != nil {
		return result, err
	}

	builtClientAssets, err := buildInstalledClientAssets(clientAssets)
	if err != nil {
		return result, err
	}
	applyBuiltClientAssets(result, builtClientAssets)
	if len(result.ClientComponents) > 0 {
		runtimeAsset, err := clientbuilder.EnsureGoxIsleRuntime(graph.RootDir)
		if err != nil {
			return result, err
		}
		baseStyleAsset, err := clientbuilder.EnsureGoxIsleBaseStyle(graph.RootDir)
		if err != nil {
			return result, err
		}
		if result.ClientAssetMap == nil {
			result.ClientAssetMap = make(map[string]string)
		}
		result.ClientAssetMap[runtimeAsset.URL] = runtimeAsset.File
		result.ClientAssetMap[baseStyleAsset.URL] = baseStyleAsset.File
		result.ClientRuntimeURL = runtimeAsset.URL
		result.ClientBaseStyleURL = baseStyleAsset.URL
	}

	result.Written = true

	if info, statErr := os.Stat(outPath); statErr == nil {
		result.OutputBytes = info.Size()
	}

	return result, nil
}

func buildCompileDependencies(graph *ModuleGraph) []CompileDependency {
	importedBy := buildImportedByIndex(graph)
	out := make([]CompileDependency, 0, len(graph.Modules))

	for _, mod := range graph.Modules {
		dep := CompileDependency{
			Path:               mod.Path,
			AbsPath:            mod.Path,
			ModuleName:         mod.Name,
			DeclaredModuleName: mod.DeclaredName,
			IsEntry:            graph.Entry != nil && mod.Path == graph.Entry.Path,
			Imports:            moduleResolvedImports(mod),
			GoImports:          moduleGoImports(mod),
			ImportedBy:         importedBy[mod.Path],
			LoadDuration:       mod.LoadDuration,
		}

		if info, err := os.Stat(mod.Path); err == nil {
			dep.Size = info.Size()
			dep.ModTime = info.ModTime()
		}

		out = append(out, dep)
	}

	sortCompileDependencies(out)
	return out
}

func buildCompileModules(graph *ModuleGraph) []CompileModule {
	importedBy := buildImportedByIndex(graph)
	out := make([]CompileModule, 0, len(graph.Modules))

	for _, mod := range graph.Modules {
		out = append(out, CompileModule{
			Path:               mod.Path,
			AbsPath:            mod.Path,
			ModuleName:         mod.Name,
			DeclaredModuleName: mod.DeclaredName,
			IsEntry:            graph.Entry != nil && mod.Path == graph.Entry.Path,
			Imports:            moduleResolvedImports(mod),
			GoImports:          moduleGoImports(mod),
			Clients:            moduleClientComponents(mod, graph.RootDir),
			ImportedBy:         importedBy[mod.Path],
			LoadDuration:       mod.LoadDuration,
		})
	}

	sortCompileModules(out)
	return out
}

func buildCompileClientComponents(graph *ModuleGraph) []CompileClientComponent {
	out := make([]CompileClientComponent, 0)
	for _, mod := range graph.Modules {
		out = append(out, moduleClientComponents(mod, graph.RootDir)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Entry == out[j].Entry {
			if out[i].Framework == out[j].Framework {
				if out[i].Name == out[j].Name {
					return out[i].ModulePath < out[j].ModulePath
				}
				return out[i].Name < out[j].Name
			}
			return out[i].Framework < out[j].Framework
		}
		return out[i].Entry < out[j].Entry
	})
	return out
}

func moduleClientComponents(mod *Module, rootDir string) []CompileClientComponent {
	clients := make([]CompileClientComponent, 0, len(mod.Clients))
	for _, client := range mod.Clients {
		framework := strings.ToLower(string(client.Framework))
		mode := "prod"
		if client.Dev {
			mode = "dev"
		}
		clientPath := firstCompileNonEmpty(client.EntryPath, client.Entry)
		clientKey := client.Key
		if strings.TrimSpace(clientKey) == "" {
			clientKey = ClientComponentKey(framework, client.Entry, client.Dev)
		}
		clients = append(clients, CompileClientComponent{
			Name:          client.Name,
			Key:           clientKey,
			Framework:     framework,
			Entry:         client.Entry,
			Path:          clientPath,
			AbsPath:       client.EntryAbsPath,
			ModulePath:    clientRelPath(rootDir, mod.Path),
			ModuleAbsPath: mod.Path,
			Dev:           client.Dev,
			Mode:          mode,
			OutputDir:     clientBuildOutputDir(framework, clientPath, mode),
		})
	}
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].Path == clients[j].Path {
			if clients[i].Framework == clients[j].Framework {
				if clients[i].Name == clients[j].Name {
					return clients[i].ModulePath < clients[j].ModulePath
				}
				return clients[i].Name < clients[j].Name
			}
			return clients[i].Framework < clients[j].Framework
		}
		return clients[i].Path < clients[j].Path
	})
	return clients
}

func clientBuildOutputDir(framework string, clientPath string, mode string) string {
	framework = strings.ToLower(strings.TrimSpace(framework))
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "prod"
	}
	if framework == "" || strings.TrimSpace(clientPath) == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(".build", "client", framework, mode))
}

func clientRelPath(rootDir string, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if strings.TrimSpace(rootDir) == "" {
		return filepath.ToSlash(path)
	}
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return filepath.ToSlash(path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func moduleResolvedImports(mod *Module) []string {
	imports := make([]string, 0, len(mod.GoxImports))

	for _, imp := range mod.GoxImports {
		path := imp.ResolvedPath
		if path == "" {
			path = imp.Path
		}
		imports = append(imports, path)
	}

	sort.Strings(imports)
	return imports
}

func moduleGoImports(mod *Module) []string {
	imports := make([]string, 0, len(mod.GoImports))

	for _, imp := range mod.GoImports {
		imports = append(imports, imp.Path)
	}

	sort.Strings(imports)
	return imports
}

func buildImportedByIndex(graph *ModuleGraph) map[string][]string {
	importedBy := make(map[string][]string, len(graph.Modules))

	for _, mod := range graph.Modules {
		if _, ok := importedBy[mod.Path]; !ok {
			importedBy[mod.Path] = nil
		}

		for _, imp := range mod.GoxImports {
			if imp.ResolvedPath == "" {
				continue
			}

			importedBy[imp.ResolvedPath] = append(importedBy[imp.ResolvedPath], mod.Path)
		}
	}

	for path := range importedBy {
		sort.Strings(importedBy[path])
	}

	return importedBy
}

func sortCompileDependencies(deps []CompileDependency) {
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].IsEntry != deps[j].IsEntry {
			return deps[i].IsEntry
		}
		return deps[i].AbsPath < deps[j].AbsPath
	})
}

func sortCompileModules(mods []CompileModule) {
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].IsEntry != mods[j].IsEntry {
			return mods[i].IsEntry
		}
		return mods[i].AbsPath < mods[j].AbsPath
	})
}

func clientFrameworkImportMapEntries(projectRoot string, frameworks []string) map[string]string {
	entries := map[string]string{
		websocketmeta.ImportSpecifier: websocketmeta.ClientPath,
	}
	for _, framework := range frameworks {
		version, err := clientbuilder.FrameworkVersion(projectRoot, framework)
		if err != nil {
			continue
		}
		for _, item := range clientbuilder.FrameworkImportMapEntries(framework, version) {
			if strings.TrimSpace(item.Specifier) == "" || strings.TrimSpace(item.URL) == "" {
				continue
			}
			entries[item.Specifier] = item.URL
		}
	}
	return entries
}

func compileClientBuilderAssets(rootDir string, clients []CompileClientComponent) []clientbuilder.Asset {
	assets := make([]clientbuilder.Asset, 0, len(clients))
	seen := make(map[string]struct{}, len(clients))
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		rootAbs = rootDir
	}
	for _, client := range clients {
		outputAbsDir := ""
		if strings.TrimSpace(client.OutputDir) != "" {
			outputAbsDir = filepath.Join(rootAbs, filepath.FromSlash(client.OutputDir))
		}
		frameworkRoot := filepath.Join(rootAbs, "client", client.Framework)
		asset := clientbuilder.Asset{
			Name:          client.Name,
			Framework:     client.Framework,
			Entry:         client.Entry,
			Path:          client.Path,
			AbsPath:       client.AbsPath,
			Dev:           client.Dev,
			Mode:          client.Mode,
			OutputDir:     client.OutputDir,
			OutputAbsDir:  outputAbsDir,
			ProjectRoot:   rootAbs,
			FrameworkRoot: frameworkRoot,
		}
		identity := asset.BuildIdentity()
		if identity == "" {
			continue
		}
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		assets = append(assets, asset)
	}
	return assets
}

func buildInstalledClientAssets(assets []clientbuilder.Asset) ([]clientbuilder.BuiltAsset, error) {
	if len(assets) == 0 {
		return nil, nil
	}

	built := make([]clientbuilder.BuiltAsset, len(assets))
	skipped := make([]bool, len(assets))
	needsBuildByGroup := map[string]bool{}
	installedByGroup := map[string][]int{}
	groupKeys := make([]string, 0)
	seenGroups := map[string]struct{}{}

	for index, asset := range assets {
		if !clientbuilder.IsViteWorkspaceInstalled(asset) {
			skipped[index] = true
			continue
		}

		groupKey := clientBuildGroupKey(asset)
		if _, ok := seenGroups[groupKey]; !ok {
			seenGroups[groupKey] = struct{}{}
			groupKeys = append(groupKeys, groupKey)
		}
		installedByGroup[groupKey] = append(installedByGroup[groupKey], index)

		if cached, ok := clientbuilder.TryLoadCachedViteAsset(asset); ok {
			built[index] = cached
			continue
		}
		needsBuildByGroup[groupKey] = true
	}

	sort.Strings(groupKeys)
	toBuild := make([]clientbuilder.Asset, 0)
	buildIndexesByKey := map[string]int{}
	for _, groupKey := range groupKeys {
		indexes := installedByGroup[groupKey]
		if len(indexes) == 0 || !needsBuildByGroup[groupKey] {
			continue
		}
		// Multi-entry Vite builds share one output directory per framework/mode.
		// If any entry in the group is stale, rebuild the entire group so
		// emptyOutDir cannot delete assets that another entry still expects.
		for _, index := range indexes {
			asset := assets[index]
			buildIndexesByKey[clientBuildEntryKey(asset)] = index
			toBuild = append(toBuild, asset)
			built[index] = clientbuilder.BuiltAsset{}
		}
	}

	if len(toBuild) > 0 {
		nodePaths, pnpmPaths, err := projecttoolchain.Ensure(assets[0].ProjectRoot)
		if err != nil {
			return nil, err
		}

		fresh, err := clientbuilder.BuildViteAssets(nodePaths, pnpmPaths, toBuild)
		if err != nil {
			return nil, err
		}
		for _, item := range fresh {
			if index, ok := buildIndexesByKey[clientBuildEntryKey(item.Asset)]; ok {
				built[index] = item
			}
		}
	}

	skippedCount := 0
	for _, item := range skipped {
		if item {
			skippedCount++
		}
	}
	if len(toBuild) == 0 && skippedCount > 0 && skippedCount == len(assets) {
		logs.Info("client build skipped: framework workspace dependencies are not installed")
	}

	out := make([]clientbuilder.BuiltAsset, 0, len(assets)-skippedCount)
	for _, item := range built {
		if strings.TrimSpace(item.EntryFile) != "" {
			out = append(out, item)
		}
	}
	return out, nil
}

func clientBuildGroupKey(asset clientbuilder.Asset) string {
	framework := strings.ToLower(strings.TrimSpace(asset.Framework))
	mode := strings.ToLower(strings.TrimSpace(asset.Mode))
	if mode == "" {
		if asset.Dev {
			mode = clientbuilder.ModeDev
		} else {
			mode = clientbuilder.ModeProd
		}
	}
	projectRoot := cleanClientBuildPath(asset.ProjectRoot)
	frameworkRoot := cleanClientBuildPath(asset.FrameworkRoot)
	return projectRoot + "\x00" + frameworkRoot + "\x00" + framework + "\x00" + mode
}

func clientBuildEntryKey(asset clientbuilder.Asset) string {
	return asset.BuildIdentity()
}

func cleanClientBuildPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func compileClientGraphNodes(nodes []clientbuilder.ClientDependencyGraphNode) []CompileClientDependencyGraphNode {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]CompileClientDependencyGraphNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, CompileClientDependencyGraphNode{
			File:           node.File,
			Imports:        append([]string(nil), node.Imports...),
			DynamicImports: append([]string(nil), node.DynamicImports...),
		})
	}
	return out
}

func applyBuiltClientAssets(result *CompileResult, built []clientbuilder.BuiltAsset) {
	if result == nil || len(built) == 0 {
		return
	}
	assetMap := make(map[string]string)
	for url, file := range result.ClientAssetMap {
		assetMap[url] = file
	}
	bundles := make([]CompileClientBundle, 0, len(built))
	for _, item := range built {
		bundle := CompileClientBundle{
			Name:      item.Asset.Name,
			Framework: item.Asset.Framework,
			Entry:     item.Asset.Entry,
			Path:      item.Asset.Path,
			Mode:      item.Asset.Mode,
			EntryURL:  item.EntryURL,
			EntryFile: item.EntryFile,
			Assets:    make([]CompileClientAsset, 0, len(item.Files)),
			Graph: CompileClientDependencyGraph{
				Entry:          item.Graph.Entry,
				EntryFile:      item.Graph.EntryFile,
				Framework:      item.Graph.Framework,
				Component:      item.Graph.Component,
				OutputFiles:    append([]string(nil), item.Graph.OutputFiles...),
				ModuleFiles:    append([]string(nil), item.Graph.ModuleFiles...),
				Imports:        append([]string(nil), item.Graph.Imports...),
				DynamicImports: append([]string(nil), item.Graph.DynamicImports...),
				Nodes:          compileClientGraphNodes(item.Graph.Nodes),
			},
		}
		for _, file := range item.Files {
			if strings.TrimSpace(file.URL) == "" || strings.TrimSpace(file.File) == "" {
				continue
			}
			bundle.Assets = append(bundle.Assets, CompileClientAsset{Kind: file.Kind, URL: file.URL, File: file.File})
			assetMap[file.URL] = file.File
		}
		bundles = append(bundles, bundle)
	}
	result.ClientBundles = bundles
	result.ClientAssetMap = assetMap
}
