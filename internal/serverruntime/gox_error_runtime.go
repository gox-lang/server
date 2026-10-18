package serverruntime

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gox-lang/server/internal/goxerrors"
	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/watcher"

	"github.com/gox-lang/gox"
)

const (
	goxErrorRoutePath = "/.error"
	goxErrorRelPath   = "pages/.error.gox"
)

func isHTTPErrorStatus(status int) bool {
	return status >= 400 && status <= 599
}

func newHTTPRuntimeError(status int) *GoxError {
	if !isHTTPErrorStatus(status) {
		status = http.StatusInternalServerError
	}
	message := http.StatusText(status)
	if message == "" {
		message = "HTTP Error"
	}
	err := goxerrors.New(goxerrors.TypeHTTP, gox.GoxErrorDispatch, message, nil)
	err.Kind = httpErrorKind(status, message)
	err.Status = status
	err.StatusCode = status
	err.StatusMessage = message
	err.EnsureStatus()
	return err
}

func httpErrorKind(status int, message string) string {
	switch status {
	case http.StatusNotFound:
		return "not-found"
	case http.StatusMethodNotAllowed:
		return "method-not-allowed"
	case http.StatusTooManyRequests:
		return "rate-limit"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusServiceUnavailable:
		return "service-unavailable"
	case http.StatusInternalServerError:
		return "internal-server-error"
	}
	value := strings.ToLower(strings.TrimSpace(message))
	if value == "" {
		return "http-error"
	}
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

type GoxErrorRuntime struct {
	mu sync.RWMutex

	Route    *GoxRouteRuntime
	Handlers map[string]func(*gox.Context)
}

func (r *Runtime) loadGoxErrorRuntimeAtStartup() {
	if err := r.rebuildGoxErrorRuntime(); err != nil {
		var goxErr *GoxError
		if asGoxErrorInto(err, &goxErr) {
			logGoxError("startup", goxErr)
			return
		}
		logs.Warm("error runtime failed to load: %v", err)
	}
}

func (r *Runtime) reloadErrorEntry(entry hotreload.ReloadEntry, event watcher.Event) error {
	if isHotReloadDeleteEvent(event) {
		r.unmountGoxErrorRuntime()
		if r.hotReload != nil {
			r.hotReload.RemoveEntry(entry.EntryAbsPath)
		}
		logs.Info("error hotReload unmounted: changed=%s entry=%s", event.Rel, normalizeGoxUserFacingPath(entry.EntryPath))
		return nil
	}

	if err := r.rebuildGoxErrorRuntime(); err != nil {
		if goxErr := asGoxError(err, gox.GoxErrorBuild, nil, nil, "error hotReload rebuild failed"); goxErr != nil {
			goxErr.AddDetail("hotReload.changed", event.Rel)
			goxErr.AddDetail("hotReload.entry", entry.EntryPath)
			return goxErr
		}
		return err
	}

	logs.Info("error hotReload rebuilt: changed=%s entry=%s", event.Rel, normalizeGoxUserFacingPath(entry.EntryPath))
	return nil
}

func (r *Runtime) unmountGoxErrorRuntime() {
	r.errorMu.Lock()
	r.errorRuntime = nil
	r.errorMu.Unlock()
}

func (r *Runtime) rebuildGoxErrorRuntime() error {
	route, ok, err := r.newGoxErrorRouteRuntime()
	if err != nil || !ok {
		if !ok {
			r.unmountGoxErrorRuntime()
		}
		return err
	}

	route.mu.Lock()
	defer route.mu.Unlock()

	if err := r.compileGoxRouteLocked(route); err != nil {
		route.State = GoxRouteFailed
		route.LastError = err.Error()
		return err
	}
	if err := r.buildGoxRoutePluginLocked(route); err != nil {
		route.State = GoxRouteFailed
		route.LastError = err.Error()
		return err
	}
	if err := r.loadGoxRoutePluginLocked(route); err != nil {
		route.State = GoxRouteFailed
		route.LastError = err.Error()
		return err
	}

	route.State = GoxRouteLoaded
	route.LastError = ""
	if err := r.cleanupGoxRouteBuildArtifacts(route); err != nil {
		return newGoxError(gox.GoxErrorLoad, route, nil, "failed to clean stale gox error build artifacts", err)
	}

	registry := &GoxErrorRuntime{Route: route, Handlers: route.HTTPHandlers}
	r.errorMu.Lock()
	r.errorRuntime = registry
	r.errorMu.Unlock()
	return nil
}

func (r *Runtime) newGoxErrorRouteRuntime() (*GoxRouteRuntime, bool, error) {
	projectRoot := r.projectRoot()
	if projectRoot == "" {
		return nil, false, newRuntimeConfigError("project-root-empty", "project root is empty", nil)
	}

	absPath := filepath.Join(projectRoot, filepath.FromSlash(goxErrorRelPath))
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if info.IsDir() {
		return nil, false, nil
	}

	goBinary, err := r.resolveRepositoryGoBinary()
	if err != nil {
		return nil, false, err
	}

	buildDir, buildVersion, generatedPath, pluginPath, err := resolveGoxBuildPaths(projectRoot, goxErrorRoutePath, info.ModTime())
	if err != nil {
		return nil, false, err
	}

	return &GoxRouteRuntime{
		RoutePath:     goxErrorRoutePath,
		AbsPath:       absPath,
		RelPath:       goxErrorRelPath,
		ModTime:       info.ModTime(),
		Size:          info.Size(),
		BuildDir:      buildDir,
		BuildVersion:  buildVersion,
		GeneratedPath: generatedPath,
		PluginPath:    pluginPath,
		GoBinary:      goBinary,
		State:         GoxRouteCold,
	}, true, nil
}

func (r *Runtime) errorHandlerForStatus(status int) (string, *GoxRouteRuntime, func(*gox.Context), bool) {
	r.errorMu.RLock()
	registry := r.errorRuntime
	r.errorMu.RUnlock()
	if registry == nil || len(registry.Handlers) == 0 {
		return "fallback", nil, nil, false
	}

	name := "Error" + strconv.Itoa(status)
	if handler := registry.Handlers[name]; handler != nil {
		return name, registry.Route, handler, true
	}
	if handler := registry.Handlers["Default"]; handler != nil {
		return "Default", registry.Route, handler, true
	}
	return "fallback", registry.Route, nil, false
}

func (r *Runtime) renderErrorPage(ctx *gox.Context, req *http.Request, trace *requestPipelineTrace, err *GoxError) bool {
	if ctx == nil || err == nil || req == nil {
		return false
	}
	if isRenderingErrorPage(ctx) {
		return false
	}

	err.EnsureStatus()
	resolveStart := trace.nowDeep()
	handlerName, errorRoute, handler, ok := r.errorHandlerForStatus(err.StatusCode)
	trace.addDeep("error.resolve", resolveStart)
	trace.addDeepField("error.handler.lookup", "Error"+strconv.Itoa(err.StatusCode))
	if !ok || handler == nil {
		trace.addDeepField("error.handler", "fallback")
		return false
	}
	trace.addDeepField("error.handler", handlerName)

	if res, ok := runtimeResponse(ctx); ok && res != nil && res.WroteHeader() {
		return false
	}

	if discard, ok := ctx.Writer.(interface{ DiscardBuffered() }); ok {
		discard.DiscardBuffered()
	}

	res, ok := runtimeResponse(ctx)
	if !ok || res == nil {
		return false
	}

	setRenderingErrorPage(ctx, true)
	defer setRenderingErrorPage(ctx, false)

	ctx.Response.Headers.Set("Content-Type", "text/html; charset=utf-8")
	ctx.Response.Status.SetCode(err.StatusCode)

	if _, ok := ctx.Writer.(*GoxWriter); !ok {
		ctx.Writer = newGoxWriter(res, req, r.stateFor(ctx).Config.HTTP.Compression, false, false)
	}

	renderStart := trace.nowDeep()
	if execErr := executeGoxHandler(ctx, errorRoute, handler, trace); execErr != nil {
		trace.addDeep("error.handler.failed", renderStart)
		execErr.OriginalError = err
		execErr.AddDetail("error.handler", handlerName)
		translateGoxError(execErr)
		logGoxError("errorHandler", execErr)
		if discard, ok := ctx.Writer.(interface{ DiscardBuffered() }); ok {
			discard.DiscardBuffered()
		}
		return false
	}
	trace.addDeep("error.handler.render", renderStart)

	return true
}

func responseHasPendingBody(ctx *gox.Context, res *responseRuntimeState, writer *GoxWriter) bool {
	if res != nil && res.BodyWritten() {
		return true
	}
	if writer != nil && writer.BufferedBytes() > 0 {
		return true
	}
	if ctx != nil {
		if buffered, ok := ctx.Writer.(interface{ BufferedBytes() int }); ok && buffered.BufferedBytes() > 0 {
			return true
		}
	}
	return false
}

func (r *Runtime) ensureTerminalHTTPErrorResponse(ctx *gox.Context, req *http.Request, trace *requestPipelineTrace, writer *GoxWriter) {
	if ctx == nil || req == nil {
		return
	}
	res, ok := runtimeResponse(ctx)
	if !ok || res == nil {
		return
	}
	status := res.Status()
	if !isHTTPErrorStatus(status) {
		return
	}
	if res.WroteHeader() || responseHasPendingBody(ctx, res, writer) {
		if ctx.Error == nil {
			err := newHTTPRuntimeError(status)
			applyGoxErrorContext(err, nil, ctx)
			translateGoxError(err)
			ctx.Error = err
		}
		return
	}
	message := strings.ToLower(http.StatusText(status))
	if message == "" {
		message = "http error"
	}
	r.renderHTTPStatusError(ctx, req, trace, status, message)
}

func (r *Runtime) renderPlainStatusFallback(ctx *gox.Context, status int) {
	if ctx == nil {
		return
	}
	if closer, ok := ctx.Writer.(interface{ DiscardBuffered() }); ok {
		closer.DiscardBuffered()
	}
	ctx.Response.Headers.Set("Content-Type", "text/plain; charset=utf-8")
	writeResponseHeader(ctx, status)
	_, _ = ctx.Writer.Write([]byte(fmt.Sprintf("%d %s", status, http.StatusText(status))))
}

func (r *Runtime) renderHTTPStatusError(ctx *gox.Context, req *http.Request, trace *requestPipelineTrace, status int, message string) {
	if !isHTTPErrorStatus(status) {
		writeResponseHeader(ctx, status)
		return
	}

	err := newHTTPRuntimeError(status)
	if message != "" {
		err.Message = message
	}
	applyGoxErrorContext(err, nil, ctx)
	translateGoxError(err)
	ctx.Error = err
	if r.renderErrorPage(ctx, req, trace, err) {
		return
	}
	fallbackStart := trace.nowDeep()
	trace.addDeepField("error.handler", "fallback")
	r.renderPlainStatusFallback(ctx, status)
	trace.addDeep("error.handler.fallback", fallbackStart)
}

func asGoxErrorInto(err error, target **GoxError) bool {
	if err == nil || target == nil {
		return false
	}
	return errors.As(err, target)
}
