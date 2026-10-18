package watcher

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/gox-lang/server/internal/logs"

	"github.com/fsnotify/fsnotify"
)

const defaultDebounce = 120 * time.Millisecond

type Option func(*Watcher)

type subscription struct {
	filter  *regexp.Regexp
	handler Handler
}

type Watcher struct {
	root     string
	fs       *fsnotify.Watcher
	debounce time.Duration

	subsMu sync.RWMutex
	subs   []subscription

	pendingMu sync.Mutex
	timers    map[string]*time.Timer

	closeOnce sync.Once
	done      chan struct{}
}

func New(root string, opts ...Option) (*Watcher, error) {
	if root == "" {
		return nil, errors.New("watcher root is required")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		root:     absRoot,
		fs:       fs,
		debounce: defaultDebounce,
		timers:   make(map[string]*time.Timer),
		done:     make(chan struct{}),
	}

	for _, opt := range opts {
		opt(w)
	}

	if w.debounce <= 0 {
		w.debounce = defaultDebounce
	}

	return w, nil
}

func WithDebounce(duration time.Duration) Option {
	return func(w *Watcher) {
		w.debounce = duration
	}
}

func (w *Watcher) Subscribe(filter string, handler Handler) error {
	if handler == nil {
		return errors.New("watcher handler is required")
	}

	re, err := regexp.Compile(filter)
	if err != nil {
		return err
	}

	w.subsMu.Lock()
	w.subs = append(w.subs, subscription{
		filter:  re,
		handler: handler,
	})
	w.subsMu.Unlock()

	return nil
}

func (w *Watcher) Watch(rel string) error {
	abs, err := w.abs(rel)
	if err != nil {
		return err
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			logs.Warm("watch path does not exist: %s", rel)
			return nil
		}

		return err
	}

	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}

	return w.fs.Add(abs)
}

func (w *Watcher) WatchRecursive(rel string) error {
	abs, err := w.abs(rel)
	if err != nil {
		return err
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			logs.Warm("watch path does not exist: %s", rel)
			return nil
		}

		return err
	}

	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}

	return filepath.WalkDir(abs, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			logs.Warm("watch walk failed for %s: %v", path, walkErr)
			return nil
		}

		if !entry.IsDir() {
			return nil
		}

		if err := w.fs.Add(path); err != nil {
			logs.Warm("watch add failed for %s: %v", path, err)
		}

		return nil
	})
}

func (w *Watcher) Start() {
	go w.loop()
}

func (w *Watcher) Close() error {
	var err error

	w.closeOnce.Do(func() {
		close(w.done)
		err = w.fs.Close()
	})

	return err
}

func (w *Watcher) loop() {
	for {
		select {
		case <-w.done:
			return

		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}

			logs.Warm("watch error: %v", err)

		case raw, ok := <-w.fs.Events:
			if !ok {
				return
			}

			w.onRawEvent(raw)
		}
	}
}

func (w *Watcher) onRawEvent(raw fsnotify.Event) {
	event, ok := w.normalize(raw)
	if !ok {
		return
	}

	if event.Exists && event.IsDir && event.Op == OpCreate {
		if err := w.WatchRecursive(event.Rel); err != nil {
			logs.Warm("watch recursive add failed for %s: %v", event.Rel, err)
		}
	}

	w.debounceEvent(event)
}

func (w *Watcher) debounceEvent(event Event) {
	key := event.Rel
	if key == "" {
		return
	}

	w.pendingMu.Lock()

	if timer, ok := w.timers[key]; ok {
		timer.Stop()
	}

	w.timers[key] = time.AfterFunc(w.debounce, func() {
		w.pendingMu.Lock()
		delete(w.timers, key)
		w.pendingMu.Unlock()

		w.emit(event)
	})

	w.pendingMu.Unlock()
}

func (w *Watcher) emit(event Event) {
	w.subsMu.RLock()
	subs := make([]subscription, len(w.subs))
	copy(subs, w.subs)
	w.subsMu.RUnlock()

	for _, sub := range subs {
		if sub.filter.MatchString(event.Rel) {
			sub.handler(event)
		}
	}
}

func (w *Watcher) normalize(raw fsnotify.Event) (Event, bool) {
	rel, err := filepath.Rel(w.root, raw.Name)
	if err != nil {
		return Event{}, false
	}

	if rel == "." || rel == "" {
		return Event{}, false
	}

	if rel == ".." || len(rel) >= 3 && rel[:3] == "../" {
		return Event{}, false
	}

	rel = filepath.ToSlash(rel)

	exists := false
	isDir := false

	info, err := os.Stat(raw.Name)
	if err == nil {
		exists = true
		isDir = info.IsDir()
	}

	return Event{
		Path:   raw.Name,
		Rel:    rel,
		Op:     mapOp(raw.Op),
		Exists: exists,
		IsDir:  isDir,
		Time:   time.Now(),
	}, true
}

func (w *Watcher) abs(rel string) (string, error) {
	if rel == "" || rel == "." {
		return w.root, nil
	}

	if filepath.IsAbs(rel) {
		return rel, nil
	}

	return filepath.Join(w.root, rel), nil
}

func mapOp(op fsnotify.Op) Op {
	switch {
	case op&fsnotify.Create == fsnotify.Create:
		return OpCreate
	case op&fsnotify.Write == fsnotify.Write:
		return OpWrite
	case op&fsnotify.Remove == fsnotify.Remove:
		return OpRemove
	case op&fsnotify.Rename == fsnotify.Rename:
		return OpRename
	case op&fsnotify.Chmod == fsnotify.Chmod:
		return OpChmod
	default:
		return OpUnknown
	}
}
