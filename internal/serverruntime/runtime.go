package serverruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gox-lang/server/internal/clientbuilder"
	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/project"
	"github.com/gox-lang/server/internal/projecttoolchain"
	"github.com/gox-lang/server/internal/scaffold"
	"github.com/gox-lang/server/internal/toolchain"
	"github.com/gox-lang/server/internal/watcher"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

const (
	methodGET uint32 = 1 << iota
	methodPOST
	methodPUT
	methodDELETE
	methodPATCH
	methodHEAD
	methodOPTIONS
)

type benchmarkRequestContextKey struct{}

func withBenchmarkRequestContext(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}
	return req.WithContext(context.WithValue(req.Context(), benchmarkRequestContextKey{}, true))
}

func isBenchmarkRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	value, _ := req.Context().Value(benchmarkRequestContextKey{}).(bool)
	return value
}

type Runtime struct {
	requestMu         sync.Mutex
	reloadMu          sync.Mutex
	requestGeneration *requestGeneration
	Config            server.Config
	Out               io.Writer
	ProjectRoot       string
	instanceID        string
	startedAt         time.Time

	project         *project.Project
	paths           toolchain.Paths
	goPreflight     goSourcePreflight
	hotReload       *hotreload.Index
	hotReloadEngine *hotreload.Engine

	runtimeState         runtimeStateStore
	sessionStorageMu     sync.RWMutex
	sessionStorage       server.SessionStorageRuntime
	clusterRuntime       server.ClusterRuntime
	clusterEventCancel   context.CancelFunc
	clusterEventID       atomic.Uint64
	clusterTransferID    atomic.Uint64
	clusterRequestsOnce  sync.Once
	clusterRequests      *clusterRequestCoordinator
	sessionExpireCancel  context.CancelFunc
	sessionTouchMu       sync.Mutex
	sessionTouches       map[string]time.Time
	rateLimitStore       *RateLimitStore
	staticCache          *StaticCache
	staticBuildGroup     *staticSingleflightGroup
	goxBuildGroup        *staticSingleflightGroup
	cache404             *Cache404
	dynamicWarmup        *DynamicWarmupCache
	webSocketsOnce       sync.Once
	webSockets           *webSocketRegistry
	groupsOnce           sync.Once
	groups               *socketGroupRegistry
	channelReloadMu      sync.RWMutex
	channelMu            sync.RWMutex
	channelDefinitions   map[string]*channelDefinitionRuntime
	channelInstances     map[string]*gox.Channel
	channelMembers       map[string]map[string]*gox.Socket
	socketChannels       map[string]map[string]struct{}
	channelPending       map[string]int
	unbindSocketRegistry func()
	unbindSessions       func()
	unbindGroups         func()
	unbindChannels       func()
	unbindServerInfo     func()
	unbindClusterInfo    func()
	watcher              *watcher.Watcher
	activeListeners      *runtimeListeners // guarded by reloadMu
	vendorBuildOnce      sync.Once
	vendorBuildWG        sync.WaitGroup
	restartOnce          sync.Once
	restartCoordinator   *runtimeRestartCoordinator

	goxMu     sync.RWMutex
	goxRoutes map[string]*GoxRouteRuntime

	errorMu      sync.RWMutex
	errorRuntime *GoxErrorRuntime

	closeOnce       sync.Once
	closeErr        error
	configOverrides ConfigOverrides
}

// ConfigOverrides contains execution-only values supplied by commands such as
// gox run. They are reapplied after config hot reload and never persisted.
type ConfigOverrides struct {
	DevEnabled    bool
	DevEnabledSet bool
	HTTPHost      string
	HTTPHostSet   bool
	HTTPPort      int
	HTTPPortSet   bool
}

func (overrides ConfigOverrides) apply(config *server.Config) {
	if config == nil {
		return
	}
	if overrides.DevEnabledSet {
		config.Dev.Enabled = overrides.DevEnabled
	}
	if overrides.HTTPHostSet {
		config.HTTP.Host = overrides.HTTPHost
	}
	if overrides.HTTPPortSet {
		config.HTTP.Port = overrides.HTTPPort
	}
}

