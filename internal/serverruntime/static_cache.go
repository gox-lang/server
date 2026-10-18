package serverruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gox-lang/server/internal/logs"
)

const staticCacheVersion = 1

type StaticCache struct {
	rootDir string
	pathDir string
	path    string

	mu      sync.RWMutex
	entries map[string]*staticCacheEntry

	dirty     atomic.Bool
	requireGC atomic.Bool
	stop      chan struct{}
}

type staticCacheEntry struct {
	Exists        bool                           `json:"exists"`
	MTimeUnixNano int64                          `json:"mtimeUnixNano"`
	Size          int64                          `json:"size"`
	ETag          string                         `json:"etag,omitempty"`
	ContentType   string                         `json:"contentType,omitempty"`
	Encodings     map[string]staticCacheEncoding `json:"encodings,omitempty"`

	HasChanged bool `json:"-"`
	HasDeleted bool `json:"-"`
}

type staticCacheEncoding struct {
	Path          string `json:"path"`
	Level         int    `json:"level"`
	Size          int64  `json:"size"`
	MTimeUnixNano int64  `json:"mtimeUnixNano"`
	ETag          string `json:"etag,omitempty"`
}

type staticCacheFile struct {
	Version int                          `json:"version"`
	Entries map[string]*staticCacheEntry `json:"entries"`
}

type staticCacheRawFile struct {
	Version int                        `json:"version"`
	Entries map[string]json.RawMessage `json:"entries"`
}

func LoadStaticCache(rootDir string) *StaticCache {
	cache := &StaticCache{
		rootDir: rootDir,
		pathDir: filepath.Join(rootDir, ".build", "cache", "statics"),
		path:    filepath.Join(rootDir, ".build", "cache", "staticCache.json"),
		entries: make(map[string]*staticCacheEntry),
		stop:    make(chan struct{}),
	}

	if err := cache.load(); err != nil {
		logs.Warm("static cache load failed: %v", err)
	}

	loaded := cache.Len()
	cache.reconcileFS()
	reconciled := cache.Len()
	logs.Info("[cache] static loaded (%d entries, %d reconcile)", loaded, reconciled)

	cache.startWorkers()

	return cache
}

func (c *StaticCache) Len() int {
	c.mu.RLock()
	n := len(c.entries)
	c.mu.RUnlock()

	return n
}

func (c *StaticCache) Close() {
	select {
	case <-c.stop:
		return
	default:
		close(c.stop)
	}
	c.persistIfDirty()
	c.runGCIfRequired()
}

func (c *StaticCache) SetSource(routePath string, info os.FileInfo, etag string, contentType string) {
	if c == nil || info == nil {
		return
	}
	routePath = normalizePreRoutePath(routePath)
	if routePath == "" {
		return
	}

	mtime := info.ModTime().UnixNano()
	size := info.Size()

	c.mu.Lock()
	entry, ok := c.entries[routePath]
	if !ok {
		c.entries[routePath] = &staticCacheEntry{
			Exists:        true,
			MTimeUnixNano: mtime,
			Size:          size,
			ETag:          etag,
			ContentType:   contentType,
			HasChanged:    true,
		}
		c.mu.Unlock()
		c.dirty.Store(true)
		c.requireGC.Store(true)
		return
	}

	changed := entry.MTimeUnixNano != mtime || entry.Size != size || entry.HasDeleted || !entry.Exists
	if changed {
		entry.Encodings = nil
		entry.HasChanged = true
	}
	if changed || entry.ETag != etag || entry.ContentType != contentType {
		entry.Exists = true
		entry.MTimeUnixNano = mtime
		entry.Size = size
		entry.ETag = etag
		entry.ContentType = contentType
		entry.HasDeleted = false
		c.mu.Unlock()
		c.dirty.Store(true)
		if changed {
			c.requireGC.Store(true)
		}
		return
	}
	c.mu.Unlock()
}

func (c *StaticCache) Set(routePath string, modTime string) {
	mtime, err := strconv.ParseInt(modTime, 10, 64)
	if err != nil || mtime <= 0 {
		return
	}
	routePath = normalizePreRoutePath(routePath)
	if routePath == "" {
		return
	}

	c.mu.Lock()
	entry, ok := c.entries[routePath]
	if !ok {
		c.entries[routePath] = &staticCacheEntry{Exists: true, MTimeUnixNano: mtime, HasChanged: true}
		c.mu.Unlock()
		c.dirty.Store(true)
		c.requireGC.Store(true)
		return
	}
	if entry.MTimeUnixNano != mtime || entry.HasDeleted || !entry.Exists {
		entry.Exists = true
		entry.MTimeUnixNano = mtime
		entry.HasChanged = true
		entry.HasDeleted = false
		entry.Encodings = nil
		c.mu.Unlock()
		c.dirty.Store(true)
		c.requireGC.Store(true)
		return
	}
	c.mu.Unlock()
}

