package serverruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"plugin"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxsource"
	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/toolchain"
	"github.com/gox-lang/server/internal/watcher"

	"github.com/gox-lang/gox"
)

type GoxRouteState string

const (
	GoxRouteCold      GoxRouteState = "cold"
	GoxRouteCompiling GoxRouteState = "compiling"
	GoxRouteCompiled  GoxRouteState = "compiled"
	GoxRouteBuilding  GoxRouteState = "building"
	GoxRouteBuilt     GoxRouteState = "built"
	GoxRouteLoading   GoxRouteState = "loading"
	GoxRouteLoaded    GoxRouteState = "loaded"
	GoxRouteFailed    GoxRouteState = "failed"
)

type GoxRouteRuntime struct {
	mu sync.Mutex

	RoutePath     string
	AbsPath       string
	RelPath       string
	ModTime       time.Time
	Size          int64
	BuildDir      string
	BuildVersion  string
	GeneratedPath string
	PluginPath    string
	GoBinary      string
	State         GoxRouteState
	LastError     string

	GeneratedExists bool
	GeneratedSize   int64
	PluginExists    bool
	PluginSize      int64

	Plugin           *plugin.Plugin
	HTTPHandlers     map[string]func(*gox.Context)
	HTTPContentTypes map[string]string
	Actions          map[string]gox.ActionHandler
	WebSocketHandler gox.WebSocketHandler
	ChannelHandler   gox.ChannelHandler

	Dependencies  []GoxRouteDependency
	CompileResult *goxcompiler.CompileResult
	WarmupHits    atomic.Int64
}

type GoxRouteDependency struct {
	AbsPath string
	Size    int64
	ModTime time.Time
	IsEntry bool
}

type goxRouteFile struct {
	routePath string
	absPath   string
	relPath   string
	modTime   time.Time
	size      int64
}

func (r *Runtime) gox(ctx *gox.Context, req *http.Request, trace *requestPipelineTrace) bool {
	if ctx.Request.Route.IsStatic() {
		return false
	}

	devConfig := r.stateFor(ctx).Config.Dev
	collectTrace := trace != nil && trace.enabled && trace.deep && devGoxTraceDeepEnabled(devConfig)
	var stages []requestPipelineStage
	addDeepStage := func(name string, started time.Time) {
		if !collectTrace || started.IsZero() {
			return
		}
		stages = append(stages, requestPipelineStage{name: name, duration: time.Since(started)})
	}
	nowDeep := func() time.Time {
		if !collectTrace {
			return time.Time{}
		}
		return time.Now()
	}
	var addResolveStage func(string, time.Time)
	if collectTrace {
		addResolveStage = addDeepStage
	}

	stageStart := nowDeep()
	route, ok, err := r.getOrCreateGoxRoute(ctx.Request.Route.Target(), addResolveStage)
	addDeepStage("resolve.total", stageStart)
	if err != nil {
		r.renderGoxError(ctx, req, trace, asGoxError(err, gox.GoxErrorResolve, nil, ctx, "failed to resolve gox route"))
		recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, nil)
		return true
	}
	if !ok {
		recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, nil)
		return false
	}

	stageStart = nowDeep()
	if err := r.ensureGoxRouteReady(route); err != nil {
		addDeepStage("ensure.ready", stageStart)
		r.renderGoxError(ctx, req, trace, asGoxError(err, gox.GoxErrorLoad, route, ctx, "failed to prepare gox route"))
		recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, nil)
		return true
	}
	addDeepStage("ensure.ready", stageStart)

	// The initial request may have compiled and published client assets while
	// this context was still bound to the previous runtime snapshot. Refresh it
	// before the handler renders any client islands.
	r.refreshRequestClientAssets(ctx)

	stageStart = nowDeep()
	if err := r.attachGoxWriter(ctx, collectTrace); err != nil {
		addDeepStage("writer.new", stageStart)
		r.renderGoxError(ctx, req, trace, asGoxError(err, gox.GoxErrorWriter, route, ctx, "failed to attach gox writer"))
		recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, nil)
		return true
	}
	addDeepStage("writer.new", stageStart)

	writer, _ := ctx.Writer.(*GoxWriter)

	dispatchStart := nowDeep()
	stageStart = nowDeep()
	if ctx.Request.Route.IsAction() {
		if req == nil || req.Method != http.MethodPost {
			stageStart = nowDeep()
			r.renderGoxError(ctx, req, trace, newGoxDispatchError(route, ctx, "method not allowed", http.StatusMethodNotAllowed))
			addDeepStage("response.405", stageStart)
			addDeepStage("dispatch", dispatchStart)
			recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, writer)
			return true
		}

		handler := route.Actions[ctx.Request.Route.Method()]
		addDeepStage("handler.lookup", stageStart)
		if handler == nil {
			ctx.Response.DoneWith(405)
			stageStart = nowDeep()
			r.renderGoxError(ctx, req, trace, newGoxDispatchError(route, ctx, "method not allowed", http.StatusMethodNotAllowed))
			addDeepStage("response.405", stageStart)
			addDeepStage("dispatch", dispatchStart)
			recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, writer)
			return true
		}

		stageStart = nowDeep()
		if err := executeGoxHandler(ctx, route, handler, trace); err != nil {
			addDeepStage("handler", stageStart)
			r.renderGoxError(ctx, req, trace, err)
			addDeepStage("dispatch", dispatchStart)
			recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, writer)
			return true
		}
		addDeepStage("handler", stageStart)
		r.recordDynamicWarmup(route)
		addDeepStage("dispatch", dispatchStart)
		recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, writer)
		return true
	}

	stageStart = nowDeep()
	handler := route.HTTPHandlers[ctx.Request.Route.Method()]
	addDeepStage("handler.lookup", stageStart)
	if handler == nil {
		stageStart = nowDeep()
		r.renderGoxError(ctx, req, trace, newGoxDispatchError(route, ctx, "method not allowed", http.StatusMethodNotAllowed))
		addDeepStage("response.405", stageStart)
		addDeepStage("dispatch", dispatchStart)
		recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, writer)
		return true
	}

	applyGoxHTTPContentType(ctx, route, ctx.Request.Route.Method())

	stageStart = nowDeep()
	if err := executeGoxHandler(ctx, route, handler, trace); err != nil {
		addDeepStage("handler", stageStart)
		r.renderGoxError(ctx, req, trace, err)
		addDeepStage("dispatch", dispatchStart)
		recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, writer)
		return true
	}
	addDeepStage("handler", stageStart)
	r.recordDynamicWarmup(route)
	addDeepStage("dispatch", dispatchStart)
	recordGoxTrace(trace, collectTrace, ctx.Request.Route.Target(), ctx.Request.Route.Method(), ctx.Request.Route.IsAction(), stages, writer)
	return true
}

