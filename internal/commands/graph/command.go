package graph

import (
	"fmt"
	"github.com/gox-lang/server/internal/buildinfo"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/clientbuilder"
	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/project"
	"github.com/gox-lang/server/internal/projecttoolchain"
	goxruntime "github.com/gox-lang/server/internal/runtime"
	"github.com/gox-lang/server/internal/serverruntime"
	"github.com/gox-lang/server/internal/toolchain"

	"github.com/gox-lang/gox/server"
)

type Command struct{}

func New() *Command { return &Command{} }

func (c *Command) Name() string { return "graph" }

func (c *Command) Description() string { return "Show the GOX dependency graph" }

func (c *Command) Help() string {
	return `gox graph

Show the GOX dependency graph.

Usage:
  gox graph <filepath.gox> [options]
  gox graph -r <route> [options]

Options:
  -r, --route           Interpret the argument as a public page route
  -v, --verbose         Show dependency type details
  -f, --filter <gxcd>   Filter dependency families
  -h, --help            Show help information`
}

type graphFilter struct {
	Go         bool
	Gox        bool
	Client     bool
	ClientDeps bool
}

type graphOptions struct {
	EntryPath string
	Route     bool
	Verbose   bool
	Filter    graphFilter
}

func defaultGraphFilter() graphFilter {
	return graphFilter{Go: true, Gox: true, Client: true, ClientDeps: true}
}

func (f graphFilter) empty() bool { return !f.Go && !f.Gox && !f.Client && !f.ClientDeps }

func parseGraphFilter(raw string) (graphFilter, error) {
	var filter graphFilter
	if strings.TrimSpace(raw) == "" {
		return filter, fmt.Errorf("graph filter cannot be empty: supported filters are g, x, c, d")
	}
	for _, r := range raw {
		switch r {
		case 'g':
			filter.Go = true
		case 'x':
			filter.Gox = true
		case 'c':
			filter.Client = true
		case 'd':
			filter.ClientDeps = true
			filter.Client = true
		default:
			return filter, fmt.Errorf("invalid graph filter %q: supported filters are g, x, c, d", raw)
		}
	}
	if filter.empty() {
		return filter, fmt.Errorf("graph filter cannot be empty: supported filters are g, x, c, d")
	}
	return filter, nil
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	opts, err := parseGraphArgs(args)
	if err != nil {
		return err
	}

	p, err := project.Detect(ctx.WorkDir)
	if err != nil {
		return err
	}

	entryPath := opts.EntryPath
	if opts.Route {
		route, err := validateGraphRoute(opts.EntryPath)
		if err != nil {
			return err
		}
		if err := p.UseAsWorkingDir(); err != nil {
			return err
		}
		var loadedConfig *goxruntime.LoadedConfig
		err = runGraphRouteSilently(func() error {
			tc := toolchain.New(buildinfo.GoVersion)
			paths, err := toolchain.Ensure(tc)
			if err != nil {
				return err
			}
			loadedConfig, err = goxruntime.CompileConfigPlugin(p, paths, ctx.Stdout)
			if err != nil {
				return err
			}
			return goxruntime.CleanupConfigBuildArtifacts(p.RootDir, loadedConfig)
		})
		if err != nil {
			return err
		}
		entry, err := resolveGraphRoute(p, route, loadedConfig.Config.Routes)
		if err != nil {
			return err
		}
		entryPath = entry.File
	}

	entryRel, entryAbs, err := resolveGraphEntry(p.RootDir, entryPath)
	if err != nil {
		return err
	}

	result, err := goxcompiler.Analyze(entryAbs)
	if err != nil {
		return err
	}
	if opts.Filter.ClientDeps {
		if err := attachClientDependencyGraphs(result, p.RootDir); err != nil {
			return err
		}
	}

	fmt.Fprint(ctx.Stdout, FormatWithFilter(result, p.RootDir, entryRel, opts.Verbose, opts.Filter))
	return nil
}

func runGraphRouteSilently(action func() error) error {
	logsWasEnabled := logs.Enabled()
	logs.SetEnabled(false)
	defer logs.SetEnabled(logsWasEnabled)
	return action()
}

