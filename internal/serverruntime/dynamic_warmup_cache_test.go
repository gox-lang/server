package serverruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDynamicWarmupSelectCountUsesTwentyPercentMaxTen(t *testing.T) {
	cases := []struct {
		paths int
		want  int
	}{
		{0, 0},
		{1, 1},
		{5, 1},
		{10, 2},
		{20, 4},
		{50, 10},
		{100, 10},
	}
	for _, tc := range cases {
		if got := dynamicWarmupSelectCount(tc.paths); got != tc.want {
			t.Fatalf("dynamicWarmupSelectCount(%d) = %d, want %d", tc.paths, got, tc.want)
		}
	}
}

func TestDynamicWarmupCachePathLivesUnderBuildCache(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, ".build", "cache", dynamicWarmupCacheFileName)
	if got := dynamicWarmupCacheFilePath(root); got != want {
		t.Fatalf("dynamic warmup cache path = %q, want %q", got, want)
	}
}

func TestDynamicWarmupCachePersistsAllPathsSortedAndNormalized(t *testing.T) {
	root := t.TempDir()
	cache := LoadDynamicWarmupCache(root)

	err := cache.persistPaths([]dynamicWarmupCachePath{
		{Path: "/z", Count: 1},
		{Path: "/a", Count: 2},
		{Path: "/a", Count: 3},
		{Path: "", Count: 99},
		{Path: "/bad/../x", Count: 10},
	})
	if err != nil {
		t.Fatalf("persistPaths failed: %v", err)
	}

	data, err := os.ReadFile(dynamicWarmupCacheFilePath(root))
	if err != nil {
		t.Fatalf("read cache failed: %v", err)
	}

	var file dynamicWarmupCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("unmarshal cache failed: %v", err)
	}
	if file.Version != dynamicWarmupCacheVersion {
		t.Fatalf("version = %d", file.Version)
	}
	if len(file.Pages) != 2 || len(file.Channels) != 0 || len(file.Paths) != 0 {
		t.Fatalf("unexpected rankings: pages=%#v channels=%#v legacy=%#v", file.Pages, file.Channels, file.Paths)
	}
	if file.Pages[0].Path != "/a" || file.Pages[0].Count != 5 {
		t.Fatalf("first path = %#v, want /a count 5", file.Pages[0])
	}
	if file.Pages[1].Path != "/z" || file.Pages[1].Count != 1 {
		t.Fatalf("second path = %#v, want /z count 1", file.Pages[1])
	}
}

func TestDynamicWarmupCacheWarmupPathsUsesTopTwentyPercentMaxTen(t *testing.T) {
	root := t.TempDir()
	cache := LoadDynamicWarmupCache(root)

	paths := make([]dynamicWarmupCachePath, 0, 60)
	for i := 0; i < 60; i++ {
		paths = append(paths, dynamicWarmupCachePath{Path: "/p" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Count: int64(60 - i)})
	}
	if err := cache.persistPaths(paths); err != nil {
		t.Fatalf("persistPaths failed: %v", err)
	}

	warmup := cache.WarmupPaths()
	if len(warmup.Pages) != 10 || len(warmup.Channels) != 0 {
		t.Fatalf("warmup = %#v, want 10 pages", warmup)
	}
}

func TestDynamicWarmupSnapshotResetsRouteCounters(t *testing.T) {
	r := &Runtime{goxRoutes: map[string]*GoxRouteRuntime{
		"/index": {RoutePath: "/index"},
		"/dash":  {RoutePath: "/dash"},
	}}
	r.goxRoutes["/index"].WarmupHits.Add(3)
	r.goxRoutes["/dash"].WarmupHits.Add(1)

	snapshot := r.snapshotAndResetDynamicWarmupCounts()
	paths := normalizeDynamicWarmupPaths(snapshot.Pages)
	if len(paths) != 2 {
		t.Fatalf("paths len = %d, want 2", len(paths))
	}
	if paths[0].Path != "/index" || paths[0].Count != 3 {
		t.Fatalf("first path = %#v", paths[0])
	}
	if got := r.goxRoutes["/index"].WarmupHits.Load(); got != 0 {
		t.Fatalf("counter after snapshot = %d, want 0", got)
	}
}

