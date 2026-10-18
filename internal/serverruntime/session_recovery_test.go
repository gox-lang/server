package serverruntime

import (
	"context"
	"testing"
	"time"

	"github.com/gox-lang/gox/server"
)

type sessionExpireRecoveryTestStorage struct {
	done chan struct{}
}

func (*sessionExpireRecoveryTestStorage) GetName() string { return "expire-recovery-test" }
func (*sessionExpireRecoveryTestStorage) Close() error    { return nil }
func (*sessionExpireRecoveryTestStorage) OpenSession(context.Context, server.SessionOpenRequest) (server.SessionOpenResult, error) {
	return server.SessionOpenResult{}, nil
}
func (*sessionExpireRecoveryTestStorage) GetSession(context.Context, string) (server.SessionStorageInstance, error) {
	return nil, server.ErrSessionNotAvailable
}
func (*sessionExpireRecoveryTestStorage) CreateSession(context.Context, *server.SessionMeta) (server.SessionStorageInstance, error) {
	return nil, nil
}
func (*sessionExpireRecoveryTestStorage) SaveSession(context.Context, string) error { return nil }
func (*sessionExpireRecoveryTestStorage) TouchSession(context.Context, string, time.Duration) error {
	return nil
}
func (*sessionExpireRecoveryTestStorage) DeleteSession(context.Context, string) error { return nil }
func (s *sessionExpireRecoveryTestStorage) SubscribeSessionEvents(_ context.Context, callback server.SessionEventFunc) (err error) {
	defer func() {
		_ = recover() // Keep the test process alive if the runtime lets the callback panic escape.
		close(s.done)
	}()

	callback(server.SessionEvent{Kind: server.SessionEventExpired, Session: &server.ExpireSession{}})
	callback(server.SessionEvent{Kind: server.SessionEventExpired, Session: &server.ExpireSession{}})
	return nil
}

func TestSessionOnExpirePanicDoesNotStopSubscription(t *testing.T) {
	storage := &sessionExpireRecoveryTestStorage{done: make(chan struct{})}
	calls := 0
	policy := &SessionPolicy{
		onExpire: func(*server.ExpireSession) {
			calls++
			if calls == 1 {
				panic("expire panic")
			}
		},
	}
	rt := &Runtime{}

	cancel := rt.startSessionExpireSubscription(storage, policy)
	if cancel == nil {
		t.Fatal("startSessionExpireSubscription returned nil cancel function")
	}
	defer cancel()

	select {
	case <-storage.done:
	case <-time.After(time.Second):
		t.Fatal("session expire subscription did not finish")
	}

	if calls != 2 {
		t.Fatalf("OnExpire calls = %d, want 2 after recovering first panic", calls)
	}
}
