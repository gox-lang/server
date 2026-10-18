package serverruntime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

const socketDirectoryTimeout = 2 * time.Second

// webSocketRegistry is the runtime-owned source of truth for live sockets.
// A registry belongs to exactly one Runtime; sockets from different servers
// are therefore never mixed, even when several runtimes exist in one process.
type webSocketRegistry struct {
	mu        sync.RWMutex
	byID      map[string]*gox.Socket
	bySession map[string]map[string]*gox.Socket
}

func newWebSocketRegistry() *webSocketRegistry {
	return &webSocketRegistry{
		byID:      make(map[string]*gox.Socket),
		bySession: make(map[string]map[string]*gox.Socket),
	}
}

func (r *Runtime) webSocketRegistry() *webSocketRegistry {
	if r == nil {
		return nil
	}
	r.webSocketsOnce.Do(func() {
		r.webSockets = newWebSocketRegistry()
	})
	return r.webSockets
}

func (r *Runtime) bindSocketRegistry() {
	if r == nil {
		return
	}
	r.webSocketRegistry()
	r.unbindSocketRegistry = gox.BindSocketRegistry(gox.SocketRegistryBindings{
		All:             r.allClusterSockets,
		Count:           r.countClusterSockets,
		FindByID:        r.findClusterSocketByID,
		FindBySessionID: r.findClusterSocketsBySessionID,
		Emit: func(event string, values []any) {
			r.emitClusterSocketEvent(server.ClusterTarget{Kind: server.ClusterTargetAll}, event, values)
		},
		EmitSession: func(sessionID string, event string, values []any) {
			r.emitClusterSocketEvent(server.ClusterTarget{Kind: server.ClusterTargetSession, SessionID: sessionID}, event, values)
		},
		Disconnect: func(code gox.WSCloseCode) {
			r.disconnectClusterSockets(server.ClusterTarget{Kind: server.ClusterTargetAll}, "", code)
		},
		DisconnectSession: func(sessionID string, code gox.WSCloseCode) {
			r.disconnectClusterSockets(server.ClusterTarget{Kind: server.ClusterTargetSession, SessionID: sessionID}, "", code)
		},
	})
}

func (r *Runtime) socketDirectory() (server.ClusterSocketDirectory, server.ClusterRuntime) {
	if r == nil {
		return nil, nil
	}
	r.sessionStorageMu.RLock()
	cluster := r.clusterRuntime
	r.sessionStorageMu.RUnlock()
	if cluster == nil {
		return nil, nil
	}
	return cluster, cluster
}

func (r *Runtime) directoryContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), socketDirectoryTimeout)
}

func (r *Runtime) dispatchSocketDirectoryError(cluster server.ClusterRuntime, operation string, err error) {
	if err != nil {
		r.dispatchClusterError(cluster, "socket-directory-"+operation, "Distributed socket directory operation failed", err)
	}
}

func (r *Runtime) allClusterSockets() []*gox.Socket {
	registry := r.webSocketRegistry()
	directory, cluster := r.socketDirectory()
	if directory == nil {
		return registry.all()
	}
	ctx, cancel := r.directoryContext()
	records, err := directory.ListSockets(ctx)
	cancel()
	if err != nil {
		r.dispatchSocketDirectoryError(cluster, "list", err)
		return []*gox.Socket{}
	}
	return r.materializeClusterSockets(records)
}

func (r *Runtime) countClusterSockets() int {
	directory, cluster := r.socketDirectory()
	if directory == nil {
		return r.webSocketRegistry().count()
	}
	ctx, cancel := r.directoryContext()
	count, err := directory.CountSockets(ctx)
	cancel()
	if err != nil {
		r.dispatchSocketDirectoryError(cluster, "count", err)
		return 0
	}
	return count
}

func (r *Runtime) findClusterSocketByID(id string) (*gox.Socket, bool) {
	directory, cluster := r.socketDirectory()
	if directory == nil {
		return r.webSocketRegistry().findByID(id)
	}
	ctx, cancel := r.directoryContext()
	record, found, err := directory.GetSocket(ctx, id)
	cancel()
	if err != nil {
		r.dispatchSocketDirectoryError(cluster, "get", err)
		return nil, false
	}
	if !found {
		return nil, false
	}
	return r.materializeClusterSocket(record), true
}

func (r *Runtime) findClusterSocketsBySessionID(sessionID string) []*gox.Socket {
	directory, cluster := r.socketDirectory()
	if directory == nil {
		return r.webSocketRegistry().findBySessionID(sessionID)
	}
	ctx, cancel := r.directoryContext()
	records, err := directory.ListSessionSockets(ctx, sessionID)
	cancel()
	if err != nil {
		r.dispatchSocketDirectoryError(cluster, "list-session", err)
		return []*gox.Socket{}
	}
	return r.materializeClusterSockets(records)
}

func (r *Runtime) materializeClusterSockets(records []server.ClusterSocketRecord) []*gox.Socket {
	result := make([]*gox.Socket, 0, len(records))
	for _, record := range records {
		if socket := r.materializeClusterSocket(record); socket != nil {
			result = append(result, socket)
		}
	}
	return result
}

