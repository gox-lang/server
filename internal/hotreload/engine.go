package hotreload

import (
	"fmt"

	"github.com/gox-lang/server/internal/watcher"
)

type Handler func(entry ReloadEntry, event watcher.Event) error

type Engine struct {
	index    *Index
	handlers map[ReloadKind]Handler
}

func NewEngine(index *Index) *Engine {
	return &Engine{
		index:    index,
		handlers: make(map[ReloadKind]Handler),
	}
}

func (e *Engine) Register(kind ReloadKind, handler Handler) {
	if e == nil || handler == nil {
		return
	}

	e.handlers[kind] = handler
}

func (e *Engine) Handle(event watcher.Event) (int, error) {
	if e == nil {
		return 0, fmt.Errorf("hotReload engine is not initialized")
	}

	if event.IsDir {
		return 0, nil
	}

	if e.index == nil {
		return 0, fmt.Errorf("hotReload index is not initialized")
	}

	dependents, ok := e.index.Dependents(event.Path)
	if !ok || len(dependents.Entries) == 0 {
		return 0, nil
	}

	entries := dedupeEntries(dependents.Entries)
	reloaded := 0

	for _, entry := range entries {
		handler := e.handlers[entry.Kind]
		if handler == nil {
			return reloaded, fmt.Errorf("hotReload handler is not registered for kind=%s", entry.Kind)
		}

		if err := handler(entry, event); err != nil {
			return reloaded, err
		}

		reloaded++
	}

	return reloaded, nil
}

func dedupeEntries(entries []ReloadEntry) []ReloadEntry {
	if len(entries) < 2 {
		return entries
	}

	out := make([]ReloadEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))

	for _, entry := range entries {
		key := string(entry.Kind) + "\x00" + entry.EntryAbsPath
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		out = append(out, entry)
	}

	return out
}