func (r *Runtime) recordDynamicWarmup(route *GoxRouteRuntime) {
	if route == nil || r == nil || r.dynamicWarmup == nil {
		return
	}
	route.WarmupHits.Add(1)
}

func (r *Runtime) snapshotAndResetDynamicWarmupCounts() dynamicWarmupSnapshot {
	if r == nil {
		return dynamicWarmupSnapshot{}
	}

	r.goxMu.RLock()
	pages := make([]dynamicWarmupCachePath, 0, len(r.goxRoutes))
	for path, route := range r.goxRoutes {
		if route == nil {
			continue
		}
		count := route.WarmupHits.Swap(0)
		if count <= 0 {
			continue
		}
		if path == "" {
			path = route.RoutePath
		}
		pages = append(pages, dynamicWarmupCachePath{Path: path, Count: count})
	}
	r.goxMu.RUnlock()

	r.channelMu.RLock()
	definitions := make([]*channelDefinitionRuntime, 0, len(r.channelDefinitions))
	for _, definition := range r.channelDefinitions {
		if definition != nil {
			definitions = append(definitions, definition)
		}
	}
	r.channelMu.RUnlock()
	channels := make([]dynamicWarmupCachePath, 0, len(definitions))
	for _, definition := range definitions {
		definition.mu.Lock()
		route := definition.route
		definition.mu.Unlock()
		if route == nil {
			continue
		}
		count := route.WarmupHits.Swap(0)
		if count <= 0 {
			continue
		}
		channels = append(channels, dynamicWarmupCachePath{Path: route.RoutePath, Count: count})
	}
	return dynamicWarmupSnapshot{Pages: pages, Channels: channels}
}

func (r *Runtime) warmupDynamicRoutesAtStartup() {
	if r == nil || r.dynamicWarmup == nil {
		return
	}

	selection := r.dynamicWarmup.WarmupPaths()
	if len(selection.Pages) == 0 && len(selection.Channels) == 0 {
		return
	}

	loadedPages := 0
	loadedChannels := 0
	for _, path := range selection.Pages {
		route, ok, err := r.getOrCreateGoxRoute(path, nil)
		if err != nil || !ok || route == nil {
			continue
		}
		if err := r.ensureGoxRouteReady(route); err != nil {
			continue
		}
		loadedPages++
	}
	for _, path := range selection.Channels {
		name := strings.TrimPrefix(path, channelRoutePrefix)
		r.channelMu.RLock()
		definition := r.channelDefinitions[name]
		r.channelMu.RUnlock()
		if definition == nil {
			continue
		}
		if _, _, err := r.ensureChannelDefinition(definition); err != nil {
			continue
		}
		loadedChannels++
	}
	if loadedPages+loadedChannels > 0 {
		logs.Info("[cache] warmup loaded: pages=%d/%d channels=%d/%d", loadedPages, len(selection.Pages), loadedChannels, len(selection.Channels))
	}
}

func applyGoxHTTPContentType(ctx *gox.Context, route *GoxRouteRuntime, method string) {
	if ctx == nil || ctx.Response == nil || route == nil || method == "" {
		return
	}
	contentType := route.HTTPContentTypes[method]
	if current, _ := ctx.Response.Headers.Get("Content-Type"); contentType == "" || current != "" {
		return
	}
	ctx.Response.Headers.Set("Content-Type", contentType)
}

func executeGoxHandler(ctx *gox.Context, route *GoxRouteRuntime, handler func(*gox.Context), trace *requestPipelineTrace) (err *GoxError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if flow, ok := recovered.(gox.FlowControl); ok {
				err = executeResponseFlow(ctx, route, flow, trace)
				return
			}
			err = newGoxPanicRuntimeError(route, ctx, recovered)
		}
	}()

	if sample := activeBenchmarkHandlerAllocSample(); sample != nil && (ctx == nil || !ctx.Request.Route.IsAction()) {
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		defer func() {
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			if after.Mallocs >= before.Mallocs && after.TotalAlloc >= before.TotalAlloc {
				sample.add(after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc)
			}
		}()
	}

	handler(ctx)
	return nil
}

func recordGoxTrace(trace *requestPipelineTrace, enabled bool, route string, method string, isAction bool, stages []requestPipelineStage, writer *GoxWriter) {
	if trace == nil || !enabled {
		return
	}

	trace.addDeepField("gox.route", route)
	trace.addDeepField("gox.method", method)
	trace.addDeepField("gox.isAction", strconv.FormatBool(isAction))

	for _, stage := range stages {
		trace.addDeepDuration("gox."+stage.name, stage.duration)
	}

	if writer != nil {
		stageStart := time.Now()
		_ = writer.Stats()
		trace.addDeepDuration("gox.stats.collect", time.Since(stageStart))
	}
}

