package serverruntime

import (
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/watcher"
)

func (r *Runtime) startWatcher() error {
	if r.ProjectRoot == "" {
		return newRuntimeConfigError("watcher-project-root-missing", "watcher requires project root", nil)
	}

	w, err := watcher.New(r.ProjectRoot)
	if err != nil {
		return newRuntimeConfigError("watcher-start-failed", "watcher start failed", err)
	}

	if err := r.subscribeCache404Watcher(w); err != nil {
		_ = w.Close()
		return err
	}

	if err := r.subscribeHotReloadWatcher(w); err != nil {
		_ = w.Close()
		return err
	}

	if err := w.WatchRecursive("."); err != nil {
		_ = w.Close()
		return newRuntimeConfigError("watcher-recursive-setup-failed", "watcher recursive setup failed", err)
	}

	w.Start()
	r.watcher = w

	logs.Info("watcher started: %s", r.ProjectRoot)

	return nil
}
