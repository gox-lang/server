package serverruntime

import (
	"path/filepath"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/watcher"
)

func (r *Runtime) handleGoSourceHotReloadEvent(event watcher.Event) {
	if shouldIgnoreGoSourceHotReloadEvent(event) || r == nil || r.hotReload == nil {
		return
	}

	goxcompiler.InvalidateProjectGoGoroutineSafety(r.projectRoot(), event.Path)

	dependents, ok := r.hotReload.Dependents(event.Path)
	if !ok || len(dependents.Entries) == 0 {
		return
	}

	request := RestartRequest{
		Reason:          RestartReasonGoSourceChanged,
		ChangedPath:     dependents.FileAbsPath,
		ChangedRelPath:  filepath.ToSlash(strings.TrimSpace(event.Rel)),
		AffectedEntries: dependents.Entries,
	}
	if !r.requestRestart(request) {
		return
	}

	logs.Info(
		"server restart queued after Go source change: changed=%s affected=%d",
		request.ChangedRelPath,
		len(request.AffectedEntries),
	)
}

func shouldIgnoreGoSourceHotReloadEvent(event watcher.Event) bool {
	if event.IsDir || event.Op == watcher.OpChmod || event.Op == watcher.OpUnknown {
		return true
	}

	rel := filepath.ToSlash(strings.TrimSpace(event.Rel))
	if rel == "" || !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
		return true
	}

	for _, part := range strings.Split(rel, "/") {
		switch part {
		case ".build", ".git", "node_modules", ".toolchain", "vendor":
			return true
		}
	}
	return false
}
