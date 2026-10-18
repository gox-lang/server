package serverruntime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

const (
	sessionIDCookieSuffix    = "SessionID"
	sessionTokenCookieSuffix = "SessionToken"
	defaultSessionTTL        = 3 * time.Hour
	sessionIDBytes           = 32
	sessionTokenBytes        = 32
)

type SessionPolicy struct {
	projectName string
	ttl         time.Duration
	idCookie    string
	tokenCookie string
	storageName string
	onCreate    func(session *server.Session)
	onExpire    func(session *server.ExpireSession)
}

func NewSessionPolicy(projectName string, config server.SessionConfig) (*SessionPolicy, error) {
	ttl := config.TTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}

	prefix := strings.TrimSpace(projectName)
	if prefix == "" {
		prefix = "Gox"
	}

	policy := &SessionPolicy{
		projectName: prefix,
		ttl:         ttl,
		idCookie:    prefix + "-" + sessionIDCookieSuffix,
		tokenCookie: prefix + "-" + sessionTokenCookieSuffix,
		onCreate:    config.OnCreate,
		onExpire:    config.OnExpire,
	}

	return policy, nil
}

func OpenSessionRuntime(cluster server.ClusterAdapter, policy *SessionPolicy, instanceID string, maxMessageSize ...int64) (server.SessionStorageRuntime, server.ClusterRuntime, error) {
	projectName := "Gox"
	ttl := defaultSessionTTL
	if policy != nil {
		projectName = policy.projectName
		ttl = policy.ttl
	}

	if cluster != nil {
		if instanceID == "" {
			return nil, nil, errors.New("cluster runtime requires a GOX instance ID")
		}
		var clusterMaxMessageSize int64
		if len(maxMessageSize) > 0 {
			clusterMaxMessageSize = maxMessageSize[0]
		}
		runtime, err := cluster.OpenCluster(context.Background(), server.ClusterConfig{
			ProjectName:    projectName,
			InstanceID:     instanceID,
			SessionTTL:     ttl,
			MaxMessageSize: clusterMaxMessageSize,
		})
		if err != nil {
			return nil, nil, err
		}
		if runtime == nil {
			return nil, nil, errors.New("cluster adapter returned nil runtime")
		}
		if runtime.InstanceID() != instanceID {
			_ = runtime.Close()
			return nil, nil, errors.New("cluster adapter did not preserve the GOX instance ID")
		}
		logSessionRuntime(policy, runtime.GetName())
		return runtime, runtime, nil
	}

	storage := server.NewMemorySessionAdapter()
	runtime, err := storage.OpenSessionStorage(context.Background(), server.SessionStorageConfig{
		ProjectName: projectName,
		TTL:         ttl,
	})
	if err != nil {
		return nil, nil, err
	}
	if runtime == nil {
		return nil, nil, errors.New("memory session storage returned nil runtime")
	}
	logSessionRuntime(policy, runtime.GetName())
	return runtime, nil, nil
}

func logSessionRuntime(policy *SessionPolicy, storageName string) {
	if policy == nil {
		return
	}
	storageName = strings.TrimSpace(storageName)
	if storageName == "" {
		storageName = "unknown"
	}
	policy.storageName = storageName
	logs.Info("session loaded: ttl=%s storage=%s idCookie=%s tokenCookie=%s", policy.ttl, storageName, policy.idCookie, policy.tokenCookie)
}

func (r *Runtime) startSessionExpireSubscription(storage server.SessionStorageRuntime, policy *SessionPolicy) context.CancelFunc {
	if storage == nil || policy == nil {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		err := storage.SubscribeSessionEvents(ctx, func(event server.SessionEvent) {
			r.handleWebSocketSessionEvent(event)
			if event.Kind == server.SessionEventExpired && policy.onExpire != nil && event.Session != nil {
				if err := executeSessionOnExpire(policy.onExpire, event.Session); err != nil {
					logs.Error("session OnExpire hook failed: %v", err)
				}
			}
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			logs.Error("session expire subscription failed: %v", err)
			logs.Flush()
			os.Exit(1)
		}
	}()

	return cancel
}

func executeSessionOnExpire(onExpire func(session *server.ExpireSession), session *server.ExpireSession) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("OnExpire panic: %v", recovered)
		}
	}()

	onExpire(session)
	return nil
}

func (r *Runtime) session(ctx *gox.Context, req *http.Request, trace *requestPipelineTrace) bool {
	policy := r.stateFor(ctx).Session
	if policy == nil {
		return true
	}

	storage := r.sessionStorageFor(ctx)
	if storage == nil {
		logs.Warm("session storage is not available")
		r.renderHTTPStatusError(ctx, req, trace, http.StatusServiceUnavailable, "service unavailable")
		return false
	}

	session, ok := r.resolveSession(ctx, req, policy, storage, trace)
	if !ok {
		r.renderHTTPStatusError(ctx, req, trace, http.StatusServiceUnavailable, "service unavailable")
		return false
	}

	ctx.Session = session
	return true
}

func (r *Runtime) saveSession(req *http.Request, ctx *gox.Context) {
	if ctx == nil || ctx.Session.ID() == "" || ctx.Session.IsDestroyed() {
		return
	}

	policy := r.stateFor(ctx).Session
	if policy == nil {
		return
	}

	storage := r.sessionStorageFor(ctx)
	if storage == nil {
		logs.Warm("session storage is not available")
		return
	}

	if err := storage.SaveSession(req.Context(), ctx.Session.ID()); err != nil {
		logs.Warm("session save failed: %v", err)
	}
}