func (r *Runtime) materializeClusterSocket(record server.ClusterSocketRecord) *gox.Socket {
	if record.ID == "" || record.InstanceID == "" {
		return nil
	}
	if socket, found := r.webSocketRegistry().findByID(record.ID); found {
		return socket
	}
	return r.newClusterSocketProxyRecord(record)
}

func (r *Runtime) registerSocketDirectory(socket *gox.Socket) error {
	directory, _ := r.socketDirectory()
	if directory == nil || socket == nil {
		return nil
	}
	ctx, cancel := r.directoryContext()
	err := directory.RegisterSocket(ctx, server.ClusterSocketRecord{ID: socket.ID(), SessionID: socket.Session.ID(), InstanceID: r.instanceID})
	cancel()
	return err
}

func (r *Runtime) unregisterSocketDirectory(socketID string) {
	directory, cluster := r.socketDirectory()
	if directory == nil || socketID == "" {
		return
	}
	ctx, cancel := r.directoryContext()
	err := directory.UnregisterSocket(ctx, socketID)
	cancel()
	r.dispatchSocketDirectoryError(cluster, "unregister", err)
}

func (r *Runtime) synchronizeSocketDirectory() {
	directory, cluster := r.socketDirectory()
	if directory == nil {
		return
	}
	for _, socket := range r.webSocketRegistry().all() {
		ctx, cancel := r.directoryContext()
		err := directory.RegisterSocket(ctx, server.ClusterSocketRecord{ID: socket.ID(), SessionID: socket.Session.ID(), InstanceID: r.instanceID})
		cancel()
		if err != nil {
			r.dispatchSocketDirectoryError(cluster, "synchronize", err)
		}
	}
}

func newWebSocketID() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (registry *webSocketRegistry) register(socket *gox.Socket, session gox.Session, identity ...func(string)) error {
	if registry == nil || socket == nil {
		return errors.New("WebSocket registry requires a socket")
	}
	var assignIdentity func(string)
	if len(identity) == 0 {
		assignIdentity = gox.BindSocketIdentity(socket)
	} else {
		assignIdentity = identity[0]
	}
	if assignIdentity == nil {
		return errors.New("WebSocket identity is unavailable")
	}

	for {
		id, err := newWebSocketID()
		if err != nil {
			return err
		}

		registry.mu.Lock()
		if _, exists := registry.byID[id]; exists {
			registry.mu.Unlock()
			continue
		}

		assignIdentity(id)
		socket.Session = session
		registry.byID[id] = socket
		if session.ID() != "" {
			sockets := registry.bySession[session.ID()]
			if sockets == nil {
				sockets = make(map[string]*gox.Socket)
				registry.bySession[session.ID()] = sockets
			}
			sockets[id] = socket
		}
		registry.mu.Unlock()
		return nil
	}
}

// unregister removes only the exact socket registered under id. The identity
// check prevents a stale connection from removing a future reused entry.
func (registry *webSocketRegistry) unregister(id string, socket *gox.Socket) bool {
	if registry == nil || id == "" || socket == nil {
		return false
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	current, exists := registry.byID[id]
	if !exists || current != socket {
		return false
	}
	delete(registry.byID, id)

	sessionID := socket.Session.ID()
	if sockets := registry.bySession[sessionID]; sockets != nil {
		delete(sockets, id)
		if len(sockets) == 0 {
			delete(registry.bySession, sessionID)
		}
	}
	return true
}

func (registry *webSocketRegistry) findByID(id string) (*gox.Socket, bool) {
	if registry == nil || id == "" {
		return nil, false
	}
	registry.mu.RLock()
	socket, ok := registry.byID[id]
	registry.mu.RUnlock()
	return socket, ok
}

func (registry *webSocketRegistry) findBySessionID(sessionID string) []*gox.Socket {
	if registry == nil || sessionID == "" {
		return []*gox.Socket{}
	}
	registry.mu.RLock()
	sockets := registry.bySession[sessionID]
	result := make([]*gox.Socket, 0, len(sockets))
	for _, socket := range sockets {
		result = append(result, socket)
	}
	registry.mu.RUnlock()
	return result
}

func (registry *webSocketRegistry) siblings(current *gox.Socket) []*gox.Socket {
	if registry == nil || current == nil || current.Session.ID() == "" {
		return []*gox.Socket{}
	}
	registry.mu.RLock()
	sockets := registry.bySession[current.Session.ID()]
	result := make([]*gox.Socket, 0, len(sockets))
	for _, socket := range sockets {
		if socket != nil && socket != current {
			result = append(result, socket)
		}
	}
	registry.mu.RUnlock()
	return result
}

func (registry *webSocketRegistry) all() []*gox.Socket {
	if registry == nil {
		return []*gox.Socket{}
	}
	registry.mu.RLock()
	result := make([]*gox.Socket, 0, len(registry.byID))
	for _, socket := range registry.byID {
		result = append(result, socket)
	}
	registry.mu.RUnlock()
	return result
}

func (registry *webSocketRegistry) count() int {
	if registry == nil {
		return 0
	}
	registry.mu.RLock()
	count := len(registry.byID)
	registry.mu.RUnlock()
	return count
}
