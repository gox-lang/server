package serverruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gox-lang/server/internal/logs"
)

const (
	cache404Version    = 2
	cache404MaxEntries = 4096
	cache404TTL        = 30 * time.Second
)

type Cache404 struct {
	path       string
	mu         sync.RWMutex
	items      map[string]struct{}
	expires    map[string]time.Time
	generation uint64
	dirty      bool
	stop       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
	closed     bool
}

type cache404File struct {
	Version int                  `json:"version"`
	Entries map[string]time.Time `json:"entries"`
}

func LoadCache404(rootDir string) *Cache404 {
	c := &Cache404{path: filepath.Join(rootDir, ".build", "cache", "cache404.json"), items: make(map[string]struct{}), expires: make(map[string]time.Time), stop: make(chan struct{}), done: make(chan struct{})}
	// Negative filesystem observations cannot be trusted across server restarts:
	// files or routing configuration may have changed while no watcher was active.
	c.dirty = true
	go func() {
		defer close(c.done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.persistIfDirty()
			case <-c.stop:
				c.persistIfDirty()
				return
			}
		}
	}()
	return c
}

func (c *Cache404) Len() int { c.mu.RLock(); defer c.mu.RUnlock(); return len(c.items) }
func (c *Cache404) Generation() uint64 {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}
func (c *Cache404) Has(path string) bool {
	if c == nil || path == "" {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.items[path]
	return ok && time.Now().Before(c.expires[path])
}
func (c *Cache404) Add(path string) { c.AddIfGeneration(path, c.Generation()) }
func (c *Cache404) AddIfGeneration(path string, generation uint64) {
	if c == nil || path == "" || len(path) > 2048 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || generation != c.generation {
		return
	}
	now := time.Now()
	if c.items == nil {
		c.items = make(map[string]struct{})
	}
	if c.expires == nil {
		c.expires = make(map[string]time.Time)
	}
	if expiry, ok := c.expires[path]; ok && now.Before(expiry) {
		return
	}
	if len(c.items) >= cache404MaxEntries {
		for key, expiry := range c.expires {
			if !now.Before(expiry) {
				delete(c.items, key)
				delete(c.expires, key)
			}
		}
		if len(c.items) >= cache404MaxEntries {
			return
		}
	}
	c.items[path] = struct{}{}
	c.expires[path] = now.Add(cache404TTL)
	c.dirty = true
}
func (c *Cache404) Remove(path string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	delete(c.items, path)
	delete(c.expires, path)
	c.dirty = true
}
func (c *Cache404) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	clear(c.items)
	clear(c.expires)
	c.dirty = true
}
func (c *Cache404) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		if c.stop != nil {
			close(c.stop)
			<-c.done
		}
	})
}
func (c *Cache404) persistIfDirty() {
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return
	}
	entries := make(map[string]time.Time, len(c.expires))
	now := time.Now()
	for path, expiry := range c.expires {
		if now.Before(expiry) {
			entries[path] = expiry
		} else {
			delete(c.items, path)
			delete(c.expires, path)
		}
	}
	c.dirty = false
	c.mu.Unlock()
	if c.path == "" {
		return
	}
	data, err := json.Marshal(cache404File{Version: cache404Version, Entries: entries})
	if err == nil {
		err = os.MkdirAll(filepath.Dir(c.path), 0755)
	}
	if err == nil {
		err = os.WriteFile(c.path+".tmp", data, 0644)
	}
	if err == nil {
		err = os.Rename(c.path+".tmp", c.path)
	}
	if err != nil {
		c.mu.Lock()
		c.dirty = true
		c.mu.Unlock()
		logs.Warm("cache404 persist failed: %v", err)
	}
}