func (r *Runtime) refreshRequestClientAssets(ctx *gox.Context) {
	if r == nil || ctx == nil {
		return
	}
	// Client bundles may be registered by this very request during cold compilation.
	clientAssets := r.state().ClientAssets
	gox.BindClientIslandRuntime(ctx, clientAssets)
	if response, ok := runtimeResponse(ctx); ok {
		response.SetClientAssets(clientAssets)
	}
}

func (r *Runtime) attachGoxWriter(ctx *gox.Context, traceDeep bool) error {
	if _, ok := runtimeResponse(ctx); !ok {
		return newResponseRuntimeError("invalid-response-runtime-type", "response has invalid runtime type")
	}
	writer, ok := ctx.Writer.(*GoxWriter)
	if !ok || writer == nil {
		return newResponseRuntimeError("invalid-response-writer-type", "response writer has invalid runtime type")
	}
	return writer.ConfigureRoute(ctx.Request.Route.IsAction(), traceDeep)
}

func (r *Runtime) ensureGoxRouteReady(route *GoxRouteRuntime) error {
	if route == nil {
		return newRuntimeConfigError("gox-route-nil", "gox route is nil", nil)
	}

	route.mu.Lock()
	ready := route.State == GoxRouteLoaded && (route.Plugin != nil || len(route.HTTPHandlers) > 0 || len(route.Actions) > 0 || route.WebSocketHandler != nil || route.ChannelHandler != nil)
	route.mu.Unlock()
	if ready {
		return nil
	}

	key := route.RoutePath

	_, err := r.getGoxBuildGroup().Do(key, func() (any, error) {
		route.mu.Lock()
		ready := route.State == GoxRouteLoaded && (route.Plugin != nil || len(route.HTTPHandlers) > 0 || len(route.Actions) > 0 || route.WebSocketHandler != nil || route.ChannelHandler != nil)
		route.mu.Unlock()
		if ready {
			return route, nil
		}
		if err := r.rebuildGoxRouteTransactional(route); err != nil {
			route.mu.Lock()
			route.State = GoxRouteFailed
			route.LastError = err.Error()
			route.mu.Unlock()
			return nil, err
		}
		return route, nil
	})
	return err
}

func (r *Runtime) getGoxBuildGroup() *staticSingleflightGroup {
	r.goxMu.Lock()
	defer r.goxMu.Unlock()

	if r.goxBuildGroup == nil {
		r.goxBuildGroup = newStaticSingleflightGroup()
	}

	return r.goxBuildGroup
}

func (r *Runtime) compileGoxRouteLocked(route *GoxRouteRuntime) error {
	if route.GeneratedExists && len(route.Dependencies) > 0 && !goxRouteNeedsReloadLocked(route) {
		route.State = GoxRouteCompiled
		return nil
	}

	route.State = GoxRouteCompiling
	route.LastError = ""

	if err := os.MkdirAll(route.BuildDir, 0755); err != nil {
		return newGoxError(gox.GoxErrorCompile, route, nil, "failed to create gox build dir", err)
	}

	result, err := goxcompiler.Compile(route.AbsPath, route.GeneratedPath)

	if err != nil {
		return newGoxError(gox.GoxErrorCompile, route, nil, "failed to compile gox route", err)
	}
	if result == nil || !result.Written {
		return newGoxError(gox.GoxErrorCompile, route, nil, "compile did not write output", errors.New("generated path: "+route.GeneratedPath))
	}

	deps := goxRouteDependenciesFromCompileResult(result)
	if len(deps) == 0 {
		return newGoxError(gox.GoxErrorCompile, route, nil, "compile returned no dependencies", errors.New("entry path: "+route.AbsPath))
	}

	if err := r.applyGoxDependencyBuildVersionLocked(route, deps, result); err != nil {
		return err
	}

	info, err := os.Stat(route.GeneratedPath)
	if err != nil {
		return newGoxError(gox.GoxErrorCompile, route, nil, "failed to stat generated go file", err)
	}
	if info.IsDir() {
		return newGoxError(gox.GoxErrorCompile, route, nil, "generated go path is a directory", errors.New("generated path: "+route.GeneratedPath))
	}

	route.Dependencies = deps
	route.CompileResult = result
	reloadKind := hotreload.ReloadPage
	if route.RoutePath == goxErrorRoutePath {
		reloadKind = hotreload.ReloadError
	}
	r.registerHotReloadCompileResult(reloadKind, result)
	r.registerClientAssets(result)
	route.GeneratedExists = true
	route.GeneratedSize = info.Size()
	route.State = GoxRouteCompiled
	return nil
}

// GOX runtime plugins do not require the external symbol table or DWARF data
// to be loaded with plugin.Open. Omitting both avoids linker work on every hot
// reload while preserving Go runtime stack metadata and exported plugin symbols.
const goxPluginLinkerFlags = "-s -w"

func goxPluginBuildArgs(pluginPath, generatedPath string) []string {
	return toolchain.PluginBuildArgs([]string{
		"build",
		"-buildmode=plugin",
		"-ldflags=" + goxPluginLinkerFlags,
		"-o", pluginPath,
		generatedPath,
	})
}