func parseGraphArgs(args []string) (graphOptions, error) {
	opts := graphOptions{Filter: defaultGraphFilter()}
	filterWasSet := false
	optionsEnded := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !optionsEnded && arg == "--" {
			optionsEnded = true
			continue
		}
		switch {
		case !optionsEnded && (arg == "-v" || arg == "--verbose"):
			opts.Verbose = true
		case !optionsEnded && (arg == "-r" || arg == "--route"):
			opts.Route = true
		case !optionsEnded && (arg == "-f" || arg == "--filter"):
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires a filter value", arg)
			}
			filter, err := parseGraphFilter(args[i+1])
			if err != nil {
				return opts, err
			}
			opts.Filter = filter
			filterWasSet = true
			i++
		case !optionsEnded && strings.HasPrefix(arg, "--filter="):
			filter, err := parseGraphFilter(strings.TrimPrefix(arg, "--filter="))
			if err != nil {
				return opts, err
			}
			opts.Filter = filter
			filterWasSet = true
		default:
			if !optionsEnded && strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown graph option: %s", arg)
			}
			if opts.EntryPath != "" {
				return opts, fmt.Errorf("usage: gox graph <filepath.gox> [options]")
			}
			opts.EntryPath = arg
		}
	}

	if opts.EntryPath == "" {
		return opts, fmt.Errorf("usage: gox graph <filepath.gox> [options]")
	}
	if !filterWasSet && opts.Filter.empty() {
		opts.Filter = defaultGraphFilter()
	}

	return opts, nil
}

func resolveGraphRoute(p *project.Project, raw string, config server.RoutesConfig) (project.RouteEntry, error) {
	route, err := validateGraphRoute(raw)
	if err != nil {
		return project.RouteEntry{}, err
	}

	router, err := serverruntime.NewPreRouter(config)
	if err != nil {
		return project.RouteEntry{}, err
	}
	target := route
	if target == "/" {
		target = "/index"
	}
	if _, matchedTarget, _, ok := router.Match(route); ok {
		target = matchedTarget
	}
	if path.Ext(target) != "" {
		return project.RouteEntry{}, fmt.Errorf("route resolves to a static target: %s", target)
	}

	entry, ok, err := p.ResolvePageRoute(target)
	if err != nil {
		return project.RouteEntry{}, err
	}
	if !ok {
		return project.RouteEntry{}, fmt.Errorf("route not found: %s", route)
	}
	return entry, nil
}

func validateGraphRoute(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("invalid page route: %s", raw)
	}
	if strings.ContainsAny(raw, "\\?#%") || strings.Contains(raw, "//") {
		return "", fmt.Errorf("invalid page route: %s", raw)
	}
	for _, value := range raw {
		if unicode.IsSpace(value) || unicode.IsControl(value) {
			return "", fmt.Errorf("invalid page route: %s", raw)
		}
	}
	for _, segment := range strings.Split(strings.Trim(raw, "/"), "/") {
		if segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return "", fmt.Errorf("invalid page route: %s", raw)
		}
	}
	if len(raw) > 1 {
		raw = strings.TrimRight(raw, "/")
	}
	return raw, nil
}

func resolveGraphEntry(rootDir string, raw string) (string, string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", "", fmt.Errorf("graph path is empty")
	}

	rawPath := filepath.FromSlash(raw)
	if filepath.IsAbs(rawPath) {
		return "", "", fmt.Errorf("graph path must be relative to the project root: %s", raw)
	}
	rawParts := strings.Split(rawPath, string(filepath.Separator))
	for _, part := range rawParts {
		if part == ".." {
			return "", "", fmt.Errorf("graph path cannot contain ..: %s", raw)
		}
	}

	clean := filepath.Clean(rawPath)
	if clean == "." {
		return "", "", fmt.Errorf("graph path is empty")
	}
	if filepath.Ext(clean) != ".gox" {
		return "", "", fmt.Errorf("graph path must end in .gox: %s", raw)
	}

	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return "", "", err
	}
	entryAbs, err := filepath.Abs(filepath.Join(rootAbs, clean))
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(rootAbs, entryAbs)
	if err != nil {
		return "", "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("graph path escapes project root: %s", raw)
	}

	return filepath.ToSlash(rel), entryAbs, nil
}