func TestDynamicWarmupSnapshotIncludesChannelDefinitions(t *testing.T) {
	channelRoute := &GoxRouteRuntime{RoutePath: channelRoutePrefix + "Chat"}
	channelRoute.WarmupHits.Add(7)
	r := &Runtime{
		goxRoutes: map[string]*GoxRouteRuntime{},
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {route: channelRoute},
		},
	}
	snapshot := r.snapshotAndResetDynamicWarmupCounts()
	paths := normalizeDynamicWarmupPaths(snapshot.Channels)
	if len(paths) != 1 || paths[0].Path != channelRoutePrefix+"Chat" || paths[0].Count != 7 {
		t.Fatalf("channel warmup paths = %#v", paths)
	}
	if got := channelRoute.WarmupHits.Load(); got != 0 {
		t.Fatalf("channel counter after snapshot = %d, want 0", got)
	}
}

func TestDynamicWarmupCacheAcceptsStableChannelDefinitionPaths(t *testing.T) {
	paths := normalizeDynamicWarmupPaths([]dynamicWarmupCachePath{
		{Path: channelRoutePrefix + "Chat", Count: 4},
		{Path: channelRoutePrefix + "Chat", Count: 3},
	})
	if len(paths) != 1 || paths[0].Path != channelRoutePrefix+"Chat" || paths[0].Count != 7 {
		t.Fatalf("normalized channel paths = %#v", paths)
	}
}

func TestDynamicWarmupCacheSelectsPagesAndChannelsIndependently(t *testing.T) {
	root := t.TempDir()
	cache := LoadDynamicWarmupCache(root)
	if err := cache.persistSnapshot(dynamicWarmupSnapshot{
		Pages: []dynamicWarmupCachePath{
			{Path: "/index", Count: 2},
		},
		Channels: []dynamicWarmupCachePath{
			{Path: channelRoutePrefix + "Chat", Count: 2},
		},
	}); err != nil {
		t.Fatalf("persistSnapshot failed: %v", err)
	}

	selection := cache.WarmupPaths()
	if len(selection.Pages) != 1 || selection.Pages[0] != "/index" {
		t.Fatalf("page selection = %#v", selection.Pages)
	}
	if len(selection.Channels) != 1 || selection.Channels[0] != channelRoutePrefix+"Chat" {
		t.Fatalf("channel selection = %#v", selection.Channels)
	}
}

func TestDynamicWarmupCacheMigratesLegacyCombinedPaths(t *testing.T) {
	root := t.TempDir()
	cache := LoadDynamicWarmupCache(root)
	if err := os.MkdirAll(filepath.Dir(cache.path), 0755); err != nil {
		t.Fatal(err)
	}
	legacy := dynamicWarmupCacheFile{
		Version: dynamicWarmupCacheVersion,
		Paths: []dynamicWarmupCachePath{
			{Path: channelRoutePrefix + "Chat", Count: 2},
			{Path: "/index", Count: 2},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache.path, data, 0644); err != nil {
		t.Fatal(err)
	}

	selection := cache.WarmupPaths()
	if len(selection.Pages) != 1 || selection.Pages[0] != "/index" {
		t.Fatalf("legacy page selection = %#v", selection.Pages)
	}
	if len(selection.Channels) != 1 || selection.Channels[0] != channelRoutePrefix+"Chat" {
		t.Fatalf("legacy channel selection = %#v", selection.Channels)
	}
}

func TestDynamicWarmupCacheConcurrentWritersUseIndependentTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	cache := LoadDynamicWarmupCache(root)
	var wait sync.WaitGroup
	errors := make(chan error, 16)
	for index := 0; index < 16; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := cache.persistPaths([]dynamicWarmupCachePath{{Path: "/page" + string(rune('a'+index)), Count: int64(index + 1)}}); err != nil {
				errors <- err
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if _, ok := cache.load(); !ok {
		t.Fatal("concurrent writers left an invalid warmup cache")
	}
}