func (r *Runtime) resolveSession(ctx *gox.Context, req *http.Request, policy *SessionPolicy, storage server.SessionStorageRuntime, trace *requestPipelineTrace) (server.Session, bool) {
	stageStart := trace.nowDeep()
	now := time.Now()
	trace.addDeep("session.now", stageStart)

	stageStart = trace.nowDeep()
	id, token := readSessionCookies(req, policy)
	trace.addDeep("session.cookies", stageStart)

	if id != "" {
		var sessionTrace *server.SessionTrace
		if trace != nil && trace.enabled && trace.deep {
			sessionTrace = server.NewSessionTrace(true)
		}

		stageStart = trace.nowDeep()
		result, err := storage.OpenSession(req.Context(), server.SessionOpenRequest{
			ID:    id,
			Token: token,
			TTL:   policy.ttl,
			Now:   now,
			Trace: sessionTrace,
		})
		trace.addDeep("session.open", stageStart)
		trace.addSessionTrace(sessionTrace)
		trace.addDeepField("session.open.status", sessionOpenStatusName(result.Status))

		if err != nil {
			logs.Warm("session open failed: %v", err)
			return server.Session{}, false
		}

		if result.Opened() {
			return r.newSession(ctx, policy, result.Instance), true
		}
	}

	stageStart = trace.nowDeep()
	meta := newSessionMeta(policy, now)
	trace.addDeep("session.newMeta", stageStart)

	stageStart = trace.nowDeep()
	instance, err := storage.CreateSession(req.Context(), meta)
	trace.addDeep("session.create", stageStart)
	if err != nil {
		logs.Warm("session create failed: %v", err)
		return server.Session{}, false
	}

	stageStart = trace.nowDeep()
	session := r.newSession(ctx, policy, instance)
	trace.addDeep("session.wrap", stageStart)

	if policy.onCreate != nil {
		stageStart = trace.nowDeep()
		policy.onCreate(&session)
		trace.addDeep("session.onCreate", stageStart)
	}

	stageStart = trace.nowDeep()
	writeCookie(ctx.Response, policy.idCookie, session.ID(), ctx.Request.IsSecure())
	writeCookie(ctx.Response, policy.tokenCookie, session.Token(), ctx.Request.IsSecure())
	trace.addDeep("session.cookies.write", stageStart)

	return session, true
}

func sessionOpenStatusName(status server.SessionOpenStatus) string {
	switch status {
	case server.SessionOpenOpened:
		return "opened"
	case server.SessionOpenInvalidToken:
		return "invalid_token"
	case server.SessionOpenExpired:
		return "expired"
	case server.SessionOpenMissing:
		fallthrough
	default:
		return "missing"
	}
}

func (r *Runtime) newSession(ctx *gox.Context, policy *SessionPolicy, instance server.SessionStorageInstance) server.Session {
	return server.NewSessionWithLifecycle(instance, func() (string, error) {
		token := randomToken(sessionTokenBytes)
		if err := instance.SetToken(token); err != nil {
			return "", err
		}

		writeCookie(ctx.Response, policy.tokenCookie, token, ctx.Request.IsSecure())
		return token, nil
	}, func() error {
		return r.destroySession(instance.ID())
	}, func() {
		deleteCookie(ctx.Response, policy.idCookie, ctx.Request.IsSecure())
		deleteCookie(ctx.Response, policy.tokenCookie, ctx.Request.IsSecure())
	}, func() error {
		return r.saveSessionByID(instance.ID())
	})
}

func (r *Runtime) saveSessionByID(sessionID string) error {
	if r == nil || sessionID == "" {
		return server.ErrSessionNotAvailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), webSocketSessionOperationTimeout)
	defer cancel()
	r.sessionStorageMu.RLock()
	defer r.sessionStorageMu.RUnlock()
	if r.sessionStorage == nil {
		return server.ErrSessionNotAvailable
	}
	return r.sessionStorage.SaveSession(ctx, sessionID)
}

func (r *Runtime) destroySession(sessionID string) error {
	if r == nil || sessionID == "" {
		return server.ErrSessionNotAvailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), webSocketSessionOperationTimeout)
	defer cancel()
	r.sessionStorageMu.RLock()
	defer r.sessionStorageMu.RUnlock()
	if r.sessionStorage == nil {
		return server.ErrSessionNotAvailable
	}
	return r.sessionStorage.DeleteSession(ctx, sessionID)
}

func (r *Runtime) getSessionStorage() server.SessionStorageRuntime {
	r.sessionStorageMu.RLock()
	defer r.sessionStorageMu.RUnlock()

	return r.sessionStorage
}

func newSessionMeta(policy *SessionPolicy, now time.Time) *server.SessionMeta {
	ttl := policy.ttl
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}

	return &server.SessionMeta{
		ID:        randomToken(sessionIDBytes),
		Token:     randomToken(sessionTokenBytes),
		TTL:       ttl,
		ExpireAt:  now.Add(ttl),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func readSessionCookies(req *http.Request, policy *SessionPolicy) (string, string) {
	idCookie, _ := req.Cookie(policy.idCookie)
	tokenCookie, _ := req.Cookie(policy.tokenCookie)

	id := ""
	token := ""

	if idCookie != nil {
		id = strings.TrimSpace(idCookie.Value)
	}

	if tokenCookie != nil {
		token = strings.TrimSpace(tokenCookie.Value)
	}

	return id, token
}

func writeCookie(res *gox.Response, name string, value string, secure bool) {
	if res == nil {
		return
	}
	cookie := (&http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}).String()

	res.Headers.Append("Set-Cookie", cookie)
}

func deleteCookie(res *gox.Response, name string, secure bool) {
	if res == nil {
		return
	}
	cookie := (&http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}).String()
	res.Headers.Append("Set-Cookie", cookie)
}

func randomToken(size int) string {
	if size <= 0 {
		size = 32
	}

	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}

	return base64.RawURLEncoding.EncodeToString(buf)
}