type graphModule struct {
	Path       string
	ModuleName string
	Imports    []string
	GoImports  []string
	Clients    []goxcompiler.CompileClientComponent
	Bundles    map[string]goxcompiler.CompileClientBundle
	IsEntry    bool
}

type graphGoPackage struct {
	ImportPath string
	Imports    []string
}

func attachClientDependencyGraphs(result *goxcompiler.CompileResult, rootDir string) error {
	if result == nil || len(result.ClientComponents) == 0 {
		return nil
	}
	assets, cleanup, err := graphProbeAssets(rootDir, result.ClientComponents)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err
	}
	assets = clientbuilder.InstalledViteAssets(assets)
	if len(assets) == 0 {
		return nil
	}

	logsWasEnabled := logs.Enabled()
	logs.SetEnabled(false)
	defer logs.SetEnabled(logsWasEnabled)

	nodePaths, pnpmPaths, err := projecttoolchain.Ensure(rootDir)
	if err != nil {
		return err
	}

	bundles := make([]goxcompiler.CompileClientBundle, 0, len(assets))
	assetMap := map[string]string{}
	for _, asset := range assets {
		if err := writeGraphProbeViteConfig(asset); err != nil {
			return err
		}
		built, err := clientbuilder.BuildViteAsset(nodePaths, pnpmPaths, asset)
		if err != nil {
			return err
		}
		bundle := compileClientBundleFromBuiltAsset(built)
		bundles = append(bundles, bundle)
		for _, file := range bundle.Assets {
			if file.URL != "" && file.File != "" {
				assetMap[file.URL] = file.File
			}
		}
	}
	result.ClientBundles = bundles
	result.ClientAssetMap = assetMap
	return nil
}

func graphProbeAssets(rootDir string, clients []goxcompiler.CompileClientComponent) ([]clientbuilder.Asset, func(), error) {
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		rootAbs = rootDir
	}
	tmpRoot, err := os.MkdirTemp("", "gox-client-graph-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpRoot) }

	assets := make([]clientbuilder.Asset, 0, len(clients))
	for _, client := range clients {
		mode := strings.TrimSpace(client.Mode)
		if mode == "" {
			mode = clientbuilder.ModeProd
		}
		outDir := filepath.Join(tmpRoot, client.Framework, filepath.FromSlash(client.Path), mode)
		assets = append(assets, clientbuilder.Asset{
			Name:          client.Name,
			Framework:     client.Framework,
			Entry:         client.Entry,
			Path:          client.Path,
			AbsPath:       client.AbsPath,
			Dev:           client.Dev,
			Mode:          mode,
			OutputAbsDir:  outDir,
			ProjectRoot:   rootAbs,
			FrameworkRoot: filepath.Join(rootAbs, "client", client.Framework),
		})
	}
	return assets, cleanup, nil
}

func writeGraphProbeViteConfig(asset clientbuilder.Asset) error {
	cfg, err := clientbuilder.GenerateViteConfig(asset)
	if err != nil {
		return err
	}
	path := asset.ViteConfigPath()
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("missing graph probe vite config path for client %q", asset.Name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, cfg, 0o644)
}