func New(config server.Config, out io.Writer, p *project.Project, paths toolchain.Paths, result *goxcompiler.CompileResult, overrides ...ConfigOverrides) (*Runtime, error) {
	var executionOverrides ConfigOverrides
	if len(overrides) > 0 {
		executionOverrides = overrides[0]
		executionOverrides.apply(&config)
	}

	tlsConfig, tlsErr := normalizeTLSConfig(config.HTTP)
	if tlsErr != nil {
		return nil, tlsErr
	}
	config.HTTP.TLS = tlsConfig

	clientbuilder.LoadClientCache(p.RootDir)
	nodePaths, pnpmPaths, err := projecttoolchain.Ensure(p.RootDir)
	if err != nil {
		return nil, err
	}
	logs.Info("using Node toolchain: %s", nodePaths.NodeExec)
	logs.Info("using PNPM toolchain: %s", pnpmPaths.PNPMExec)

	instanceID, err := newRuntimeInstanceID()
	if err != nil {
		return nil, err
	}
	r := &Runtime{
		Config:             config,
		Out:                out,
		ProjectRoot:        p.RootDir,
		instanceID:         instanceID,
		startedAt:          time.Now(),
		configOverrides:    executionOverrides,
		project:            p,
		paths:              paths,
		hotReload:          hotreload.NewIndex(),
		rateLimitStore:     NewRateLimitStore(),
		staticCache:        LoadStaticCache(p.RootDir),
		staticBuildGroup:   newStaticSingleflightGroup(),
		goxBuildGroup:      newStaticSingleflightGroup(),
		cache404:           LoadCache404(p.RootDir),
		dynamicWarmup:      LoadDynamicWarmupCache(p.RootDir),
		goxRoutes:          make(map[string]*GoxRouteRuntime),
		sessionTouches:     make(map[string]time.Time),
		channelDefinitions: make(map[string]*channelDefinitionRuntime),
		channelInstances:   make(map[string]*gox.Channel),
		channelMembers:     make(map[string]map[string]*gox.Socket),
		socketChannels:     make(map[string]map[string]struct{}),
		channelPending:     make(map[string]int),
	}
	initialized := false
	defer func() {
		if !initialized {
			r.cache404.Close()
		}
	}()
	if err := r.indexChannelDefinitions(); err != nil {
		_ = r.close(false)
		return nil, newRuntimeConfigError("channel-index-failed", "index WebSocket channels failed", err)
	}

	state, err := NewRuntimeState(config, p.RootDir, result)
	if err != nil {
		if r.rateLimitStore != nil {
			r.rateLimitStore.Close()
		}
		if r.staticCache != nil {
			r.staticCache.Close()
		}
		return nil, err
	}

	if config.WebSocket.Enabled {
		if err := scaffold.EnsureWebSocket(p.RootDir); err != nil {
			_ = r.close(false)
			return nil, err
		}
	}

	r.runtimeState.Store(state)
	if err := prepareDynamicCompression(state.Config.HTTP.Compression); err != nil {
		_ = r.close(false)
		return nil, newRuntimeConfigError("http-compression-init-failed", "initialize dynamic HTTP compression failed", err)
	}
	r.startMissingFrameworkVendorsAsync(nodePaths, pnpmPaths)
	r.configureHotReloadEngine()
	r.registerHotReloadCompileResult(hotreload.ReloadConfig, result)

	if state.Session != nil {
		storage, clusterRuntime, err := OpenSessionRuntime(config.Cluster, state.Session, r.instanceID, config.WebSocket.MaxMessageSize)
		if err != nil {
			if r.rateLimitStore != nil {
				r.rateLimitStore.Close()
			}
			if r.staticCache != nil {
				r.staticCache.Close()
			}
			return nil, newRuntimeConfigError("cluster-open-failed", "open cluster or session fallback failed", err)
		}
		r.sessionStorage = storage
		r.clusterRuntime = clusterRuntime
		r.sessionExpireCancel = r.startSessionExpireSubscription(storage, state.Session)
	}

	if state.PreRouter != nil {
		state.PreRouter.LogLoaded()
	}

	r.loadGoxErrorRuntimeAtStartup()
	r.warmupDynamicRoutesAtStartup()
	if r.dynamicWarmup != nil {
		r.dynamicWarmup.Start(r)
	}
	r.bindSocketRegistry()
	r.bindSessions()
	r.bindGroups()
	r.bindChannels()
	r.bindPublicRuntimeInfo()
	if r.clusterRuntime != nil {
		r.clusterEventCancel = r.startClusterEventSubscription(r.clusterRuntime)
	}

	if err := r.runOnStartServer(); err != nil {
		_ = r.close(false)
		return nil, newRuntimeConfigError("server-start-hook-failed", "server startup hook failed", err)
	}

	initialized = true
	return r, nil
}