func (r *Runtime) buildGoxRoutePluginLocked(route *GoxRouteRuntime) error {
	if route.PluginExists {
		route.State = GoxRouteBuilt
		return nil
	}

	route.State = GoxRouteBuilding
	route.LastError = ""

	projectRoot := r.projectRoot()
	if projectRoot == "" {
		return newGoxError(gox.GoxErrorBuild, route, nil, "project root is empty", nil)
	}

	cmd := exec.Command(route.GoBinary, goxPluginBuildArgs(route.PluginPath, route.GeneratedPath)...)
	cmd.Dir = projectRoot
	cmd.Env = os.Environ()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return newGoxError(gox.GoxErrorBuild, route, nil, "page build failed", errors.New(msg))
	}

	info, err := os.Stat(route.PluginPath)
	if err != nil {
		return newGoxError(gox.GoxErrorBuild, route, nil, "page build output is missing", err)
	}
	if info.IsDir() {
		return newGoxError(gox.GoxErrorBuild, route, nil, "page build output is invalid", errors.New("compiled output path: "+route.PluginPath))
	}

	route.PluginExists = true
	route.PluginSize = info.Size()
	route.State = GoxRouteBuilt
	return nil
}

func (r *Runtime) loadGoxRoutePluginLocked(route *GoxRouteRuntime) error {
	if route.Plugin != nil {
		route.State = GoxRouteLoaded
		return nil
	}

	route.State = GoxRouteLoading
	route.LastError = ""

	plug, err := plugin.Open(route.PluginPath)
	if err != nil {
		return newGoxError(gox.GoxErrorLoad, route, nil, "page failed to load", err)
	}

	httpHandlers, err := lookupGoxHTTPHandlers(plug)
	if err != nil {
		return newGoxError(gox.GoxErrorLoad, route, nil, "compiled page handlers are missing", err)
	}

	httpContentTypes, err := lookupGoxHTTPContentTypes(plug)
	if err != nil {
		return newGoxError(gox.GoxErrorLoad, route, nil, "compiled page content types are invalid", err)
	}

	actions, err := lookupGoxActions(plug)
	if err != nil {
		return newGoxError(gox.GoxErrorLoad, route, nil, "compiled page actions are missing", err)
	}

	webSocketHandler, err := lookupGoxWebSocketHandler(plug)
	if err != nil {
		return newGoxError(gox.GoxErrorLoad, route, nil, "compiled WebSocket handler is invalid", err)
	}
	channelHandler, err := lookupGoxChannelHandler(plug)
	if err != nil {
		return newGoxError(gox.GoxErrorLoad, route, nil, "compiled Channel handler is invalid", err)
	}

	route.Plugin = plug
	route.HTTPHandlers = httpHandlers
	route.HTTPContentTypes = httpContentTypes
	route.Actions = actions
	route.WebSocketHandler = webSocketHandler
	route.ChannelHandler = channelHandler
	route.State = GoxRouteLoaded
	return nil
}

func lookupGoxChannelHandler(plug *plugin.Plugin) (gox.ChannelHandler, error) {
	sym, err := plug.Lookup("GoxChannelHandler")
	if err != nil {
		return nil, nil
	}
	handler, ok := sym.(*gox.ChannelHandler)
	if !ok || handler == nil || *handler == nil {
		return nil, newRuntimeConfigError("compiled-channel-handler-invalid", "compiled Channel handler has invalid type", nil)
	}
	return *handler, nil
}

func lookupGoxWebSocketHandler(plug *plugin.Plugin) (gox.WebSocketHandler, error) {
	sym, err := plug.Lookup("GoxWebSocketHandler")
	if err != nil {
		return nil, nil
	}

	handler, ok := sym.(*gox.WebSocketHandler)
	if !ok || handler == nil || *handler == nil {
		return nil, newRuntimeConfigError("compiled-websocket-handler-invalid", "compiled WebSocket handler has invalid type", nil)
	}
	return *handler, nil
}

func lookupGoxHTTPHandlers(plug *plugin.Plugin) (map[string]func(*gox.Context), error) {
	sym, err := plug.Lookup("GoxHTTPHandlers")
	if err != nil {
		return map[string]func(*gox.Context){}, nil
	}

	handlers, ok := sym.(*map[string]func(*gox.Context))
	if !ok || handlers == nil {
		return nil, newRuntimeConfigError("compiled-page-handlers-invalid", "compiled page handlers have invalid type", nil)
	}
	if *handlers == nil {
		return map[string]func(*gox.Context){}, nil
	}

	return *handlers, nil
}

func lookupGoxHTTPContentTypes(plug *plugin.Plugin) (map[string]string, error) {
	sym, err := plug.Lookup("GoxHTTPContentTypes")
	if err != nil {
		return map[string]string{}, nil
	}

	contentTypes, ok := sym.(*map[string]string)
	if !ok || contentTypes == nil {
		return nil, newRuntimeConfigError("compiled-page-content-types-invalid", "compiled page content types have invalid type", nil)
	}
	if *contentTypes == nil {
		return map[string]string{}, nil
	}

	return *contentTypes, nil
}

func lookupGoxActions(plug *plugin.Plugin) (map[string]gox.ActionHandler, error) {
	sym, err := plug.Lookup("GoxActions")
	if err != nil {
		return map[string]gox.ActionHandler{}, nil
	}

	actions, ok := sym.(*map[string]gox.ActionHandler)
	if !ok || actions == nil {
		return nil, newRuntimeConfigError("compiled-page-actions-invalid", "compiled page actions have invalid type", nil)
	}
	if *actions == nil {
		return map[string]gox.ActionHandler{}, nil
	}

	return *actions, nil
}