func compileClientBundleFromBuiltAsset(item clientbuilder.BuiltAsset) goxcompiler.CompileClientBundle {
	bundle := goxcompiler.CompileClientBundle{
		Name:      item.Asset.Name,
		Framework: item.Asset.Framework,
		Entry:     item.Asset.Entry,
		Path:      item.Asset.Path,
		Mode:      item.Asset.Mode,
		EntryURL:  item.EntryURL,
		EntryFile: item.EntryFile,
		Assets:    make([]goxcompiler.CompileClientAsset, 0, len(item.Files)),
		Graph: goxcompiler.CompileClientDependencyGraph{
			Entry:          item.Graph.Entry,
			EntryFile:      item.Graph.EntryFile,
			Framework:      item.Graph.Framework,
			Component:      item.Graph.Component,
			OutputFiles:    append([]string(nil), item.Graph.OutputFiles...),
			ModuleFiles:    append([]string(nil), item.Graph.ModuleFiles...),
			Imports:        append([]string(nil), item.Graph.Imports...),
			DynamicImports: append([]string(nil), item.Graph.DynamicImports...),
			Nodes:          make([]goxcompiler.CompileClientDependencyGraphNode, 0, len(item.Graph.Nodes)),
		},
	}
	for _, node := range item.Graph.Nodes {
		bundle.Graph.Nodes = append(bundle.Graph.Nodes, goxcompiler.CompileClientDependencyGraphNode{
			File:           node.File,
			Imports:        append([]string(nil), node.Imports...),
			DynamicImports: append([]string(nil), node.DynamicImports...),
		})
	}
	for _, file := range item.Files {
		bundle.Assets = append(bundle.Assets, goxcompiler.CompileClientAsset{Kind: file.Kind, URL: file.URL, File: file.File})
	}
	return bundle
}

func attachBundlesToGraphModules(mods map[string]graphModule, result *goxcompiler.CompileResult, rootDir string) {
	if len(result.ClientBundles) == 0 {
		return
	}
	byModule := map[string]map[string]goxcompiler.CompileClientBundle{}
	moduleByClient := map[string]string{}
	for _, mod := range result.Modules {
		modRel := relPath(rootDir, mod.AbsPath)
		for _, client := range mod.Clients {
			moduleByClient[clientKey(client.Framework, client.Path)] = modRel
		}
	}
	for _, bundle := range result.ClientBundles {
		key := clientKey(bundle.Framework, bundle.Path)
		modRel := moduleByClient[key]
		if modRel == "" {
			continue
		}
		if byModule[modRel] == nil {
			byModule[modRel] = map[string]goxcompiler.CompileClientBundle{}
		}
		byModule[modRel][key] = bundle
	}
	for modRel, bundles := range byModule {
		mod := mods[modRel]
		mod.Bundles = bundles
		mods[modRel] = mod
	}
}

func Format(result *goxcompiler.CompileResult, rootDir string, entryRel string, verbose bool) string {
	return FormatWithFilter(result, rootDir, entryRel, verbose, defaultGraphFilter())
}

func FormatWithFilter(result *goxcompiler.CompileResult, rootDir string, entryRel string, verbose bool, filter graphFilter) string {
	if filter.empty() {
		filter = defaultGraphFilter()
	}

	mods := make(map[string]graphModule, len(result.Modules))
	goPackages := make(map[string]graphGoPackage, len(result.GoPackages))
	for _, pkg := range result.GoPackages {
		imports := append([]string(nil), pkg.Imports...)
		sortPackageImports(imports)
		goPackages[pkg.ImportPath] = graphGoPackage{
			ImportPath: pkg.ImportPath,
			Imports:    imports,
		}
	}
	for _, mod := range result.Modules {
		rel := relPath(rootDir, mod.AbsPath)
		imports := make([]string, 0, len(mod.Imports))
		for _, imp := range mod.Imports {
			imports = append(imports, relPath(rootDir, imp))
		}
		sortPathStrings(imports)

		goImports := append([]string(nil), mod.GoImports...)
		sortPackageImports(goImports)

		clients := append([]goxcompiler.CompileClientComponent(nil), mod.Clients...)
		sortClientComponents(clients)

		moduleName := mod.DeclaredModuleName
		if moduleName == "" {
			moduleName = mod.ModuleName
		}
		mods[rel] = graphModule{Path: rel, ModuleName: moduleName, Imports: imports, GoImports: goImports, Clients: clients, IsEntry: mod.IsEntry}
	}

	entry := entryRel
	if result.EntryAbsPath != "" {
		entry = relPath(rootDir, result.EntryAbsPath)
	}
	if _, ok := mods[entry]; !ok {
		mods[entry] = graphModule{Path: entry}
	}
	attachBundlesToGraphModules(mods, result, rootDir)

	var b strings.Builder
	b.WriteString("GRAPH ")
	b.WriteString(entryRel)
	b.WriteString("\n\n")
	if verbose {
		summary := graphSummaryFor(mods, goPackages, entry, filter)
		writeSummary(&b, summary)
		b.WriteString("\n")
	}
	writeTree(&b, mods, goPackages, entry, "", make(map[string]bool), verbose, filter)

	return b.String()
}

