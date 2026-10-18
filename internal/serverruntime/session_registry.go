package serverruntime

import (
	"context"
	"strings"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

func (r *Runtime) bindSessions() {
	if r == nil {
		return
	}
	r.unbindSessions = gox.BindSessionRegistry(gox.SessionRegistryBindings{
		Get: r.getSessionByID,
	})
}

func (r *Runtime) getSessionByID(sessionID string) (*gox.Session, error) {
	if r == nil || strings.TrimSpace(sessionID) == "" {
		return nil, server.ErrSessionNotAvailable
	}

	ctx, cancel := context.WithTimeout(context.Background(), webSocketSessionOperationTimeout)
	defer cancel()
	r.sessionStorageMu.RLock()
	storage := r.sessionStorage
	if storage == nil {
		r.sessionStorageMu.RUnlock()
		return nil, server.ErrSessionNotAvailable
	}
	instance, err := storage.GetSession(ctx, sessionID)
	r.sessionStorageMu.RUnlock()
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, server.ErrSessionNotAvailable
	}

	session := server.NewSessionWithLifecycle(instance, nil, func() error {
		return r.destroySession(instance.ID())
	}, nil, func() error {
		return r.saveSessionByID(instance.ID())
	})
	return &session, nil
}
