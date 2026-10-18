package serverruntime

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gox-lang/server/internal/clientbuilder"
	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/projecttoolchain"
	"github.com/gox-lang/server/internal/watcher"
)

func (r *Runtime) handleClientHotReloadEvent(event watcher.Event) {
	if shouldIgnoreClientHotReloadEvent(event) {
		return
	}
	if r.hotReloadEngine == nil {
		logs.Warm("client hotReload engine is not initialized; ignoring %s", event.Rel)
		return
	}
	count, err := r.hotReloadEngine.Handle(event)
	if err != nil {
		logs.Warm("client hotReload failed after %s: %v", event.Rel, err)
		return
	}
	if count == 0 {
		logs.Warm("client hotReload has no dependents for %s", event.Rel)
		return
	}
	logs.Info("client hotReload completed after %s: entries=%d", event.Rel, count)
}

func shouldIgnoreClientHotReloadEvent(event watcher.Event) bool {
	if event.IsDir || event.Op == watcher.OpChmod {
		return true
	}
	rel := filepath.ToSlash(strings.TrimSpace(event.Rel))
	if rel == "" {
		return true
	}
	if strings.Contains(rel, "/node_modules/") || strings.Contains(rel, "/.pnpm/") {
		return true
	}
	if strings.HasPrefix(rel, ".build/") {
		return true
	}
	if strings.HasSuffix(rel, ".gox") {
		return true
	}
	return false
}

func (r *Runtime) reloadClientEntry(entry hotreload.ReloadEntry, event watcher.Event) error {
	state := r.state()
	if state == nil || state.ClientAssets == nil {
		return nil
	}
	bundle, ok := state.ClientAssets.BundleByEntryFile(entry.EntryAbsPath)
	if !ok {
		return nil
	}

	mode := strings.TrimSpace(bundle.Mode)
	if mode == "" {
		mode = clientbuilder.ModeProd
	}
	groupBundles := state.ClientAssets.BundlesForFrameworkMode(bundle.Framework, mode)
	if len(groupBundles) == 0 {
		groupBundles = []goxcompiler.CompileClientBundle{bundle}
	}

	assets := make([]clientbuilder.Asset, 0, len(groupBundles))
	for _, item := range groupBundles {
		asset := r.clientBuilderAssetFromBundle(item)
		if strings.TrimSpace(asset.OutputAbsDir) == "" || strings.TrimSpace(asset.FrameworkRoot) == "" {
			continue
		}
		assets = append(assets, asset)
	}
	if len(assets) == 0 {
		return nil
	}

	nodePaths, pnpmPaths, err := projecttoolchain.Ensure(r.ProjectRoot)
	if err != nil {
		return err
	}

	built, err := clientbuilder.BuildViteAssets(nodePaths, pnpmPaths, assets)
	if err != nil {
		return err
	}
	compiled := make([]goxcompiler.CompileClientBundle, 0, len(built))
	for _, item := range built {
		compiled = append(compiled, compileClientBundleFromBuiltAsset(item))
	}

	r.requestMu.Lock()
	old := r.state()
	newState := *old
	if newState.ClientAssets == nil {
		newState.ClientAssets = NewClientAssetPolicy(nil)
	} else {
		newState.ClientAssets = newState.ClientAssets.Clone()
	}
	oldFiles := make([]string, 0)
	for _, item := range compiled {
		entryFile := item.Graph.EntryFile
		if strings.TrimSpace(entryFile) == "" {
			entryFile = item.EntryFile
		}
		oldFiles = append(oldFiles, newState.ClientAssets.FilesForEntry(entryFile)...)
	}
	for _, item := range compiled {
		newState.ClientAssets.ReplaceBundle(item)
	}
	precompressClientAssets(r.ProjectRoot, newState.Config.Static.Compression, newState.ClientAssets)
	r.runtimeState.Store(&newState)
	r.requestMu.Unlock()
	r.scheduleDeleteUnreferencedClientAssetFiles(oldFiles)

	if r.hotReload != nil {
		r.hotReload.RegisterClientBundles(compiled)
	}

	logs.Info("client framework hotReload rebuilt: %s %s entries=%d changed=%s", bundle.Framework, mode, len(compiled), event.Rel)
	return nil
}

const clientAssetRetireGracePeriod = 45 * time.Second

func (r *Runtime) scheduleDeleteUnreferencedClientAssetFiles(files []string) {
	if r == nil || len(files) == 0 {
		return
	}
	retired := append([]string(nil), files...)
	time.AfterFunc(clientAssetRetireGracePeriod, func() {
		state := r.state()
		if state == nil {
			return
		}
		deleteUnreferencedClientAssetFiles(retired, state.ClientAssets)
	})
}