type graphSummary struct {
	Go         int
	Gox        int
	Client     int
	ClientDeps int
}

func (s graphSummary) Total() int { return s.Go + s.Gox + s.Client + s.ClientDeps }

func graphSummaryFor(mods map[string]graphModule, goPackages map[string]graphGoPackage, entry string, filter graphFilter) graphSummary {
	var summary graphSummary
	walkGraphSummary(mods, goPackages, entry, filter, make(map[string]bool), make(map[string]bool), &summary)
	return summary
}

func walkGraphSummary(mods map[string]graphModule, goPackages map[string]graphGoPackage, path string, filter graphFilter, seenModules map[string]bool, seenPackages map[string]bool, summary *graphSummary) {
	if seenModules[path] {
		return
	}
	seenModules[path] = true
	items := treeItems(mods[path], filter)
	for _, item := range items {
		switch item.Kind {
		case "package":
			walkGoPackageSummary(goPackages, item.Path, seenPackages, summary)
		case "module":
			summary.Gox++
			walkGraphSummary(mods, goPackages, item.Path, filter, seenModules, seenPackages, summary)
		case "client":
			summary.Client++
			summary.ClientDeps += countClientDependencyItems(item.Children)
		}
	}
	seenModules[path] = false
}

func walkGoPackageSummary(packages map[string]graphGoPackage, importPath string, seen map[string]bool, summary *graphSummary) {
	if seen[importPath] {
		return
	}
	seen[importPath] = true
	defer delete(seen, importPath)
	summary.Go++
	pkg, ok := packages[importPath]
	if !ok {
		return
	}
	for _, imported := range pkg.Imports {
		walkGoPackageSummary(packages, imported, seen, summary)
	}
}

func writeSummary(b *strings.Builder, summary graphSummary) {
	b.WriteString("Summary\n")
	b.WriteString(fmt.Sprintf("  Go      : %d\n", summary.Go))
	b.WriteString(fmt.Sprintf("  GoX     : %d\n", summary.Gox))
	b.WriteString(fmt.Sprintf("  Client  : %d\n", summary.Client))
	if summary.ClientDeps > 0 {
		b.WriteString(fmt.Sprintf("  Deps    : %d\n", summary.ClientDeps))
	}
	b.WriteString("  ----------\n")
	b.WriteString(fmt.Sprintf("  Total   : %d\n", summary.Total()))
}

type treeItem struct {
	Kind      string
	Path      string
	Framework string
	Children  []treeItem
}

func writeTree(b *strings.Builder, mods map[string]graphModule, goPackages map[string]graphGoPackage, path string, prefix string, seen map[string]bool, verbose bool, filter graphFilter) {
	b.WriteString(prefix)
	b.WriteString(formatEntryModuleLabel(mods, path, verbose))
	if seen[path] {
		b.WriteString(" (cycle)\n")
		return
	}
	b.WriteString("\n")

	seen[path] = true
	items := treeItems(mods[path], filter)
	for i, item := range items {
		last := i == len(items)-1
		branch := "├─ "
		nextPrefix := prefix + "│  "
		if last {
			branch = "└─ "
			nextPrefix = prefix + "   "
		}
		separatorPrefix := prefix + "│"
		if last {
			separatorPrefix = ""
		}
		b.WriteString(prefix)
		b.WriteString(branch)
		writeTreeChild(b, mods, goPackages, item, nextPrefix, separatorPrefix, seen, verbose, filter)
	}
	seen[path] = false
}

