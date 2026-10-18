package serverruntime

import (
	"errors"

	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/watcher"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (r *Runtime) configureHotReloadEngine() {
	engine := hotreload.NewEngine(r.hotReload)
	engine.Register(hotreload.ReloadConfig, r.reloadConfigEntry)
	engine.Register(hotreload.ReloadPage, r.reloadPageEntry)
	engine.Register(hotreload.ReloadError, r.reloadErrorEntry)
	engine.Register(hotreload.ReloadClient, r.reloadClientEntry)
	r.hotReloadEngine = engine
}

func (r *Runtime) handleHotReloadEvent(event watcher.Event) {
	if isGoxErrorFileEvent(event) {
		if isHotReloadDeleteEvent(event) {
			r.unmountGoxErrorRuntime()
			logs.Info("error hotReload unmounted: changed=%s", event.Rel)
			return
		}
		if err := r.rebuildGoxErrorRuntime(); err != nil {
			var goxErr *GoxError
			if errors.As(err, &goxErr) {
				goxErr.AddDetail("hotReload.changed", event.Rel)
				logGoxError("hotReload", goxErr)
				return
			}
			logs.Warm("error hotReload failed after %s: %v", event.Rel, err)
			return
		}
		logs.Info("error hotReload rebuilt: changed=%s", event.Rel)
		return
	}

	if r.hotReloadEngine == nil {
		logs.Warm("hotReload engine is not initialized; ignoring %s", event.Rel)
		return
	}

	count, err := r.hotReloadEngine.Handle(event)
	if err != nil {
		var goxErr *GoxError
		if errors.As(err, &goxErr) {
			goxErr.AddDetail("hotReload.changed", event.Rel)
			logGoxError("hotReload", goxErr)
			return
		}

		logs.Warm("hotReload failed after %s: %v", event.Rel, err)
		return
	}

	if count == 0 {
		logs.Warm("hotReload has no dependents for %s", event.Rel)
		return
	}

	logs.Info("hotReload completed after %s: entries=%d", event.Rel, count)
}

func isGoxErrorFileEvent(event watcher.Event) bool {
	return normalizeGoxUserFacingPath(event.Rel) == goxErrorRelPath
}

func (r *Runtime) registerHotReloadCompileResult(kind hotreload.ReloadKind, result *goxcompiler.CompileResult) {
	if r.hotReload == nil || result == nil {
		return
	}

	r.hotReload.RegisterCompileResult(kind, result)
	r.hotReload.RegisterClientBundles(result.ClientBundles)
	logs.Info(
		"hotReload index updated: kind=%s entry=%s dependencies=%d",
		kind,
		result.EntryPath,
		len(result.Dependencies),
	)
	if len(result.ClientBundles) > 0 {
		logs.Info("client hotReload index updated: bundles=%d", len(result.ClientBundles))
	}
}