func (r *Runtime) ListenAndServe() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listeners, err := openRuntimeListeners(r.state().Config.HTTP, r.ProjectRoot, r)
	if err != nil {
		return errors.Join(err, r.Close())
	}
	r.reloadMu.Lock()
	r.activeListeners = listeners
	r.reloadMu.Unlock()
	defer func() {
		r.reloadMu.Lock()
		r.activeListeners = nil
		r.reloadMu.Unlock()
		listeners.Close()
	}()
	if err := r.startWatcher(); err != nil {
		return errors.Join(err, r.Close())
	}
	r.waitForFrameworkVendors()
	if ctx.Err() != nil {
		return errors.Join(listeners.Close(), r.Close())
	}
	serveErr := listeners.Serve()
	for i, listener := range listeners.listeners {
		logs.Info("server listening on %s://%s", listeners.schemes[i], listener.Addr())
	}
	select {
	case err := <-serveErr:
		return errors.Join(err, listeners.Close(), r.Close())
	case request := <-r.restartRequests():
		return r.finishRuntimeRestart(listeners, request)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r.disconnectLocalWebSocketsForRestart()
		return errors.Join(listeners.Shutdown(shutdownCtx), r.Close())
	}
}

func (r *Runtime) finishRequest(
	ctx *gox.Context,
	req *http.Request,
	responseState *responseRuntimeState,
	state *RuntimeState,
	trace *requestPipelineTrace,
	benchmarkRequest bool,
	handledKind *string,
	start time.Time,
) {
	if recovered := recover(); recovered != nil {
		stageStart := trace.now()
		if flow, ok := recovered.(gox.FlowControl); ok {
			if err := executeResponseFlow(ctx, nil, flow, trace); err != nil {
				r.renderGoxError(ctx, req, trace, err)
			}
		} else {
			r.recoverRequest(ctx, req, recovered)
		}
		trace.add("recover", stageStart)
	}

	var writer *GoxWriter
	if gw, ok := ctx.Writer.(*GoxWriter); ok {
		writer = gw
	}

	stageStart := trace.now()
	if !responseState.Upgraded() {
		r.ensureTerminalHTTPErrorResponse(ctx, req, trace, writer)
	}
	trace.add("error.finalize", stageStart)
	if gw, ok := ctx.Writer.(*GoxWriter); ok {
		writer = gw
	}

	stageStart = trace.now()
	if !responseState.Upgraded() {
		if closer, ok := ctx.Writer.(interface{ Close() error }); ok {
			r.closeRequestWriter(ctx, req, closer)
		}
		// Empty handlers still own their status, headers and cookies. Commit only
		// after error rendering and writer recovery have had their final say.
		if !responseState.WroteHeader() {
			responseState.WriteHeader(responseState.Status())
		}
	}
	if writer != nil {
		stats := writer.Stats()
		trace.addDeepDuration("writer.flush.buffer", stats.CloseFlushBufferDuration)
		trace.addDeepDuration("response.write", stats.CloseResponseWriteDuration)
		trace.addDeepDuration("pool.put", stats.ClosePoolPutDuration)
	}
	if !stageStart.IsZero() {
		trace.addDuration("writer.close.total", time.Since(stageStart))
	}

	status := responseState.Status()
	if ctx.Error == nil && isHTTPErrorStatus(status) {
		err := newHTTPRuntimeError(status)
		applyGoxErrorContext(err, nil, ctx)
		ctx.Error = err
	}
	kind := ""
	if handledKind != nil {
		kind = *handledKind
	}
	if shouldLogRequest(state.Config.Dev, kind, req.URL.Path) {
		stageStart = trace.now()
		elapsed := time.Duration(0)
		if !start.IsZero() {
			elapsed = time.Since(start)
		}
		method := requestLogMethod(ctx, req)
		logs.Request(status, elapsed, method, req.URL.Path, devEnabled(state.Config.Dev))
		trace.add("logs.request", stageStart)
	}

	stageStart = trace.now()
	r.saveSession(req, ctx)
	trace.add("session.save", stageStart)

	if shouldTraceRequest(state.Config.Dev, kind, req.URL.Path) {
		trace.logHTTP(status, req.Method, req.URL.Path, writer)
	}
	if writer != nil {
		ctx.Writer = nil
		releaseGoxWriter(writer)
	}
	unregisterContextRuntimeState(ctx)
	ctx.Response = nil
	ctx.Body = nil
	releaseRuntimeResponse(responseState)
}