func writeTreeChild(b *strings.Builder, mods map[string]graphModule, goPackages map[string]graphGoPackage, item treeItem, nextPrefix string, separatorPrefix string, seen map[string]bool, verbose bool, filter graphFilter) {
	switch item.Kind {
	case "package":
		b.WriteString(formatPackageLabel(item.Path, verbose))
		key := "package\x00" + item.Path
		if seen[key] {
			b.WriteString(" (cycle)\n")
			return
		}
		b.WriteString("\n")
		pkg, ok := goPackages[item.Path]
		if !ok {
			return
		}
		seen[key] = true
		children := goPackageTreeItems(pkg)
		for i, child := range children {
			last := i == len(children)-1
			branch := "├─ "
			childPrefix := nextPrefix + "│  "
			if last {
				branch = "└─ "
				childPrefix = nextPrefix + "   "
			}
			childSeparatorPrefix := nextPrefix + "│"
			if last {
				childSeparatorPrefix = ""
			}
			b.WriteString(nextPrefix)
			b.WriteString(branch)
			writeTreeChild(b, mods, goPackages, child, childPrefix, childSeparatorPrefix, seen, verbose, filter)
		}
		delete(seen, key)
		if len(children) > 0 && separatorPrefix != "" {
			b.WriteString(separatorPrefix)
			b.WriteString("\n")
		}
		return
	case "client":
		b.WriteString(formatClientLabel(item.Framework, item.Path, verbose))
		b.WriteString("\n")
		writeClientDependencyChildren(b, item.Children, nextPrefix, verbose)
		return
	case "client-dep":
		b.WriteString(formatClientDependencyLabel(item.Path, verbose))
		b.WriteString("\n")
		return
	}

	path := item.Path
	b.WriteString(formatChildModuleLabel(mods, path, verbose))
	if seen[path] {
		b.WriteString(" (cycle)\n")
		return
	}
	b.WriteString("\n")

	seen[path] = true
	items := treeItems(mods[path], filter)
	for i, child := range items {
		last := i == len(items)-1
		branch := "├─ "
		childPrefix := nextPrefix + "│  "
		if last {
			branch = "└─ "
			childPrefix = nextPrefix + "   "
		}
		childSeparatorPrefix := nextPrefix + "│"
		if last {
			childSeparatorPrefix = ""
		}
		b.WriteString(nextPrefix)
		b.WriteString(branch)
		writeTreeChild(b, mods, goPackages, child, childPrefix, childSeparatorPrefix, seen, verbose, filter)
	}
	if len(items) > 0 && separatorPrefix != "" {
		b.WriteString(separatorPrefix)
		b.WriteString("\n")
	}
	seen[path] = false
}

func goPackageTreeItems(pkg graphGoPackage) []treeItem {
	items := make([]treeItem, 0, len(pkg.Imports))
	for _, imported := range pkg.Imports {
		items = append(items, treeItem{Kind: "package", Path: imported})
	}
	return items
}

func treeItems(mod graphModule, filter graphFilter) []treeItem {
	items := make([]treeItem, 0, len(mod.Imports)+len(mod.GoImports)+len(mod.Clients))
	if filter.Go {
		goImports := append([]string(nil), mod.GoImports...)
		sortPackageImports(goImports)
		for _, imp := range goImports {
			items = append(items, treeItem{Kind: "package", Path: imp})
		}
	}

	if filter.Gox {
		imports := append([]string(nil), mod.Imports...)
		sortPathStrings(imports)
		for _, imp := range imports {
			items = append(items, treeItem{Kind: "module", Path: imp})
		}
	}

	if filter.Client {
		clients := append([]goxcompiler.CompileClientComponent(nil), mod.Clients...)
		sortClientComponents(clients)
		for _, client := range clients {
			children := []treeItem(nil)
			if filter.ClientDeps {
				children = clientDependencyTreeItems(mod.Bundles, client)
			}
			items = append(items, treeItem{Kind: "client", Path: client.Path, Framework: client.Framework, Children: children})
		}
	}

	return items
}

func writeClientDependencyChildren(b *strings.Builder, items []treeItem, prefix string, verbose bool) {
	writeClientDependencyChildrenSeen(b, items, prefix, verbose, make(map[string]bool))
}

