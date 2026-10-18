package serverruntime

import (
	"fmt"
	"github.com/gox-lang/gox"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConditional404DoesNotPoisonPage(t *testing.T) {
	calls := 0
	r := newHotBenchmarkRuntime(func(ctx *gox.Context) {
		calls++
		if calls == 1 {
			ctx.Response.Status.SetCode(404)
		} else {
			ctx.Writer.WriteRawString("available")
		}
	})
	defer r.rateLimitStore.Close()
	for _, status := range []int{404, 200} {
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest("GET", "http://example/", nil))
		if response.Code != status {
			t.Fatalf("status=%d want=%d calls=%d", response.Code, status, calls)
		}
	}
	if calls != 2 || r.cache404.Has("/") {
		t.Fatal("handler-generated 404 cached")
	}
}
func TestOnlyUnresolvedRoutesEnter404Cache(t *testing.T) {
	r := newHotBenchmarkRuntime(func(ctx *gox.Context) { ctx.Writer.WriteRawString("ok") })
	defer r.rateLimitStore.Close()
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest("GET", "http://example/missing", nil))
	if response.Code != 404 || !r.cache404.Has("/missing") {
		t.Fatal("missing route not cached")
	}
	generation := r.cache404.Generation()
	r.cache404.Clear()
	r.cache404.AddIfGeneration("/missing", generation)
	if r.cache404.Has("/missing") {
		t.Fatal("old request repopulated invalidated cache")
	}
}
func Test404CacheBoundExpiryRestartAndClose(t *testing.T) {
	root := t.TempDir()
	c := LoadCache404(root)
	for i := 0; i < cache404MaxEntries+20; i++ {
		c.Add(fmt.Sprint("/", i))
	}
	if c.Len() != cache404MaxEntries {
		t.Fatalf("unbounded cache: %d", c.Len())
	}
	c.mu.Lock()
	c.expires["/0"] = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if c.Has("/0") {
		t.Fatal("expired hit")
	}
	c.Add("/new")
	if !c.Has("/new") || c.Len() > cache404MaxEntries {
		t.Fatal("expired entries not reclaimed")
	}
	c.Close()
	c.Close()
	select {
	case <-c.done:
	default:
		t.Fatal("worker still active")
	}
	if _, err := os.Stat(filepath.Join(root, ".build/cache/cache404.json")); err != nil {
		t.Fatal(err)
	}
	reopened := LoadCache404(root)
	defer reopened.Close()
	if reopened.Has("/new") {
		t.Fatal("stale filesystem observations restored after restart")
	}
}