func (r *Runtime) getOrCreateGoxRoute(routePath string, addStage func(string, time.Time)) (*GoxRouteRuntime, bool, error) {
	stageStart := optionalGoxStageStart(addStage)
	normalized := normalizeGoxRoutePath(routePath)
	addOptionalGoxStage(addStage, "resolve.normalize", stageStart)
	if normalized == "" {
		return nil, false, nil
	}

	stageStart = optionalGoxStageStart(addStage)
	r.goxMu.RLock()
	cached := r.goxRoutes[normalized]
	r.goxMu.RUnlock()
	if cached != nil {
		addOptionalGoxStage(addStage, "resolve.cache.hit", stageStart)
		addOptionalGoxStage(addStage, "route.cache.lookup", stageStart)
		return cached, true, nil
	}
	addOptionalGoxStage(addStage, "resolve.cache.miss", stageStart)
	addOptionalGoxStage(addStage, "route.cache.lookup", stageStart)

	stageStart = optionalGoxStageStart(addStage)
	file, ok, err := r.resolveGoxRouteFile(normalized, addStage)
	addOptionalGoxStage(addStage, "resolve.file", stageStart)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}

	projectRoot := r.projectRoot()
	if projectRoot == "" {
		return nil, false, newRuntimeConfigError("project-root-empty", "project root is empty", nil)
	}

	stageStart = optionalGoxStageStart(addStage)
	goBinary, err := r.resolveRepositoryGoBinary()
	addOptionalGoxStage(addStage, "resolve.go", stageStart)
	if err != nil {
		return nil, false, err
	}

	stageStart = optionalGoxStageStart(addStage)
	buildDir, buildVersion, generatedPath, pluginPath, err := resolveGoxBuildPaths(projectRoot, file.routePath, file.modTime)
	addOptionalGoxStage(addStage, "resolve.buildpaths", stageStart)
	if err != nil {
		return nil, false, err
	}

	route := &GoxRouteRuntime{
		RoutePath:     file.routePath,
		AbsPath:       file.absPath,
		RelPath:       file.relPath,
		ModTime:       file.modTime,
		Size:          file.size,
		BuildDir:      buildDir,
		BuildVersion:  buildVersion,
		GeneratedPath: generatedPath,
		PluginPath:    pluginPath,
		GoBinary:      goBinary,
		State:         GoxRouteCold,
	}

	stageStart = optionalGoxStageStart(addStage)
	r.goxMu.Lock()
	if r.goxRoutes == nil {
		r.goxRoutes = make(map[string]*GoxRouteRuntime)
	}
	cached = r.goxRoutes[file.routePath]
	if cached != nil {
		r.goxMu.Unlock()
		addOptionalGoxStage(addStage, "resolve.cache.race", stageStart)
		return cached, true, nil
	}
	r.goxRoutes[file.routePath] = route
	r.goxMu.Unlock()
	addOptionalGoxStage(addStage, "resolve.cache.store", stageStart)

	return route, true, nil
}

func optionalGoxStageStart(addStage func(string, time.Time)) time.Time {
	if addStage == nil {
		return time.Time{}
	}
	return time.Now()
}

func addOptionalGoxStage(addStage func(string, time.Time), name string, started time.Time) {
	if addStage == nil || started.IsZero() {
		return
	}
	addStage(name, started)
}

func normalizeGoxRoutePath(routePath string) string {
	routePath = normalizePreRoutePath(routePath)
	if routePath == "" || routePath == "/" {
		routePath = "/index"
	}
	routePath = strings.ToLower(routePath)

	clean := strings.Trim(routePath, "/")
	if clean == "" || strings.Contains(clean, "..") {
		return ""
	}

	segmentStart := 0
	for i := 0; i <= len(clean); i++ {
		if i < len(clean) && clean[i] != '/' {
			continue
		}
		if i == segmentStart || clean[segmentStart] == '.' {
			return ""
		}
		segmentStart = i + 1
	}

	return routePath
}

func (r *Runtime) resolveGoxRouteFile(routePath string, addStage func(string, time.Time)) (goxRouteFile, bool, error) {
	stageStart := optionalGoxStageStart(addStage)
	projectRoot := r.projectRoot()
	addOptionalGoxStage(addStage, "resolve.file.projectRoot", stageStart)
	if projectRoot == "" {
		return goxRouteFile{}, false, nil
	}

	clean := strings.Trim(routePath, "/")
	if clean == "" || strings.Contains(clean, "..") {
		return goxRouteFile{}, false, nil
	}

	searchRoot := filepath.Join(projectRoot, "pages")
	candidates := []string{}
	if routePath == webSocketModuleRoutePath {
		searchRoot = filepath.Join(projectRoot, "websocket")
		candidates = append(candidates, filepath.Join(searchRoot, "index.gox"))
	} else {
		candidates = append(candidates, filepath.Join(searchRoot, filepath.FromSlash(clean)+".gox"))

		base := filepath.Base(filepath.FromSlash(clean))
		if base != "" && base != "." {
			candidates = append(candidates, filepath.Join(searchRoot, filepath.FromSlash(clean), base+".gox"))
		}
	}

	stageStart = optionalGoxStageStart(addStage)
	pagesDir := searchRoot
	addOptionalGoxStage(addStage, "resolve.file.pagesDir", stageStart)
	for _, candidate := range candidates {
		stageStart = optionalGoxStageStart(addStage)
		abs, err := resolveCaseInsensitivePath(searchRoot, candidate)
		addOptionalGoxStage(addStage, "resolve.file.abs", stageStart)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return goxRouteFile{}, false, err
		}

		stageStart = optionalGoxStageStart(addStage)
		inside := isPathInside(abs, pagesDir)
		addOptionalGoxStage(addStage, "resolve.file.safe", stageStart)
		if !inside {
			continue
		}

		stageStart = optionalGoxStageStart(addStage)
		info, err := os.Stat(abs)
		addOptionalGoxStage(addStage, "resolve.file.stat", stageStart)
		if err != nil || info.IsDir() {
			continue
		}

		stageStart = optionalGoxStageStart(addStage)
		rel, err := filepath.Rel(projectRoot, abs)
		if err != nil {
			rel = abs
		}
		addOptionalGoxStage(addStage, "resolve.file.rel", stageStart)
		if routePath != webSocketModuleRoutePath && !goxsource.IsPublicPage(filepath.ToSlash(rel)) {
			continue
		}
		if routePath != webSocketModuleRoutePath {
			canonical, ok := goxsource.PublicPageRoute(filepath.ToSlash(rel))
			if !ok {
				continue
			}
			if canonical == "/" {
				canonical = "/index"
			}
			if canonical != routePath {
				continue
			}
		}

		return goxRouteFile{
			routePath: routePath,
			absPath:   abs,
			relPath:   filepath.ToSlash(rel),
			modTime:   info.ModTime(),
			size:      info.Size(),
		}, true, nil
	}

	return goxRouteFile{}, false, nil
}

