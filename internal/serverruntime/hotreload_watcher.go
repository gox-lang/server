package serverruntime

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/watcher"
)

func (r *Runtime) subscribeHotReloadWatcher(w *watcher.Watcher) error {
	if err := w.Subscribe(`^pages(?:/.*)?$`, r.handlePageTreeEvent); err != nil {
		return newRuntimeConfigError("hotreload-watcher-subscribe-failed", "page tree watcher subscribe failed", err)
	}

	if err := w.Subscribe(`.*\.gox$`, r.handleHotReloadEvent); err != nil {
		return newRuntimeConfigError("hotreload-watcher-subscribe-failed", "hotReload watcher subscribe failed", err)
	}

	if err := w.Subscribe(`.*\.go$`, r.handleGoSourceHotReloadEvent); err != nil {
		return newRuntimeConfigError("hotreload-watcher-subscribe-failed", "Go source hotReload watcher subscribe failed", err)
	}

	if err := w.Subscribe(`^(client/[^/]+|public|component)/.*`, r.handleClientHotReloadEvent); err != nil {
		return newRuntimeConfigError("hotreload-watcher-subscribe-failed", "client hotReload watcher subscribe failed", err)
	}

	return nil
}

// Removing or moving a directory need not produce events for its children.
// IsDir is also unavailable once the watcher can no longer stat the old path.
func (r *Runtime) handlePageTreeEvent(event watcher.Event) {
	if event.Exists && event.IsDir {
		if r.cache404 != nil {
			r.cache404.Clear()
		}
		return
	}
	if !isHotReloadDeleteEvent(event) {
		return
	}
	path := cleanGoxAbsPath(event.Path)
	if path == "" {
		return
	}
	prefix := path + string(filepath.Separator)
	var entries []hotreload.ReloadEntry
	r.goxMu.RLock()
	for _, route := range r.goxRoutes {
		if route == nil {
			continue
		}
		source := cleanGoxAbsPath(route.AbsPath)
		if !strings.HasPrefix(source, prefix) {
			continue
		}
		// Atomic replacements can recreate a directory before the debounce expires.
		if _, err := os.Stat(source); !os.IsNotExist(err) {
			continue
		}
		entries = append(entries, hotreload.ReloadEntry{Kind: hotreload.ReloadPage, EntryPath: route.RelPath, EntryAbsPath: source})
	}
	r.goxMu.RUnlock()
	for _, entry := range entries {
		r.unmountGoxRouteByEntry(entry, event)
	}
	if len(entries) > 0 && r.cache404 != nil {
		r.cache404.Clear()
	}
}