func writeClientDependencyChildrenSeen(b *strings.Builder, items []treeItem, prefix string, verbose bool, seen map[string]bool) {
	for i, child := range items {
		last := i == len(items)-1
		branch := "├─ "
		nextPrefix := prefix + "│  "
		separatorPrefix := prefix + "│"
		if last {
			branch = "└─ "
			nextPrefix = prefix + "   "
			separatorPrefix = ""
		}
		b.WriteString(prefix)
		b.WriteString(branch)
		b.WriteString(formatClientDependencyLabel(child.Path, verbose))
		if seen[child.Path] {
			b.WriteString(" (cycle)\n")
			continue
		}
		b.WriteString("\n")
		if len(child.Children) == 0 {
			continue
		}
		seen[child.Path] = true
		writeClientDependencyChildrenSeen(b, child.Children, nextPrefix, verbose, seen)
		seen[child.Path] = false
		if separatorPrefix != "" {
			b.WriteString(separatorPrefix)
			b.WriteString("\n")
		}
	}
}

func clientDependencyTreeItems(bundles map[string]goxcompiler.CompileClientBundle, client goxcompiler.CompileClientComponent) []treeItem {
	if len(bundles) == 0 {
		return nil
	}
	bundle, ok := bundles[clientKey(client.Framework, client.Path)]
	if !ok {
		return nil
	}
	return projectClientDependencyTree(bundle.Graph, client)
}

func projectClientDependencyTree(graph goxcompiler.CompileClientDependencyGraph, client goxcompiler.CompileClientComponent) []treeItem {
	nodes := make(map[string]goxcompiler.CompileClientDependencyGraphNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		rel := clientDependencyRelPath(node.File)
		if rel == "" || isNodeModulePath(rel) {
			continue
		}
		node.File = rel
		nodes[rel] = normalizeClientDependencyGraphNode(node)
	}
	entry := filepath.ToSlash(client.Path)
	if entry == "" {
		entry = clientDependencyRelPath(graph.EntryFile)
	}
	entryNode, ok := nodes[entry]
	if !ok {
		return projectClientDependencyFlatTree(graph, client)
	}
	return clientDependencyChildrenFromNode(entryNode, nodes, entry, make(map[string]bool))
}

func normalizeClientDependencyGraphNode(node goxcompiler.CompileClientDependencyGraphNode) goxcompiler.CompileClientDependencyGraphNode {
	node.File = clientDependencyRelPath(node.File)
	node.Imports = normalizeClientDependencyImports(node.Imports)
	node.DynamicImports = normalizeClientDependencyImports(node.DynamicImports)
	return node
}

func normalizeClientDependencyImports(imports []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(imports))
	for _, imp := range imports {
		rel := clientDependencyRelPath(imp)
		if rel == "" || isNodeModulePath(rel) {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	sortPathStrings(out)
	return out
}

func clientDependencyChildrenFromNode(node goxcompiler.CompileClientDependencyGraphNode, nodes map[string]goxcompiler.CompileClientDependencyGraphNode, entry string, seen map[string]bool) []treeItem {
	childrenPaths := append([]string(nil), node.Imports...)
	childrenPaths = append(childrenPaths, node.DynamicImports...)
	childrenPaths = uniqueClientDependencyPaths(childrenPaths, entry)
	items := make([]treeItem, 0, len(childrenPaths))
	for _, childPath := range childrenPaths {
		item := treeItem{Kind: "client-dep", Path: childPath}
		if childNode, ok := nodes[childPath]; ok && !seen[childPath] {
			seen[childPath] = true
			item.Children = clientDependencyChildrenFromNode(childNode, nodes, entry, seen)
			seen[childPath] = false
		}
		items = append(items, item)
	}
	return items
}

func uniqueClientDependencyPaths(paths []string, entry string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = clientDependencyRelPath(path)
		if path == "" || isNodeModulePath(path) || path == filepath.ToSlash(entry) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sortPathStrings(out)
	return out
}

func projectClientDependencyFlatTree(graph goxcompiler.CompileClientDependencyGraph, client goxcompiler.CompileClientComponent) []treeItem {
	seen := map[string]struct{}{}
	add := func(path string) {
		rel := clientDependencyRelPath(path)
		if rel == "" || isNodeModulePath(rel) {
			return
		}
		if rel == filepath.ToSlash(client.Path) {
			return
		}
		seen[rel] = struct{}{}
	}
	for _, node := range graph.Nodes {
		add(node.File)
		for _, imp := range node.Imports {
			add(imp)
		}
		for _, imp := range node.DynamicImports {
			add(imp)
		}
	}
	for _, file := range graph.ModuleFiles {
		add(file)
	}
	out := make([]string, 0, len(seen))
	for file := range seen {
		out = append(out, file)
	}
	sortPathStrings(out)
	items := make([]treeItem, 0, len(out))
	for _, dep := range out {
		items = append(items, treeItem{Kind: "client-dep", Path: dep})
	}
	return items
}

func countClientDependencyItems(items []treeItem) int {
	seen := map[string]struct{}{}
	var walk func([]treeItem)
	walk = func(children []treeItem) {
		for _, child := range children {
			if child.Path != "" {
				seen[child.Path] = struct{}{}
			}
			walk(child.Children)
		}
	}
	walk(items)
	return len(seen)
}

func clientDependencyRelPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || strings.HasPrefix(path, "\x00") {
		return ""
	}
	marker := "/client/"
	if idx := strings.Index(path, marker); idx >= 0 {
		return path[idx+1:]
	}
	if strings.HasPrefix(path, "client/") {
		return path
	}
	return path
}