func requestLogMethod(ctx *gox.Context, req *http.Request) string {
	if ctx != nil {
		if method := strings.TrimSpace(ctx.Request.Route.Method()); method != "" {
			return method
		}
	}
	if req != nil {
		return req.Method
	}
	return ""
}

func executeLifecycleHook(name string, hook func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%s panic: %v", name, recovered)
		}
	}()

	return hook()
}

func (r *Runtime) runOnStartServer() error {
	if r == nil || r.Config.OnStartServer == nil {
		return nil
	}

	logs.Info("server startup hook executing")
	if err := executeLifecycleHook("OnStartServer", r.Config.OnStartServer); err != nil {
		logs.Error("server startup hook failed: %v", err)
		return err
	}
	logs.Info("server startup hook completed")

	return nil
}

func (r *Runtime) Close() error {
	return r.close(true)
}

func (r *Runtime) close(runStopHook bool) error {
	if r == nil {
		return nil
	}

	r.closeOnce.Do(func() {
		var errs []error

		if r.watcher != nil {
			if err := r.watcher.Close(); err != nil {
				errs = append(errs, err)
			}
			r.watcher = nil
		}

		r.sessionStorageMu.Lock()
		sessionExpireCancel := r.sessionExpireCancel
		clusterEventCancel := r.clusterEventCancel
		clusterRuntime := r.clusterRuntime
		sessionStorage := r.sessionStorage
		r.sessionExpireCancel = nil
		r.clusterEventCancel = nil
		r.clusterRuntime = nil
		r.sessionStorage = nil
		r.sessionStorageMu.Unlock()
		if sessionExpireCancel != nil {
			sessionExpireCancel()
		}
		if clusterEventCancel != nil {
			clusterEventCancel()
		}
		r.clusterRequestCoordinator().close()

		if clusterRuntime != nil {
			if err := clusterRuntime.Close(); err != nil {
				errs = append(errs, err)
			}
		} else if sessionStorage != nil {
			if err := sessionStorage.Close(); err != nil {
				errs = append(errs, err)
			}
		}

		if r.rateLimitStore != nil {
			r.rateLimitStore.Close()
			r.rateLimitStore = nil
		}

		if r.staticCache != nil {
			r.staticCache.Close()
			r.staticCache = nil
		}

		r.cache404.Close()

		if r.dynamicWarmup != nil {
			r.dynamicWarmup.Close()
			r.dynamicWarmup = nil
		}

		if runStopHook && r.Config.OnStopServer != nil {
			logs.Info("server shutdown hook executing")
			if err := executeLifecycleHook("OnStopServer", r.Config.OnStopServer); err != nil {
				logs.Error("server shutdown hook failed: %v", err)
			} else {
				logs.Info("server shutdown hook completed")
			}
		}

		if r.unbindSocketRegistry != nil {
			r.unbindSocketRegistry()
			r.unbindSocketRegistry = nil
		}
		if r.unbindSessions != nil {
			r.unbindSessions()
			r.unbindSessions = nil
		}
		if r.unbindGroups != nil {
			r.unbindGroups()
			r.unbindGroups = nil
		}
		if r.unbindChannels != nil {
			r.closeChannelInstances()
			r.unbindChannels()
			r.unbindChannels = nil
		}
		if r.unbindServerInfo != nil {
			r.unbindServerInfo()
			r.unbindServerInfo = nil
		}
		if r.unbindClusterInfo != nil {
			r.unbindClusterInfo()
			r.unbindClusterInfo = nil
		}

		r.vendorBuildWG.Wait()
		r.closeErr = errors.Join(errs...)
	})

	return r.closeErr
}

