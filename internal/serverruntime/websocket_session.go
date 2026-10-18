package serverruntime

import (
	"context"
	"errors"
	"time"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

const webSocketSessionOperationTimeout = 10 * time.Second

func (r *Runtime) handleWebSocketSessionEvent(event server.SessionEvent) {
	if r == nil || event.SessionID == "" {
		return
	}

	r.sessionTouchMu.Lock()
	delete(r.sessionTouches, event.SessionID)
	r.sessionTouchMu.Unlock()

	var code gox.WSCloseCode
	switch event.Kind {
	case server.SessionEventExpired:
		code = gox.WSCloseSessionExpired
	case server.SessionEventDestroyed:
		code = gox.WSCloseSessionDestroyed
	default:
		return
	}

	for _, socket := range r.webSocketRegistry().findBySessionID(event.SessionID) {
		if socket != nil {
			socket.Disconnect(code)
		}
	}
}

func (connection *webSocketRuntimeConnection) persistSessionActivity(event string) *gox.GoxSocketError {
	if connection == nil || connection.runtime == nil || connection.socket == nil || connection.socket.Session.ID() == "" {
		return nil
	}
	if connection.socket.Session.IsDestroyed() {
		return nil
	}

	runtime := connection.runtime
	policy := runtime.state().Session
	if policy == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), webSocketSessionOperationTimeout)
	defer cancel()

	runtime.sessionStorageMu.RLock()
	defer runtime.sessionStorageMu.RUnlock()
	storage := runtime.sessionStorage
	if storage == nil {
		err := gox.NewGoxSocketError(
			gox.GoxSocketSessionSaveFailed,
			"WebSocket session storage is not available",
			gox.GoxSocketPhaseSession,
			event,
			true,
			server.ErrSessionNotAvailable,
		)
		return &err
	}

	if err := storage.SaveSession(ctx, connection.socket.Session.ID()); err != nil {
		socketErr := newWebSocketSessionError(gox.GoxSocketSessionSaveFailed, "WebSocket session could not be saved", event, err)
		if sessionUnavailable(err) {
			connection.disconnect(gox.WSCloseSessionExpired, gox.WSCloseReason(gox.WSCloseSessionExpired))
		}
		return &socketErr
	}

	now := time.Now()
	ttl := connection.socket.Session.GetTTL()
	if ttl <= 0 {
		ttl = policy.ttl
	}
	if !runtime.reserveSessionTouch(connection.socket.Session.ID(), ttl, now) {
		return nil
	}

	err := storage.TouchSession(ctx, connection.socket.Session.ID(), ttl)
	runtime.finishSessionTouch(connection.socket.Session.ID(), now, err == nil)
	if err == nil {
		return nil
	}

	code := gox.GoxSocketSessionTouchFailed
	message := "WebSocket session activity could not be renewed"
	if sessionUnavailable(err) {
		code = gox.GoxSocketSessionExpired
		message = "WebSocket session expired"
		connection.disconnect(gox.WSCloseSessionExpired, gox.WSCloseReason(gox.WSCloseSessionExpired))
	}
	socketErr := newWebSocketSessionError(code, message, event, err)
	return &socketErr
}

func newWebSocketSessionError(code gox.GoxSocketErrorCode, message string, event string, cause error) gox.GoxSocketError {
	return gox.NewGoxSocketError(code, message, gox.GoxSocketPhaseSession, event, false, cause)
}

func sessionUnavailable(err error) bool {
	return errors.Is(err, server.ErrSessionExpired) || errors.Is(err, server.ErrSessionNotAvailable)
}

func (r *Runtime) reserveSessionTouch(sessionID string, ttl time.Duration, now time.Time) bool {
	if r == nil || sessionID == "" {
		return false
	}
	interval := webSocketSessionTouchInterval(ttl)
	r.sessionTouchMu.Lock()
	defer r.sessionTouchMu.Unlock()
	if r.sessionTouches == nil {
		r.sessionTouches = make(map[string]time.Time)
	}
	last := r.sessionTouches[sessionID]
	if !last.IsZero() && now.Sub(last) < interval {
		return false
	}
	// A future timestamp reserves this session while the storage operation is
	// in flight, preventing sibling sockets from issuing the same touch.
	r.sessionTouches[sessionID] = now.Add(interval)
	return true
}

func (r *Runtime) finishSessionTouch(sessionID string, touchedAt time.Time, succeeded bool) {
	if r == nil || sessionID == "" {
		return
	}
	r.sessionTouchMu.Lock()
	if succeeded {
		r.sessionTouches[sessionID] = touchedAt
	} else {
		delete(r.sessionTouches, sessionID)
	}
	r.sessionTouchMu.Unlock()
}

func (r *Runtime) clearSessionTouch(sessionID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.sessionTouchMu.Lock()
	delete(r.sessionTouches, sessionID)
	r.sessionTouchMu.Unlock()
}

func webSocketSessionTouchInterval(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	interval := ttl / 4
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	return interval
}