func (r *Runtime) clientBuilderAssetFromBundle(bundle goxcompiler.CompileClientBundle) clientbuilder.Asset {
	projectRoot := r.ProjectRoot
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = "."
	}
	rootAbs, err := filepath.Abs(projectRoot)
	if err != nil {
		rootAbs = projectRoot
	}
	mode := strings.TrimSpace(bundle.Mode)
	if mode == "" {
		mode = clientbuilder.ModeProd
	}
	outputAbsDir := ""
	if strings.TrimSpace(bundle.EntryFile) != "" {
		outputAbsDir = filepath.Dir(bundle.EntryFile)
	}
	frameworkRoot := filepath.Join(rootAbs, "client", bundle.Framework)
	entryFile := bundle.Graph.EntryFile
	if strings.TrimSpace(entryFile) == "" {
		entryFile = bundle.EntryFile
	}
	return clientbuilder.Asset{
		Name:          bundle.Name,
		Framework:     bundle.Framework,
		Entry:         bundle.Entry,
		Path:          bundle.Path,
		AbsPath:       entryFile,
		Dev:           strings.EqualFold(mode, clientbuilder.ModeDev),
		Mode:          mode,
		OutputAbsDir:  outputAbsDir,
		ProjectRoot:   rootAbs,
		FrameworkRoot: frameworkRoot,
	}
}

func compileClientHotReloadGraphNodes(nodes []clientbuilder.ClientDependencyGraphNode) []goxcompiler.CompileClientDependencyGraphNode {
	if len(nodes) == 0 {
		return nil
	}
	out := make([]goxcompiler.CompileClientDependencyGraphNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, goxcompiler.CompileClientDependencyGraphNode{
			File:           node.File,
			Imports:        append([]string(nil), node.Imports...),
			DynamicImports: append([]string(nil), node.DynamicImports...),
		})
	}
	return out
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
			Nodes:          compileClientHotReloadGraphNodes(item.Graph.Nodes),
		},
	}
	for _, file := range item.Files {
		if strings.TrimSpace(file.URL) == "" || strings.TrimSpace(file.File) == "" {
			continue
		}
		bundle.Assets = append(bundle.Assets, goxcompiler.CompileClientAsset{Kind: file.Kind, URL: file.URL, File: file.File})
	}
	return bundle
}

func deleteUnreferencedClientAssetFiles(files []string, policy *ClientAssetPolicy) {
	if len(files) == 0 {
		return
	}
	referenced := policy.ReferencedFiles()
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		clean := filepath.Clean(file)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		if _, ok := referenced[clean]; ok {
			continue
		}
		// Stable production framework vendors are shared by every component of
		// the framework. They are built and retired as a framework unit, never as
		// part of an individual entry's delayed hot-reload cleanup.
		if isSharedFrameworkVendorOutput(clean) {
			continue
		}
		deleteClientAssetFile(clean)
	}
}

func isSharedFrameworkVendorOutput(file string) bool {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(file)))
	marker := "/.build/client/"
	idx := strings.LastIndex(clean, marker)
	if idx < 0 {
		return false
	}
	rel := strings.TrimPrefix(clean[idx+len(marker):], "/")
	parts := strings.Split(rel, "/")
	if len(parts) < 4 || strings.TrimSpace(parts[0]) == "" || parts[1] != clientbuilder.ModeProd {
		return false
	}
	// Vendor facades live in prod/vendor/. Shared chunks emitted by the vendor
	// build live in prod/chunks/. Component chunks live below their build
	// identity directory, so they remain eligible for normal retirement.
	return parts[2] == "vendor" || parts[2] == "chunks"
}

func deleteClientAssetFile(file string) {
	if strings.TrimSpace(file) == "" {
		return
	}
	_ = os.Remove(file)
	for _, suffix := range []string{".l1.gzip", ".l2.gzip", ".l3.gzip", ".l4.gzip", ".l5.gzip", ".l6.gzip", ".l7.gzip", ".l8.gzip", ".l9.gzip", ".l1.deflate", ".l2.deflate", ".l3.deflate", ".l4.deflate", ".l5.deflate", ".l6.deflate", ".l7.deflate", ".l8.deflate", ".l9.deflate", ".l1.br", ".l2.br", ".l3.br", ".l4.br", ".l5.br", ".l6.br", ".l7.br", ".l8.br", ".l9.br", ".l1.zstd", ".l2.zstd", ".l3.zstd", ".l4.zstd", ".l5.zstd", ".l6.zstd", ".l7.zstd", ".l8.zstd", ".l9.zstd"} {
		_ = os.Remove(file + suffix)
	}
}
