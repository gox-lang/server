package serverruntime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

type webSocketSessionTestStorage struct {
	mu       sync.Mutex
	saves    int
	touches  int
	touchTTL time.Duration
}

func (*webSocketSessionTestStorage) GetName() string { return "test" }
func (*webSocketSessionTestStorage) Close() error    { return nil }
func (*webSocketSessionTestStorage) OpenSession(context.Context, server.SessionOpenRequest) (server.SessionOpenResult, error) {
	return server.SessionOpenResult{}, nil
}
func (*webSocketSessionTestStorage) GetSession(context.Context, string) (server.SessionStorageInstance, error) {
	return nil, server.ErrSessionNotAvailable
}
func (*webSocketSessionTestStorage) CreateSession(context.Context, *server.SessionMeta) (server.SessionStorageInstance, error) {
	return nil, nil
}
func (s *webSocketSessionTestStorage) SaveSession(context.Context, string) error {
	s.mu.Lock()
	s.saves++
	s.mu.Unlock()
	return nil
}
func (s *webSocketSessionTestStorage) TouchSession(_ context.Context, _ string, ttl time.Duration) error {
	s.mu.Lock()
	s.touches++
	s.touchTTL = ttl
	s.mu.Unlock()
	return nil
}
func (*webSocketSessionTestStorage) DeleteSession(context.Context, string) error { return nil }
func (*webSocketSessionTestStorage) SubscribeSessionEvents(ctx context.Context, _ server.SessionEventFunc) error {
	<-ctx.Done()
	return nil
}

func TestWebSocketPublicActivitySavesEveryEventAndThrottlesTouch(t *testing.T) {
	storage := &webSocketSessionTestStorage{}
	runtime := &Runtime{sessionStorage: storage, sessionTouches: make(map[string]time.Time)}
	runtime.runtimeState.Store(&RuntimeState{Session: &SessionPolicy{ttl: time.Hour}})
	memory, err := (server.MemorySessionAdapter{}).OpenSessionStorage(context.Background(), server.SessionStorageConfig{TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = memory.Close() })
	now := time.Now()
	instance, err := memory.CreateSession(context.Background(), &server.SessionMeta{
		ID: "session-1", Token: "token", TTL: 15 * time.Minute,
		CreatedAt: now, UpdatedAt: now, ExpireAt: now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	connection := &webSocketRuntimeConnection{
		runtime: runtime,
		socket:  &gox.Socket{Session: server.NewSession(instance)},
	}

	if err := connection.persistSessionActivity("first"); err != nil {
		t.Fatalf("first activity error = %#v", err)
	}
	if err := connection.persistSessionActivity("second"); err != nil {
		t.Fatalf("second activity error = %#v", err)
	}

	storage.mu.Lock()
	saves, touches, touchTTL := storage.saves, storage.touches, storage.touchTTL
	storage.mu.Unlock()
	if saves != 2 {
		t.Fatalf("SaveSession calls = %d, want 2", saves)
	}
	if touches != 1 {
		t.Fatalf("TouchSession calls = %d, want 1", touches)
	}
	if touchTTL != 15*time.Minute {
		t.Fatalf("TouchSession TTL = %s, want 15m", touchTTL)
	}
}

func TestWebSocketSessionLifecycleClosesEveryAssociatedSocket(t *testing.T) {
	runtime := &Runtime{sessionTouches: map[string]time.Time{"session-1": time.Now()}}
	registry := runtime.webSocketRegistry()
	first := &gox.Socket{}
	second := &gox.Socket{}
	other := &gox.Socket{}
	if err := registry.register(first, server.NewSessionReference("session-1")); err != nil {
		t.Fatal(err)
	}
	if err := registry.register(second, server.NewSessionReference("session-1")); err != nil {
		t.Fatal(err)
	}
	if err := registry.register(other, server.NewSessionReference("session-2")); err != nil {
		t.Fatal(err)
	}

	closed := make(chan gox.WSCloseCode, 2)
	gox.BindSocketDisconnect(first, func(code gox.WSCloseCode, _ string) { closed <- code })
	gox.BindSocketDisconnect(second, func(code gox.WSCloseCode, _ string) { closed <- code })
	gox.BindSocketDisconnect(other, func(code gox.WSCloseCode, _ string) { t.Errorf("unrelated socket closed with %d", code) })

	runtime.handleWebSocketSessionEvent(server.SessionEvent{Kind: server.SessionEventExpired, SessionID: "session-1"})
	for index := 0; index < 2; index++ {
		select {
		case code := <-closed:
			if code != gox.WSCloseSessionExpired {
				t.Fatalf("close code = %d", code)
			}
		case <-time.After(time.Second):
			t.Fatal("session socket was not closed")
		}
	}
	if _, ok := runtime.sessionTouches["session-1"]; ok {
		t.Fatal("session touch state was not released")
	}
}

var _ server.SessionStorageRuntime = (*webSocketSessionTestStorage)(nil)

func TestHTTPSessionDestroyDeletesCookies(t *testing.T) {
	storage, err := (server.MemorySessionAdapter{}).OpenSessionStorage(context.Background(), server.SessionStorageConfig{TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	now := time.Now()
	instance, err := storage.CreateSession(context.Background(), &server.SessionMeta{
		ID: "http-destroy", Token: "token", TTL: time.Hour,
		CreatedAt: now, UpdatedAt: now, ExpireAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{sessionStorage: storage}
	response := gox.NewResponse()
	ctx := &gox.Context{Response: response}
	policy := &SessionPolicy{idCookie: "App-SessionID", tokenCookie: "App-SessionToken"}
	session := runtime.newSession(ctx, policy, instance)
	if err := session.Destroy(); err != nil {
		t.Fatal(err)
	}
	cookies, _ := response.Headers.GetAll("Set-Cookie")
	if len(cookies) != 2 {
		t.Fatalf("deleted cookies = %#v", cookies)
	}
	for _, cookie := range cookies {
		if !strings.Contains(cookie, "Max-Age=0") || !strings.Contains(cookie, "Expires=") {
			t.Fatalf("cookie was not deleted: %q", cookie)
		}
	}
}
