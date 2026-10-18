package serverruntime

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gox-lang/server/internal/logs"
)

const (
	dynamicWarmupCacheVersion  = 1
	dynamicWarmupCacheFileName = "dynamicWarmupCache.json"
	dynamicWarmupFlushInterval = time.Minute
	dynamicWarmupMaxPaths      = 10
	dynamicWarmupRatio         = 0.20
)

type DynamicWarmupCache struct {
	path string
	mu   sync.Mutex

	started  atomic.Bool
	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

type dynamicWarmupCacheFile struct {
	Version     int                      `json:"version"`
	GeneratedAt string                   `json:"generatedAt"`
	Pages       []dynamicWarmupCachePath `json:"pages,omitempty"`
	Channels    []dynamicWarmupCachePath `json:"channels,omitempty"`
	// Paths reads the original version 1 layout. New writes always use the two
	// independent rankings above.
	Paths []dynamicWarmupCachePath `json:"paths,omitempty"`
}

type dynamicWarmupSnapshot struct {
	Pages    []dynamicWarmupCachePath
	Channels []dynamicWarmupCachePath
}

type dynamicWarmupSelection struct {
	Pages    []string
	Channels []string
}

type dynamicWarmupCachePath struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

func LoadDynamicWarmupCache(rootDir string) *DynamicWarmupCache {
	return &DynamicWarmupCache{
		path:   dynamicWarmupCacheFilePath(rootDir),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

func dynamicWarmupCacheFilePath(rootDir string) string {
	return filepath.Join(rootDir, ".build", "cache", dynamicWarmupCacheFileName)
}

func (c *DynamicWarmupCache) Start(r *Runtime) {
	if c == nil || r == nil || !c.started.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer close(c.doneCh)

		ticker := time.NewTicker(dynamicWarmupFlushInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := c.FlushRuntime(r); err != nil {
					logs.Warm("dynamic warmup cache persist failed: %v", err)
				}
			case <-c.stopCh:
				if err := c.FlushRuntime(r); err != nil {
					logs.Warm("dynamic warmup cache persist failed: %v", err)
				}
				return
			}
		}
	}()
}

func (c *DynamicWarmupCache) Close() {
	if c == nil || !c.started.Load() {
		return
	}
	c.stopOnce.Do(func() {
		close(c.stopCh)
		<-c.doneCh
	})
}

func (c *DynamicWarmupCache) WarmupPaths() dynamicWarmupSelection {
	if c == nil || c.path == "" {
		return dynamicWarmupSelection{}
	}

	file, ok := c.load()
	if !ok {
		return dynamicWarmupSelection{}
	}
	pages := file.Pages
	channels := file.Channels
	if len(pages) == 0 && len(channels) == 0 && len(file.Paths) > 0 {
		pages, channels = splitDynamicWarmupPaths(file.Paths)
	}
	return dynamicWarmupSelection{
		Pages:    selectDynamicWarmupPaths(pages),
		Channels: selectDynamicWarmupPaths(channels),
	}
}

func selectDynamicWarmupPaths(paths []dynamicWarmupCachePath) []string {
	paths = normalizeDynamicWarmupPaths(paths)
	limit := dynamicWarmupSelectCount(len(paths))
	out := make([]string, 0, limit)
	for i := 0; i < limit && i < len(paths); i++ {
		out = append(out, paths[i].Path)
	}
	return out
}

func (c *DynamicWarmupCache) FlushRuntime(r *Runtime) error {
	if c == nil || r == nil {
		return nil
	}
	snapshot := r.snapshotAndResetDynamicWarmupCounts()
	return c.persistSnapshot(snapshot)
}

func (c *DynamicWarmupCache) FlushRuntimeForRestart(r *Runtime, request RestartRequest) error {
	if c == nil || r == nil {
		return nil
	}
	snapshot := r.snapshotAndResetDynamicWarmupCounts()
	snapshot = r.promoteRestartWarmupEntries(snapshot, request.AffectedEntries)
	return c.persistSnapshot(snapshot)
}

func (c *DynamicWarmupCache) persistSnapshot(snapshot dynamicWarmupSnapshot) error {
	if c == nil || c.path == "" || (len(snapshot.Pages) == 0 && len(snapshot.Channels) == 0) {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	pages := normalizeDynamicWarmupPaths(snapshot.Pages)
	channels := normalizeDynamicWarmupPaths(snapshot.Channels)
	if len(pages) == 0 && len(channels) == 0 {
		return nil
	}

	file := dynamicWarmupCacheFile{
		Version:     dynamicWarmupCacheVersion,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Pages:       pages,
		Channels:    channels,
	}

	if err := os.MkdirAll(filepath.Dir(c.path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".dynamicWarmupCache-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, c.path)
}

// persistPaths keeps internal callers and version 1 tests readable while
// classifying every route into the correct independent ranking.
func (c *DynamicWarmupCache) persistPaths(paths []dynamicWarmupCachePath) error {
	pages, channels := splitDynamicWarmupPaths(paths)
	return c.persistSnapshot(dynamicWarmupSnapshot{Pages: pages, Channels: channels})
}

func splitDynamicWarmupPaths(paths []dynamicWarmupCachePath) (pages, channels []dynamicWarmupCachePath) {
	for _, item := range paths {
		if isChannelRoutePath(strings.TrimSpace(item.Path)) {
			channels = append(channels, item)
		} else {
			pages = append(pages, item)
		}
	}
	return normalizeDynamicWarmupPaths(pages), normalizeDynamicWarmupPaths(channels)
}

func (c *DynamicWarmupCache) load() (dynamicWarmupCacheFile, bool) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return dynamicWarmupCacheFile{}, false
	}

	var file dynamicWarmupCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		logs.Warm("dynamic warmup cache load failed: %v", err)
		return dynamicWarmupCacheFile{}, false
	}
	if file.Version != dynamicWarmupCacheVersion {
		logs.Warm("ignoring unsupported dynamic warmup cache version: %d", file.Version)
		return dynamicWarmupCacheFile{}, false
	}
	return file, true
}

func normalizeDynamicWarmupPaths(paths []dynamicWarmupCachePath) []dynamicWarmupCachePath {
	if len(paths) == 0 {
		return nil
	}

	merged := make(map[string]int64, len(paths))
	for _, item := range paths {
		rawPath := strings.TrimSpace(item.Path)
		if rawPath == "" || item.Count <= 0 {
			continue
		}

		path := normalizeDynamicWarmupPath(rawPath)
		if path == "" {
			continue
		}
		merged[path] += item.Count
	}
	if len(merged) == 0 {
		return nil
	}

	out := make([]dynamicWarmupCachePath, 0, len(merged))
	for path, count := range merged {
		out = append(out, dynamicWarmupCachePath{Path: path, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Path < out[j].Path
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func normalizeDynamicWarmupPath(rawPath string) string {
	path := normalizePreRoutePath(rawPath)
	if !isChannelRoutePath(path) {
		return normalizeGoxRoutePath(path)
	}

	name := strings.TrimPrefix(path, channelRoutePrefix)
	if name == "" {
		return ""
	}
	if _, err := goxBuildRoutePath("/" + name); err != nil {
		return ""
	}
	return channelRoutePrefix + name
}

func dynamicWarmupSelectCount(pathCount int) int {
	if pathCount <= 0 {
		return 0
	}
	n := int(math.Ceil(float64(pathCount) * dynamicWarmupRatio))
	if n < 1 {
		n = 1
	}
	if n > dynamicWarmupMaxPaths {
		n = dynamicWarmupMaxPaths
	}
	return n
}