func (r *Runtime) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	benchmarkRequest := isBenchmarkRequest(req)
	state, generation, cacheGeneration := r.acquireRequestGeneration()
	defer generation.release()
	var start time.Time
	if requestLoggingPossible(state.Config.Dev, req.URL.Path) {
		start = time.Now()
	}
	trace := newRequestPipelineTrace(devTraceEnabled(state.Config.Dev), devTraceDeepEnabled(state.Config.Dev))
	res, responseState := newRuntimeResponse(w, state.ClientAssets)
	responseState.development = devEnabled(state.Config.Dev)
	handledKind := ""

	request := buildRequestWithBody(req, responseState.body, state.IPResolver)
	ctx := &responseState.contextValue
	gox.ResetRuntimeContext(ctx)
	ctx.Request = request
	ctx.Response = res
	ctx.Body = request.Body
	ctx.Writer = newGoxWriter(responseState, req, state.Config.HTTP.Compression, false, trace.deep)
	gox.BindClientIslandRuntime(ctx, state.ClientAssets)
	registerContextRuntimeState(ctx, &responseState.contextState)
	responseState.contextState.requestState = state
	responseState.contextState.generation = generation

	defer r.finishRequest(ctx, req, responseState, state, trace, benchmarkRequest, &handledKind, start)

	stageStart := trace.now()
	ok := r.resolveForwardedIP(ctx, req)
	trace.add("ip.resolve", stageStart)
	if !ok {
		return
	}

	stageStart = trace.now()
	ok = r.methodsSafe(ctx, req)
	trace.add("methods.safe", stageStart)
	if !ok {
		return
	}

	stageStart = trace.now()
	clientValuesHandled := r.clientValuesRoute(ctx, req)
	trace.add("client.values", stageStart)
	if clientValuesHandled {
		handledKind = "static"
		return
	}

	stageStart = trace.now()
	webSocketClientHandled := r.webSocketClientRoute(ctx, req)
	trace.add("websocket.client", stageStart)
	if webSocketClientHandled {
		handledKind = "static"
		return
	}

	skipCache404 := isClientAssetRequestPath(ctx.Request.URL.Path()) || isWebSocketRequestPath(ctx.Request.URL.Path())

	stageStart = trace.now()
	r.preRoute(ctx)
	trace.add("preRoute", stageStart)

	stageStart = trace.now()
	r.clientStaticRoute(ctx, req)
	trace.add("client.staticRoute", stageStart)

	stageStart = trace.now()
	ok = r.session(ctx, req, trace)
	trace.add("session.load", stageStart)
	if !ok {
		return
	}

	if !benchmarkRequest {
		stageStart = trace.now()
		ok = r.rateLimit(ctx, req)
		trace.add("rateLimit", stageStart)
		if !ok {
			return
		}
	}

	if !benchmarkRequest && !skipCache404 {
		stageStart = trace.now()
		cache404Hit := r.cache404.Has(ctx.Request.URL.Path())
		trace.add("cache404.has", stageStart)
		if cache404Hit {
			handledKind = "404"
			stageStart = trace.now()
			r.renderHTTPStatusError(ctx, req, trace, http.StatusNotFound, "page not found")
			trace.add("response.404.cached", stageStart)
			return
		}
	}

	stageStart = trace.now()
	webSocketHandshakeHandled := r.webSocketHandshakeRoute(ctx, req)
	trace.add("websocket.handshake", stageStart)
	if webSocketHandshakeHandled {
		handledKind = "static"
		return
	}

	stageStart = trace.now()
	staticHandled := r.static(ctx, req)
	trace.add("static", stageStart)
	if staticHandled {
		handledKind = "static"
		return
	}

	stageStart = trace.now()
	goxHandled := r.gox(ctx, req, trace)
	trace.add("gox", stageStart)
	if goxHandled {
		handledKind = "gox"
		return
	}

	if !benchmarkRequest && !skipCache404 {
		r.cache404.AddIfGeneration(ctx.Request.URL.Path(), cacheGeneration)
	}
	handledKind = "404"
	stageStart = trace.now()
	r.renderHTTPStatusError(ctx, req, trace, http.StatusNotFound, "page not found")
	trace.add("response.404", stageStart)
}
