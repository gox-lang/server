package serverruntime

import (
	"context"

	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/logs"
	goxruntime "github.com/gox-lang/server/internal/runtime"
	"github.com/gox-lang/server/internal/scaffold"
	"github.com/gox-lang/server/internal/watcher"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

func (r *Runtime) ReloadConfig() error {
	return r.reloadConfig()
}

func (r *Runtime) reloadConfigEntry(entry hotreload.ReloadEntry, event watcher.Event) error {
	return r.reloadConfig()
}

func (r *Runtime) reloadConfig() error {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	if r.project == nil {
		return newRuntimeConfigError("config-reload-before-init", "reload config failed: project is not initialized", nil)
	}

	loaded, err := goxruntime.CompileConfigPlugin(r.project, r.paths, r.Out)
	if err != nil {
		return err
	}
	r.configOverrides.apply(&loaded.Config)

	state, err := NewRuntimeState(loaded.Config, r.project.RootDir, loaded.CompileResult)
	if err != nil {
		return err
	}

	if loaded.Config.WebSocket.Enabled {
		if err := scaffold.EnsureWebSocket(r.project.RootDir); err != nil {
			return err
		}
	}

	// A config-only reload does not rebuild page/client entries. Keep the currently
	// loaded client asset index so pages that were already compiled can still
	// inject and serve their /_gox/client assets until a page/client hot reload
	// replaces the affected bundles.
	if previous := r.runtimeState.Load(); previous != nil && previous.ClientAssets != nil {
		if state.ClientAssets == nil || state.ClientAssets.IsEmpty() {
			state.ClientAssets = previous.ClientAssets
		}
	}
	precompressClientAssets(r.project.RootDir, loaded.Config.Static.Compression, state.ClientAssets)

	var sessionStorage server.SessionStorageRuntime
	var clusterRuntime server.ClusterRuntime
	if state.Session != nil {
		storage, cluster, err := OpenSessionRuntime(loaded.Config.Cluster, state.Session, r.instanceID, loaded.Config.WebSocket.MaxMessageSize)
		if err != nil {
			return newRuntimeConfigError("cluster-open-failed", "open cluster or session fallback failed", err)
		}
		sessionStorage = storage
		clusterRuntime = cluster
	}
	restart, err := r.prepareTransportReload(state.Config.HTTP)
	if err != nil || restart {
		if clusterRuntime != nil {
			_ = clusterRuntime.Close()
		} else if sessionStorage != nil {
			_ = sessionStorage.Close()
		}
		if err != nil {
			return newRuntimeConfigError("http-reload-rejected", "HTTP/TLS reload rejected; keeping active configuration", err)
		}
		return nil
	}

	sessionExpireCancel := r.startSessionExpireSubscription(sessionStorage, state.Session)
	var clusterEventCancel context.CancelFunc
	if clusterRuntime != nil {
		clusterEventCancel = r.startClusterEventSubscription(clusterRuntime)
	}

	r.requestMu.Lock()
	// Preserve bundles registered while the new configuration was compiling.
	if current := r.runtimeState.Load(); current != nil && current.ClientAssets != nil {
		state.ClientAssets = current.ClientAssets
	}
	oldGeneration := r.requestGeneration
	r.sessionStorageMu.Lock()
	oldSessionStorage := r.sessionStorage
	oldClusterRuntime := r.clusterRuntime
	oldSessionExpireCancel := r.sessionExpireCancel
	oldClusterEventCancel := r.clusterEventCancel
	r.clusterRequestCoordinator().cancelAll(errClusterRequestReloaded)

	r.sessionStorage = sessionStorage
	r.clusterRuntime = clusterRuntime
	r.sessionExpireCancel = sessionExpireCancel
	r.clusterEventCancel = clusterEventCancel
	r.sessionStorageMu.Unlock()
	r.requestGeneration = &requestGeneration{storage: sessionStorage}
	r.Config = loaded.Config
	r.runtimeState.Store(state)
	r.cache404.Clear()
	r.requestMu.Unlock()
	r.synchronizeSocketDirectory()
	r.synchronizeGroupDirectory()
	r.synchronizeChannelDirectory()

	cleanup := func() {
		if oldSessionExpireCancel != nil {
			oldSessionExpireCancel()
		}
		if oldClusterEventCancel != nil {
			oldClusterEventCancel()
		}
		if oldClusterRuntime != nil {
			_ = oldClusterRuntime.Close()
		} else if oldSessionStorage != nil {
			_ = oldSessionStorage.Close()
		}
	}
	if oldGeneration != nil {
		oldGeneration.retire(cleanup)
	} else {
		cleanup()
	}

	r.registerHotReloadCompileResult(hotreload.ReloadConfig, loaded.CompileResult)
	if err := goxruntime.CleanupConfigBuildArtifacts(r.project.RootDir, loaded); err != nil {
		return newRuntimeConfigError("config-build-cleanup-failed", "clean stale config build artifacts failed", err)
	}

	logs.Info("config reloaded from app/config.gox")

	if state.PreRouter != nil {
		state.PreRouter.LogLoaded()
	}

	return nil
}

func (r *Runtime) reloadPageEntry(entry hotreload.ReloadEntry, event watcher.Event) error {
	if isHotReloadDeleteEvent(event) {
		route, ok := r.unmountGoxRouteByEntry(entry, event)
		if !ok {
			logs.Warm(
				"page hotReload unmount entry not loaded: changed=%s entry=%s",
				event.Rel,
				entry.EntryPath,
			)
			return nil
		}

		logs.Info(
			"page hotReload unmounted: changed=%s entry=%s route=%s",
			event.Rel,
			entry.EntryPath,
			route.RoutePath,
		)

		return nil
	}

	route, ok, err := r.rebuildGoxRouteByEntry(entry)
	if !ok {
		logs.Warm(
			"page hotReload entry not loaded: changed=%s entry=%s",
			event.Rel,
			entry.EntryPath,
		)
		return nil
	}
	if err != nil {
		if goxErr := asGoxError(err, gox.GoxErrorBuild, route, nil, "page hotReload rebuild failed"); goxErr != nil {
			goxErr.AddDetail("hotReload.changed", event.Rel)
			goxErr.AddDetail("hotReload.entry", entry.EntryPath)
			return goxErr
		}
		return err
	}

	logs.Info(
		"page hotReload rebuilt: changed=%s entry=%s route=%s version=%s",
		event.Rel,
		entry.EntryPath,
		route.RoutePath,
		route.BuildVersion,
	)

	return nil
}

func isHotReloadDeleteEvent(event watcher.Event) bool {
	return !event.Exists || event.Op == watcher.OpRemove || event.Op == watcher.OpRename
}
