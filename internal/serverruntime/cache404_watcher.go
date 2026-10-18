package serverruntime

import (
	"path"
	"strings"

	"github.com/gox-lang/server/internal/goxsource"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/watcher"
)

func (r *Runtime) subscribeCache404Watcher(w *watcher.Watcher) error {
	if err := w.Subscribe(`^(pages|public)/`, r.onCache404WatchEvent); err != nil {
		return newRuntimeConfigError("cache404-watcher-subscribe-failed", "cache404 watcher subscribe failed", err)
	}

	return nil
}

func (r *Runtime) onCache404WatchEvent(event watcher.Event) {
	if !event.Exists || event.IsDir {
		return
	}

	route, ok := r.cache404RouteFromWatchEvent(event)
	if !ok {
		return
	}

	// Aliases and wildcard routes can map several URLs to this resource.
	r.cache404.Clear()
	logs.Info("cache404 invalidated: %s", route)
}

func (r *Runtime) cache404RouteFromWatchEvent(event watcher.Event) (string, bool) {
	switch {
	case strings.HasPrefix(event.Rel, "pages/"):
		return pageRelToRoute(event.Rel)

	case strings.HasPrefix(event.Rel, "public/"):
		return staticRelToRoute(event.Rel, r.state().Config.Static.Path)

	default:
		return "", false
	}
}

func pageRelToRoute(rel string) (string, bool) {
	return goxsource.PublicPageRoute(rel)
}

func staticRelToRoute(rel string, staticPath string) (string, bool) {
	staticPath = strings.Trim(staticPath, "/")
	if staticPath == "" {
		staticPath = "public"
	}

	prefix := staticPath + "/"
	if !strings.HasPrefix(rel, prefix) {
		return "", false
	}

	value := strings.TrimPrefix(rel, prefix)
	value = strings.Trim(value, "/")
	if value == "" {
		return "", false
	}

	route := path.Join("/", value)
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}

	return route, true
}
