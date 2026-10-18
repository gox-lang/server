package serverruntime

import (
	"path/filepath"
	"testing"
)

func TestBuildCachePathsLiveUnderBuildCache(t *testing.T) {
	root := t.TempDir()

	cache404 := LoadCache404(root)
	defer cache404.Close()
	if cache404.path != filepath.Join(root, ".build", "cache", "cache404.json") {
		t.Fatalf("cache404 path = %q", cache404.path)
	}

	staticCache := LoadStaticCache(root)
	defer staticCache.Close()
	if staticCache.path != filepath.Join(root, ".build", "cache", "staticCache.json") {
		t.Fatalf("static cache path = %q", staticCache.path)
	}
	if staticCache.pathDir != filepath.Join(root, ".build", "cache", "statics") {
		t.Fatalf("static artifact dir = %q", staticCache.pathDir)
	}

	warmupCache := LoadDynamicWarmupCache(root)
	if warmupCache.path != filepath.Join(root, ".build", "cache", "dynamicWarmupCache.json") {
		t.Fatalf("dynamic warmup cache path = %q", warmupCache.path)
	}
}

func TestBuildStaticArtifactPathLivesUnderBuildCacheStatics(t *testing.T) {
	root := t.TempDir()
	got := buildStaticArtifactPath(root, "/style.css", "123", "style.css", "br", 7)
	want := filepath.Join(root, ".build", "cache", "statics", "style.css", "123", "style.css.l7.br")
	if got != want {
		t.Fatalf("artifact path = %q, want %q", got, want)
	}
}
