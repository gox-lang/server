package serverruntime

import (
	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
	"sync"
)

// A generation keeps the session/cluster resources alive for requests that
// started before a configuration reload. Retirement never blocks new requests.
type requestGeneration struct {
	mu      sync.Mutex
	users   int
	retired bool
	cleanup func()
	storage server.SessionStorageRuntime
}

func (g *requestGeneration) release() {
	g.mu.Lock()
	g.users--
	cleanup := g.takeCleanup()
	g.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}
func (g *requestGeneration) retire(cleanup func()) {
	g.mu.Lock()
	g.retired = true
	g.cleanup = cleanup
	run := g.takeCleanup()
	g.mu.Unlock()
	if run != nil {
		run()
	}
}
func (g *requestGeneration) takeCleanup() func() {
	if !g.retired || g.users != 0 {
		return nil
	}
	cleanup := g.cleanup
	g.cleanup = nil
	return cleanup
}
func (r *Runtime) acquireRequestGeneration() (*RuntimeState, *requestGeneration, uint64) {
	r.requestMu.Lock()
	defer r.requestMu.Unlock()
	if r.requestGeneration == nil {
		r.requestGeneration = &requestGeneration{storage: r.getSessionStorage()}
	}
	g := r.requestGeneration
	g.mu.Lock()
	g.users++
	g.mu.Unlock()
	return r.state(), g, r.cache404.Generation()
}
func (r *Runtime) stateFor(ctx *gox.Context) *RuntimeState {
	if state := runtimeContextState(ctx); state != nil && state.requestState != nil {
		return state.requestState
	}
	return r.state()
}
func (r *Runtime) sessionStorageFor(ctx *gox.Context) server.SessionStorageRuntime {
	if state := runtimeContextState(ctx); state != nil && state.generation != nil {
		return state.generation.storage
	}
	return r.getSessionStorage()
}