func resolveCaseInsensitivePath(root string, candidate string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", os.ErrNotExist
	}

	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		entries, err := os.ReadDir(current)
		if err != nil {
			return "", err
		}

		var match string
		for _, entry := range entries {
			if !strings.EqualFold(entry.Name(), part) {
				continue
			}
			if match != "" && match != entry.Name() {
				return "", newRuntimeConfigError(
					"duplicate-case-insensitive-route",
					fmt.Sprintf("multiple page paths differ only by letter case: %s and %s", filepath.Join(current, match), filepath.Join(current, entry.Name())),
					nil,
				)
			}
			match = entry.Name()
		}
		if match == "" {
			return "", os.ErrNotExist
		}
		current = filepath.Join(current, match)
	}

	return current, nil
}

func isPathInside(path string, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)

	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}

	return rel == "." || (rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func goxRouteDependenciesFromCompileResult(result *goxcompiler.CompileResult) []GoxRouteDependency {
	if result == nil {
		return nil
	}

	out := make([]GoxRouteDependency, 0, len(result.Dependencies))
	for _, dep := range result.Dependencies {
		absPath := strings.TrimSpace(dep.AbsPath)
		if absPath == "" {
			absPath = strings.TrimSpace(dep.Path)
		}
		if absPath == "" {
			continue
		}

		out = append(out, GoxRouteDependency{
			AbsPath: absPath,
			Size:    dep.Size,
			ModTime: dep.ModTime,
			IsEntry: dep.IsEntry,
		})
	}

	return out
}

func goxDependencyBuildVersion(deps []GoxRouteDependency) (time.Time, string, error) {
	var max time.Time
	entryDir := ""
	for _, dep := range deps {
		if dep.ModTime.After(max) {
			max = dep.ModTime
		}
		if dep.IsEntry && strings.TrimSpace(dep.AbsPath) != "" {
			entryDir = filepath.Dir(dep.AbsPath)
		}
	}
	if len(deps) == 0 {
		return max, "", nil
	}

	type fingerprintDependency struct {
		name    string
		entry   bool
		content []byte
	}
	fingerprintDeps := make([]fingerprintDependency, 0, len(deps))
	for _, dep := range deps {
		absPath := strings.TrimSpace(dep.AbsPath)
		if absPath == "" {
			return max, "", errors.New("dependency path is empty")
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			return max, "", fmt.Errorf("read dependency %q: %w", absPath, err)
		}

		name := filepath.Clean(absPath)
		if entryDir != "" {
			if rel, err := filepath.Rel(entryDir, absPath); err == nil {
				name = rel
			}
		}
		fingerprintDeps = append(fingerprintDeps, fingerprintDependency{
			name:    filepath.ToSlash(name),
			entry:   dep.IsEntry,
			content: content,
		})
	}
	sort.Slice(fingerprintDeps, func(i, j int) bool {
		if fingerprintDeps[i].name == fingerprintDeps[j].name {
			return fingerprintDeps[i].entry && !fingerprintDeps[j].entry
		}
		return fingerprintDeps[i].name < fingerprintDeps[j].name
	})

	hash := sha256.New()
	_, _ = hash.Write([]byte("gox-build-version-v1\x00"))
	var length [8]byte
	for _, dep := range fingerprintDeps {
		binary.BigEndian.PutUint64(length[:], uint64(len(dep.name)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(dep.name))
		if dep.entry {
			_, _ = hash.Write([]byte{1})
		} else {
			_, _ = hash.Write([]byte{0})
		}
		binary.BigEndian.PutUint64(length[:], uint64(len(dep.content)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(dep.content)
	}
	return max, hex.EncodeToString(hash.Sum(nil)), nil
}

func (r *Runtime) applyGoxDependencyBuildVersionLocked(route *GoxRouteRuntime, deps []GoxRouteDependency, result *goxcompiler.CompileResult) error {
	_, buildVersion, err := goxDependencyBuildVersion(deps)
	if err != nil {
		return newGoxError(gox.GoxErrorCompile, route, nil, "failed to fingerprint dependencies", err)
	}
	if buildVersion == "" {
		return newGoxError(gox.GoxErrorCompile, route, nil, "dependency build version is empty", errors.New("route: "+route.RoutePath))
	}
	if route.BuildVersion == buildVersion {
		return nil
	}

	projectRoot := r.projectRoot()
	if projectRoot == "" {
		return newGoxError(gox.GoxErrorBuild, route, nil, "project root is empty", nil)
	}

	oldGeneratedPath := route.GeneratedPath
	oldSourceMapPath := ""
	if result != nil {
		oldSourceMapPath = result.SourceMapPath
	}
	buildDir, _, generatedPath, pluginPath, err := resolveGoxBuildPathsWithVersion(projectRoot, route.RoutePath, buildVersion)
	if err != nil {
		return newGoxError(gox.GoxErrorCompile, route, nil, "failed to resolve dependency build paths", err)
	}

	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return newGoxError(gox.GoxErrorCompile, route, nil, "failed to create gox build dir", err)
	}

	if oldGeneratedPath != generatedPath {
		if err := moveGoxGeneratedArtifacts(oldGeneratedPath, generatedPath, oldSourceMapPath, result); err != nil {
			return newGoxError(gox.GoxErrorCompile, route, nil, "failed to move generated artifacts to dependency build version", err)
		}
	}

	route.BuildDir = buildDir
	route.BuildVersion = buildVersion
	route.GeneratedPath = generatedPath
	route.PluginPath = pluginPath
	route.PluginExists = false
	route.PluginSize = 0
	route.Plugin = nil
	route.HTTPHandlers = nil
	route.HTTPContentTypes = nil
	route.Actions = nil
	route.WebSocketHandler = nil
	route.ChannelHandler = nil
	return nil
}

func moveGoxGeneratedArtifacts(oldGeneratedPath, generatedPath, oldSourceMapPath string, result *goxcompiler.CompileResult) error {
	move := func(oldPath, newPath string) error {
		if err := os.Rename(oldPath, newPath); err != nil {
			if copyErr := copyFile(oldPath, newPath); copyErr != nil {
				return errors.New(fmt.Sprintf("rename=%v copy=%v", err, copyErr))
			}
			_ = os.Remove(oldPath)
		}
		return nil
	}
	if err := move(oldGeneratedPath, generatedPath); err != nil {
		return fmt.Errorf("generated Go file: %w", err)
	}
	sourceMapPath := goxcompiler.BundleSourceMapPath(generatedPath)
	if strings.TrimSpace(oldSourceMapPath) != "" && oldSourceMapPath != sourceMapPath {
		if err := move(oldSourceMapPath, sourceMapPath); err != nil {
			return fmt.Errorf("source map: %w", err)
		}
		if err := goxcompiler.RewriteBundleSourceMapGeneratedURI(sourceMapPath, generatedPath); err != nil {
			return fmt.Errorf("source map generated URI: %w", err)
		}
	}
	if result != nil {
		result.OutPath = generatedPath
		result.SourceMapPath = sourceMapPath
		if absPath, err := filepath.Abs(generatedPath); err == nil {
			result.OutAbsPath = absPath
		}
	}
	return nil
}

func goxRouteNeedsReloadLocked(route *GoxRouteRuntime) bool {
	if route == nil {
		return true
	}
	if len(route.Dependencies) == 0 {
		info, err := os.Stat(route.AbsPath)
		if err != nil || info.IsDir() {
			return true
		}
		return info.Size() != route.Size || !info.ModTime().Equal(route.ModTime)
	}

	for _, dep := range route.Dependencies {
		info, err := os.Stat(dep.AbsPath)
		if err != nil || info.IsDir() {
			return true
		}
		if info.Size() != dep.Size || !info.ModTime().Equal(dep.ModTime) {
			return true
		}
	}

	return false
}

func (r *Runtime) unmountGoxRouteByEntry(entry hotreload.ReloadEntry, event watcher.Event) (*GoxRouteRuntime, bool) {
	if r.hotReload != nil {
		r.hotReload.RemoveEntry(entry.EntryAbsPath)
	}

	route := r.findGoxRouteByEntry(entry)
	if route == nil {
		return nil, false
	}

	r.goxMu.Lock()
	if current := r.goxRoutes[route.RoutePath]; current == route {
		delete(r.goxRoutes, route.RoutePath)
	}
	r.goxMu.Unlock()

	route.mu.Lock()
	route.State = GoxRouteFailed
	route.LastError = fmt.Sprintf("gox route unmounted after %s: %s", event.Op.String(), event.Rel)
	route.Plugin = nil
	route.HTTPHandlers = nil
	route.HTTPContentTypes = nil
	route.Actions = nil
	route.WebSocketHandler = nil
	route.ChannelHandler = nil
	route.Dependencies = nil
	route.GeneratedExists = false
	route.PluginExists = false
	route.mu.Unlock()

	return route, true
}

func (r *Runtime) rebuildGoxRouteByEntry(entry hotreload.ReloadEntry) (*GoxRouteRuntime, bool, error) {
	route := r.findGoxRouteByEntry(entry)
	if route == nil {
		return nil, false, nil
	}

	key := route.RoutePath
	value, err := r.getGoxBuildGroup().Do(key, func() (any, error) {
		if err := r.rebuildGoxRouteTransactional(route); err != nil {
			route.mu.Lock()
			route.LastError = err.Error()
			route.mu.Unlock()
			return nil, err
		}
		return route, nil
	})
	if err != nil {
		return route, true, err
	}
	if rebuilt, ok := value.(*GoxRouteRuntime); ok && rebuilt != nil {
		return rebuilt, true, nil
	}

	return route, true, nil
}

func (r *Runtime) findGoxRouteByEntry(entry hotreload.ReloadEntry) *GoxRouteRuntime {
	entryAbsPath := cleanGoxAbsPath(entry.EntryAbsPath)
	if entryAbsPath == "" {
		return nil
	}

	r.goxMu.RLock()
	for _, candidate := range r.goxRoutes {
		if candidate == nil {
			continue
		}
		if cleanGoxAbsPath(candidate.AbsPath) == entryAbsPath {
			r.goxMu.RUnlock()
			return candidate
		}
	}
	r.goxMu.RUnlock()

	r.channelMu.RLock()
	definitions := make([]*channelDefinitionRuntime, 0, len(r.channelDefinitions))
	for _, definition := range r.channelDefinitions {
		definitions = append(definitions, definition)
	}
	r.channelMu.RUnlock()
	for _, definition := range definitions {
		if definition == nil {
			continue
		}
		definition.mu.Lock()
		candidate := definition.route
		definition.mu.Unlock()
		if candidate != nil && cleanGoxAbsPath(candidate.AbsPath) == entryAbsPath {
			return candidate
		}
	}

	return nil
}

func cleanGoxAbsPath(path string) string {
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

func resetGoxRouteBuildLocked(route *GoxRouteRuntime) {
	if route == nil {
		return
	}

	info, err := os.Stat(route.AbsPath)
	if err == nil && !info.IsDir() {
		route.ModTime = info.ModTime()
		route.Size = info.Size()
	}

	route.State = GoxRouteCold
	route.LastError = ""
	route.GeneratedExists = false
	route.GeneratedSize = 0
	route.PluginExists = false
	route.PluginSize = 0
	route.Plugin = nil
	route.HTTPHandlers = nil
	route.HTTPContentTypes = nil
	route.Actions = nil
	route.WebSocketHandler = nil
	route.ChannelHandler = nil
	route.Dependencies = nil
	route.CompileResult = nil
}

func copyFile(src string, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func (r *Runtime) projectRoot() string {
	projectRoot := strings.TrimSpace(r.ProjectRoot)
	if projectRoot == "" && r.project != nil {
		projectRoot = strings.TrimSpace(r.project.RootDir)
	}
	return projectRoot
}

func (r *Runtime) resolveRepositoryGoBinary() (string, error) {
	goBinary := strings.TrimSpace(r.paths.GoExec)
	if goBinary == "" {
		return "", newRuntimeConfigError("go-binary-path-empty", "toolchain go binary path is empty", nil)
	}

	info, err := os.Stat(goBinary)
	if err != nil {
		return "", newRuntimeConfigError("go-binary-not-found", fmt.Sprintf("toolchain go binary not found: %s", goBinary), nil)
	}
	if info.IsDir() {
		return "", newRuntimeConfigError("go-binary-is-directory", fmt.Sprintf("toolchain go binary is a directory: %s", goBinary), nil)
	}

	return goBinary, nil
}

func resolveGoxBuildPaths(projectRoot string, routePath string, modTime time.Time) (string, string, string, string, error) {
	return resolveGoxBuildPathsWithVersion(projectRoot, routePath, strconv.FormatInt(modTime.UnixNano(), 10))
}

func resolveGoxBuildPathsWithVersion(projectRoot string, routePath string, buildVersion string) (string, string, string, string, error) {
	if routePath == webSocketModuleRoutePath {
		if strings.TrimSpace(buildVersion) == "" {
			return "", "", "", "", newRuntimeConfigError("gox-build-version-empty", "WebSocket build version is empty", nil)
		}
		buildDir := filepath.Join(projectRoot, ".build", "websocket")
		generatedPath := filepath.Join(buildDir, buildVersion+".go")
		pluginPath := filepath.Join(buildDir, buildVersion+".so")
		return buildDir, buildVersion, generatedPath, pluginPath, nil
	}
	if isChannelRoutePath(routePath) {
		if strings.TrimSpace(buildVersion) == "" {
			return "", "", "", "", newRuntimeConfigError("gox-build-version-empty", "Channel build version is empty", nil)
		}
		name := strings.TrimPrefix(routePath, channelRoutePrefix)
		channelPath, err := goxBuildRoutePath("/" + name)
		if err != nil {
			return "", "", "", "", err
		}
		buildDir := filepath.Join(projectRoot, ".build", "websocket", "channels", filepath.FromSlash(channelPath))
		return buildDir, buildVersion, filepath.Join(buildDir, buildVersion+".go"), filepath.Join(buildDir, buildVersion+".so"), nil
	}

	buildRoutePath, err := goxBuildRoutePath(routePath)
	if err != nil {
		return "", "", "", "", err
	}
	if strings.TrimSpace(buildVersion) == "" {
		return "", "", "", "", newRuntimeConfigError("gox-build-version-empty", fmt.Sprintf("gox build version is empty: %s", routePath), nil)
	}

	buildDir := filepath.Join(projectRoot, ".build", "pages", filepath.FromSlash(buildRoutePath))
	generatedPath := filepath.Join(buildDir, buildVersion+".go")
	pluginPath := filepath.Join(buildDir, buildVersion+".so")

	return buildDir, buildVersion, generatedPath, pluginPath, nil
}

func goxBuildRoutePath(routePath string) (string, error) {
	routePath = normalizePreRoutePath(routePath)
	if routePath == "" || routePath == "/" {
		routePath = "/index"
	}

	clean := strings.Trim(routePath, "/")
	if clean == "" {
		clean = "index"
	}
	if strings.Contains(clean, "..") {
		return "", newRuntimeConfigError("invalid-gox-route-build-path", fmt.Sprintf("invalid gox route build path: %s", routePath), nil)
	}

	parts := strings.Split(clean, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", newRuntimeConfigError("invalid-gox-route-build-path", fmt.Sprintf("invalid gox route build path: %s", routePath), nil)
		}
	}

	return strings.Join(parts, "/"), nil
}