func isNodeModulePath(path string) bool {
	path = filepath.ToSlash(path)
	return strings.Contains(path, "/node_modules/") || strings.Contains(path, "node_modules/") || strings.Contains(path, "/.pnpm/") || strings.Contains(path, ".pnpm/")
}

func clientKey(framework string, path string) string {
	return strings.ToLower(strings.TrimSpace(framework)) + "\x00" + filepath.ToSlash(strings.TrimSpace(path))
}

func sortPackageImports(imports []string) { sortPathStrings(imports) }

func sortPathStrings(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		if len(paths[i]) == len(paths[j]) {
			return paths[i] < paths[j]
		}
		return len(paths[i]) < len(paths[j])
	})
}

func sortClientComponents(clients []goxcompiler.CompileClientComponent) {
	sort.SliceStable(clients, func(i, j int) bool {
		if len(clients[i].Path) == len(clients[j].Path) {
			if clients[i].Path == clients[j].Path {
				if clients[i].Framework == clients[j].Framework {
					return clients[i].Name < clients[j].Name
				}
				return clients[i].Framework < clients[j].Framework
			}
			return clients[i].Path < clients[j].Path
		}
		return len(clients[i].Path) < len(clients[j].Path)
	})
}

func formatEntryModuleLabel(mods map[string]graphModule, path string, verbose bool) string {
	if !verbose {
		return path
	}
	mod, ok := mods[path]
	if !ok || mod.ModuleName == "" {
		return path
	}
	return fmt.Sprintf("%s  module %s", path, mod.ModuleName)
}

func formatChildModuleLabel(mods map[string]graphModule, path string, verbose bool) string {
	if !verbose {
		return fmt.Sprintf("%q", path)
	}
	mod, ok := mods[path]
	if !ok {
		return fmt.Sprintf("module   %s", path)
	}
	name := mod.ModuleName
	if name == "" {
		name = path
	}
	return fmt.Sprintf("module   %-10s %q", name, path)
}

func formatPackageLabel(path string, verbose bool) string {
	if !verbose {
		return fmt.Sprintf("%q", path)
	}
	return fmt.Sprintf("package  %q", path)
}

func formatClientLabel(framework string, path string, verbose bool) string {
	if !verbose {
		return fmt.Sprintf("%q", path)
	}
	return fmt.Sprintf("client   %-7s %q", framework, path)
}

func formatClientDependencyLabel(path string, verbose bool) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if ext == "" {
		ext = "file"
	}
	if !verbose {
		return fmt.Sprintf("%q", path)
	}
	return fmt.Sprintf("%-7s %q", "."+ext, path)
}

func relPath(rootDir string, path string) string {
	if path == "" {
		return ""
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
