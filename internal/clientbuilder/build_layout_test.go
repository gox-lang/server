package clientbuilder

import (
	"path/filepath"
	"testing"
)

func TestClientCachePathLivesUnderBuildCache(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(cleanCacheRoot(root), ".build", "cache", clientCacheFileName)
	if got := clientCachePath(root); got != want {
		t.Fatalf("client cache path = %q, want %q", got, want)
	}
}