func (c *StaticCache) SetEncoding(routePath string, sourceInfo os.FileInfo, encoding string, level int, artifactPath string, artifactInfo os.FileInfo, etag string) {
	if c == nil || sourceInfo == nil || artifactInfo == nil || encoding == "" || artifactPath == "" {
		return
	}
	routePath = normalizePreRoutePath(routePath)
	if routePath == "" {
		return
	}

	relPath, err := filepath.Rel(c.rootDir, artifactPath)
	if err != nil || strings.HasPrefix(relPath, "..") || filepath.IsAbs(relPath) {
		return
	}
	relPath = filepath.ToSlash(relPath)

	mtime := sourceInfo.ModTime().UnixNano()
	size := sourceInfo.Size()
	key := staticCacheEncodingKey(encoding, level)
	enc := staticCacheEncoding{
		Path:          relPath,
		Level:         level,
		Size:          artifactInfo.Size(),
		MTimeUnixNano: artifactInfo.ModTime().UnixNano(),
		ETag:          etag,
	}

	c.mu.Lock()
	entry, ok := c.entries[routePath]
	if !ok {
		entry = &staticCacheEntry{Exists: true, MTimeUnixNano: mtime, Size: size}
		c.entries[routePath] = entry
	}
	if entry.MTimeUnixNano != mtime || entry.Size != size || entry.HasDeleted || !entry.Exists {
		entry.Exists = true
		entry.MTimeUnixNano = mtime
		entry.Size = size
		entry.Encodings = nil
		entry.HasChanged = true
		entry.HasDeleted = false
	}
	if entry.Encodings == nil {
		entry.Encodings = make(map[string]staticCacheEncoding)
	}
	old, same := entry.Encodings[key]
	if !same || old != enc {
		entry.Encodings[key] = enc
		c.mu.Unlock()
		c.dirty.Store(true)
		return
	}
	c.mu.Unlock()
}

func (c *StaticCache) GetEncoding(routePath string, sourceInfo os.FileInfo, encoding string, level int) (string, os.FileInfo, bool) {
	if c == nil || sourceInfo == nil || encoding == "" {
		return "", nil, false
	}
	routePath = normalizePreRoutePath(routePath)
	if routePath == "" {
		return "", nil, false
	}

	key := staticCacheEncodingKey(encoding, level)
	mtime := sourceInfo.ModTime().UnixNano()
	size := sourceInfo.Size()

	c.mu.RLock()
	entry := c.entries[routePath]
	if entry == nil || !entry.Exists || entry.HasDeleted || entry.MTimeUnixNano != mtime || entry.Size != size {
		c.mu.RUnlock()
		return "", nil, false
	}
	enc, ok := entry.Encodings[key]
	c.mu.RUnlock()
	if !ok || enc.Path == "" {
		return "", nil, false
	}

	artifactPath := filepath.Join(c.rootDir, filepath.FromSlash(enc.Path))
	info, err := os.Stat(artifactPath)
	if err != nil || info.IsDir() || info.Size() != enc.Size {
		c.removeEncoding(routePath, key)
		return "", nil, false
	}
	return artifactPath, info, true
}

func (c *StaticCache) removeEncoding(routePath string, key string) {
	c.mu.Lock()
	entry := c.entries[routePath]
	if entry != nil && entry.Encodings != nil {
		delete(entry.Encodings, key)
	}
	c.mu.Unlock()
	c.dirty.Store(true)
}

func staticCacheEncodingKey(encoding string, level int) string {
	if level <= 0 {
		return encoding
	}
	return fmt.Sprintf("%s:%d", encoding, level)
}

func (c *StaticCache) MarkDeleted(routePath string) {
	routePath = normalizePreRoutePath(routePath)
	if routePath == "" {
		return
	}

	c.mu.Lock()
	entry, ok := c.entries[routePath]
	if !ok {
		c.entries[routePath] = &staticCacheEntry{Exists: false, HasDeleted: true, HasChanged: true}
		c.mu.Unlock()
		c.dirty.Store(true)
		c.requireGC.Store(true)
		return
	}
	entry.Exists = false
	entry.HasDeleted = true
	entry.HasChanged = true
	c.mu.Unlock()

	c.dirty.Store(true)
	c.requireGC.Store(true)
}

