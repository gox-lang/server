package serverruntime

import (
	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type generationStorage struct {
	server.SessionStorageRuntime
	closed atomic.Bool
}

func (s *generationStorage) Close() error { s.closed.Store(true); return nil }

func TestRequestKeepsPolicyAndStorageUntilReleased(t *testing.T) {
	oldStorage := &generationStorage{}
	nextStorage := &generationStorage{}
	r := &Runtime{sessionStorage: oldStorage}
	original := &RuntimeState{Config: server.Config{ProjectName: "old"}}
	r.runtimeState.Store(original)
	state, old, _ := r.acquireRequestGeneration()
	ctx := &gox.Context{}
	registerContextRuntimeState(ctx, nil)
	defer unregisterContextRuntimeState(ctx)
	runtimeContextState(ctx).requestState = state
	runtimeContextState(ctx).generation = old
	// Publish while the first request is still active, as reloadConfig does.
	r.requestMu.Lock()
	r.runtimeState.Store(&RuntimeState{Config: server.Config{ProjectName: "new"}})
	r.requestGeneration = &requestGeneration{storage: nextStorage}
	r.requestMu.Unlock()
	old.retire(func() { _ = oldStorage.Close() })
	if oldStorage.closed.Load() {
		t.Fatal("active storage closed")
	}
	if r.stateFor(ctx) != original || r.sessionStorageFor(ctx) != oldStorage {
		t.Fatal("request switched generation")
	}
	next, newLease, _ := r.acquireRequestGeneration()
	if next.Config.ProjectName != "new" || newLease.storage != nextStorage {
		t.Fatal("new request kept old generation")
	}
	newLease.release()
	old.release()
	if !oldStorage.closed.Load() || nextStorage.closed.Load() {
		t.Fatal("incorrect resource retirement")
	}
}

func TestHTTPInFlightSurvivesConfigurationPublication(t *testing.T) {
	entered := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan struct{})
	var r *Runtime
	r = newHotBenchmarkRuntime(func(ctx *gox.Context) {
		if ctx.Request.URL.Path() == "/" {
			close(entered)
			<-resume
		}
		ctx.Writer.WriteRawString(r.stateFor(ctx).Config.ProjectName)
	})
	defer r.rateLimitStore.Close()
	r.runtimeState.Load().Config.ProjectName = "old"
	response := httptest.NewRecorder()
	go func() { defer close(done); r.ServeHTTP(response, httptest.NewRequest("GET", "http://example/", nil)) }()
	<-entered
	r.requestMu.Lock()
	old := r.requestGeneration
	next := *r.state()
	next.Config.ProjectName = "new"
	r.requestGeneration = &requestGeneration{}
	r.runtimeState.Store(&next)
	r.cache404.Clear()
	r.requestMu.Unlock()
	var retired atomic.Bool
	old.retire(func() { retired.Store(true) })
	if retired.Load() {
		t.Fatal("resources retired during request")
	}
	close(resume)
	<-done
	if response.Body.String() != "old" || !retired.Load() {
		t.Fatalf("body=%q retired=%v", response.Body.String(), retired.Load())
	}
	state, lease, _ := r.acquireRequestGeneration()
	defer lease.release()
	if state.Config.ProjectName != "new" {
		t.Fatal("new state not visible")
	}
}