func (c *StaticCache) load() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var raw staticCacheRawFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Version != staticCacheVersion {
		logs.Warm("ignoring unsupported static cache version: %d", raw.Version)
		return nil
	}

	loaded := make(map[string]*staticCacheEntry, len(raw.Entries))
	for routePath, rawEntry := range raw.Entries {
		routePath = normalizePreRoutePath(routePath)
		if routePath == "" || len(rawEntry) == 0 {
			continue
		}

		var oldModTime string
		if err := json.Unmarshal(rawEntry, &oldModTime); err == nil {
			mtime, err := strconv.ParseInt(oldModTime, 10, 64)
			if err == nil && mtime > 0 {
				loaded[routePath] = &staticCacheEntry{Exists: true, MTimeUnixNano: mtime}
			}
			continue
		}

		var entry staticCacheEntry
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			continue
		}
		if !entry.Exists && !entry.HasDeleted {
			// Keep explicit negative entries, but ignore zero-value accidental entries.
			if entry.MTimeUnixNano == 0 && entry.Size == 0 && len(entry.Encodings) == 0 {
				continue
			}
		}
		entry.HasChanged = false
		entry.HasDeleted = false
		loaded[routePath] = &entry
	}

	c.mu.Lock()
	c.entries = loaded
	c.mu.Unlock()

	return nil
}

func (c *StaticCache) snapshot() staticCacheFile {
	c.mu.RLock()
	paths := make([]string, 0, len(c.entries))
	for routePath := range c.entries {
		paths = append(paths, routePath)
	}
	sort.Strings(paths)

	entries := make(map[string]*staticCacheEntry, len(paths))
	for _, routePath := range paths {
		entry := c.entries[routePath]
		if entry == nil || entry.HasDeleted || !entry.Exists || entry.MTimeUnixNano <= 0 {
			continue
		}
		copyEntry := *entry
		copyEntry.HasChanged = false
		copyEntry.HasDeleted = false
		if len(entry.Encodings) > 0 {
			copyEntry.Encodings = make(map[string]staticCacheEncoding, len(entry.Encodings))
			for k, v := range entry.Encodings {
				copyEntry.Encodings[k] = v
			}
		}
		entries[routePath] = &copyEntry
	}
	c.mu.RUnlock()

	return staticCacheFile{Version: staticCacheVersion, Entries: entries}
}

func (c *StaticCache) persistIfDirty() {
	if !c.dirty.Swap(false) {
		return
	}

	file := c.snapshot()
	if err := c.persist(file); err != nil {
		c.dirty.Store(true)
		logs.Warm("static cache persist failed: %v", err)
	}
}

func (c *StaticCache) persist(file staticCacheFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

func (c *StaticCache) startWorkers() {
	go func() {
		persistTicker := time.NewTicker(10 * time.Second)
		gcTicker := time.NewTicker(time.Minute)
		defer persistTicker.Stop()
		defer gcTicker.Stop()

		for {
			select {
			case <-persistTicker.C:
				c.persistIfDirty()
			case <-gcTicker.C:
				c.runGCIfRequired()
			case <-c.stop:
				return
			}
		}
	}()
}

func (c *StaticCache) runGCIfRequired() {
	if !c.requireGC.Swap(false) {
		return
	}
	c.runGC()
}

func (c *StaticCache) runGC() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for routePath, entry := range c.entries {
		cachePath := filepath.Join(c.pathDir, strings.TrimPrefix(routePath, "/"))
		if entry.HasDeleted || !entry.Exists {
			_ = os.RemoveAll(cachePath)
			delete(c.entries, routePath)
			continue
		}
		if !entry.HasChanged {
			continue
		}
		versions, err := os.ReadDir(cachePath)
		if err != nil {
			entry.HasChanged = false
			continue
		}
		current := strconv.FormatInt(entry.MTimeUnixNano, 10)
		for _, version := range versions {
			if !version.IsDir() || version.Name() == current {
				continue
			}
			_ = os.RemoveAll(filepath.Join(cachePath, version.Name()))
		}
		entry.HasChanged = false
	}
}

func (c *StaticCache) reconcileFS() {
	_ = filepath.WalkDir(c.pathDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == c.pathDir {
			return nil
		}

		rel, err := filepath.Rel(c.pathDir, p)
		if err != nil || rel == "." {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 2 {
			return nil
		}

		modTime := parts[len(parts)-1]
		if !isStaticCacheVersionDir(modTime) {
			return nil
		}

		routePath := "/" + strings.Join(parts[:len(parts)-1], "/")
		mtime, _ := strconv.ParseInt(modTime, 10, 64)

		c.mu.RLock()
		entry, ok := c.entries[routePath]
		c.mu.RUnlock()

		if !ok || !entry.Exists || entry.MTimeUnixNano != mtime {
			_ = os.RemoveAll(p)
			return filepath.SkipDir
		}
		return nil
	})

	c.removeEmptyDirs(c.pathDir)
}

func (c *StaticCache) removeEmptyDirs(root string) {
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == root {
			return nil
		}
		entries, err := os.ReadDir(p)
		if err == nil && len(entries) == 0 {
			_ = os.Remove(p)
		}
		return nil
	})
}

func isStaticCacheVersionDir(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
