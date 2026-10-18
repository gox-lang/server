package serverruntime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

func newRuntimeTestSocket(id string) *gox.Socket {
	socket := &gox.Socket{}
	gox.BindSocketIdentity(socket)(id)
	return socket
}

var errTestSocketDirectory = errors.New("socket directory failed")

type failingSocketDirectoryRuntime struct{ *clusterTestRuntime }

func (*failingSocketDirectoryRuntime) GetSocket(context.Context, string) (server.ClusterSocketRecord, bool, error) {
	return server.ClusterSocketRecord{}, false, errTestSocketDirectory
}
func (*failingSocketDirectoryRuntime) ListSockets(context.Context) ([]server.ClusterSocketRecord, error) {
	return nil, errTestSocketDirectory
}
func (*failingSocketDirectoryRuntime) ListSessionSockets(context.Context, string) ([]server.ClusterSocketRecord, error) {
	return nil, errTestSocketDirectory
}
func (*failingSocketDirectoryRuntime) CountSockets(context.Context) (int, error) {
	return 0, errTestSocketDirectory
}

type socketDirectoryTestRuntime struct {
	*clusterTestRuntime
	records map[string]server.ClusterSocketRecord
}

func (r *socketDirectoryTestRuntime) RegisterSocket(_ context.Context, record server.ClusterSocketRecord) error {
	r.records[record.ID] = record
	return nil
}
func (r *socketDirectoryTestRuntime) UnregisterSocket(_ context.Context, id string) error {
	delete(r.records, id)
	return nil
}
func (r *socketDirectoryTestRuntime) GetSocket(_ context.Context, id string) (server.ClusterSocketRecord, bool, error) {
	record, ok := r.records[id]
	return record, ok, nil
}
func (r *socketDirectoryTestRuntime) ListSockets(context.Context) ([]server.ClusterSocketRecord, error) {
	result := make([]server.ClusterSocketRecord, 0, len(r.records))
	for _, record := range r.records {
		result = append(result, record)
	}
	return result, nil
}
func (r *socketDirectoryTestRuntime) ListSessionSockets(_ context.Context, sessionID string) ([]server.ClusterSocketRecord, error) {
	result := []server.ClusterSocketRecord{}
	for _, record := range r.records {
		if record.SessionID == sessionID {
			result = append(result, record)
		}
	}
	return result, nil
}
func (r *socketDirectoryTestRuntime) CountSockets(context.Context) (int, error) {
	return len(r.records), nil
}
func (*socketDirectoryTestRuntime) RefreshInstance(context.Context, string) error { return nil }
func (*socketDirectoryTestRuntime) RemoveInstance(context.Context, string) error  { return nil }

func TestWebSocketRegistryRegistersFindsAndUnregisters(t *testing.T) {
	registry := newWebSocketRegistry()
	session := server.NewSessionReference("session-1")
	first := &gox.Socket{}
	second := &gox.Socket{}

	if err := registry.register(first, session); err != nil {
		t.Fatal(err)
	}
	if err := registry.register(second, session); err != nil {
		t.Fatal(err)
	}
	if first.ID() == "" || second.ID() == "" || first.ID() == second.ID() {
		t.Fatalf("socket IDs must be non-empty and unique: %q %q", first.ID(), second.ID())
	}
	if first.Session.ID() != session.ID() || second.Session.ID() != session.ID() {
		t.Fatal("registry did not attach the live session")
	}
	if got, ok := registry.findByID(first.ID()); !ok || got != first {
		t.Fatal("registered socket was not found by ID")
	}
	if got := registry.findBySessionID(session.ID()); len(got) != 2 {
		t.Fatalf("session sockets = %d, want 2", len(got))
	}
	if registry.count() != 2 || len(registry.all()) != 2 {
		t.Fatal("registry count does not match registered sockets")
	}
	if !registry.unregister(first.ID(), first) {
		t.Fatal("first unregister did not remove the socket")
	}
	if registry.unregister(first.ID(), first) {
		t.Fatal("unregister must be idempotent")
	}
	if got := registry.findBySessionID(session.ID()); len(got) != 1 || got[0] != second {
		t.Fatalf("unexpected sockets after unregister: %#v", got)
	}
}

func TestWebSocketRegistryDoesNotExposeMutableInternalSlices(t *testing.T) {
	registry := newWebSocketRegistry()
	socket := &gox.Socket{}
	if err := registry.register(socket, server.NewSessionReference("session-1")); err != nil {
		t.Fatal(err)
	}

	all := registry.all()
	bySession := registry.findBySessionID("session-1")
	all[0] = nil
	bySession[0] = nil
	if got, ok := registry.findByID(socket.ID()); !ok || got != socket {
		t.Fatal("caller mutated registry through a returned slice")
	}
	if got := registry.findBySessionID("missing"); got == nil || len(got) != 0 {
		t.Fatalf("empty collection must be non-nil: %#v", got)
	}
}

func TestWebSocketRegistrySiblingsAreDynamicAndExcludeCurrentSocket(t *testing.T) {
	registry := newWebSocketRegistry()
	session := server.NewSessionReference("session-1")
	current := &gox.Socket{}
	other := &gox.Socket{}
	differentSession := &gox.Socket{}
	if err := registry.register(current, session); err != nil {
		t.Fatal(err)
	}
	gox.BindSocketSiblings(current, func() []*gox.Socket {
		return registry.siblings(current)
	})
	if current.Siblings.Count() != 0 {
		t.Fatal("a socket must not be its own sibling")
	}
	if err := registry.register(other, session); err != nil {
		t.Fatal(err)
	}
	if err := registry.register(differentSession, server.NewSessionReference("session-2")); err != nil {
		t.Fatal(err)
	}
	if got := current.Siblings.All(); len(got) != 1 || got[0] != other {
		t.Fatalf("siblings = %#v, want only the other session socket", got)
	}
	registry.unregister(other.ID(), other)
	if current.Siblings.Count() != 0 {
		t.Fatal("siblings did not reflect unregister dynamically")
	}
}

func TestWebSocketRegistrySupportsConcurrentAccess(t *testing.T) {
	registry := newWebSocketRegistry()
	const total = 64
	sockets := make([]*gox.Socket, total)
	var wg sync.WaitGroup
	for index := range sockets {
		sockets[index] = &gox.Socket{}
		wg.Add(1)
		go func(socket *gox.Socket) {
			defer wg.Done()
			if err := registry.register(socket, server.NewSessionReference("session")); err != nil {
				t.Error(err)
			}
		}(sockets[index])
	}
	wg.Wait()
	if registry.count() != total {
		t.Fatalf("count = %d, want %d", registry.count(), total)
	}

	for _, socket := range sockets {
		wg.Add(1)
		go func(socket *gox.Socket) {
			defer wg.Done()
			registry.unregister(socket.ID(), socket)
		}(socket)
	}
	wg.Wait()
	if registry.count() != 0 {
		t.Fatalf("count after unregister = %d", registry.count())
	}
}

func TestRuntimeBindsRegistryToGoxSocketRegistry(t *testing.T) {
	runtime := &Runtime{}
	runtime.bindSocketRegistry()
	defer runtime.unbindSocketRegistry()
	registry := runtime.webSocketRegistry()
	first := &gox.Socket{}
	second := &gox.Socket{}
	if err := registry.register(first, server.NewSessionReference("session-1")); err != nil {
		t.Fatal(err)
	}
	if err := registry.register(second, server.NewSessionReference("session-2")); err != nil {
		t.Fatal(err)
	}
	if gox.Sockets.Count() != 2 {
		t.Fatalf("global socket count = %d, want 2", gox.Sockets.Count())
	}
	if found, ok := gox.Sockets.GetByID(first.ID()); !ok || found != first {
		t.Fatal("global FindByID did not use the runtime registry")
	}
	if found := gox.Sockets.FindBySessionID("session-2").All(); len(found) != 1 || found[0] != second {
		t.Fatal("global session collection did not use the runtime registry")
	}
	registry.unregister(first.ID(), first)
	if gox.Sockets.Count() != 1 {
		t.Fatal("global socket view did not reflect unregister")
	}
}

func TestRuntimeMaterializesGlobalSocketDirectoryAsLocalSocketsAndRemoteProxies(t *testing.T) {
	directory := &socketDirectoryTestRuntime{clusterTestRuntime: &clusterTestRuntime{}, records: map[string]server.ClusterSocketRecord{}}
	runtime := &Runtime{instanceID: "instance-local", clusterRuntime: directory}
	runtime.bindSocketRegistry()
	defer runtime.unbindSocketRegistry()
	local := &gox.Socket{}
	gox.BindSocketLocation(local, runtime.instanceID, true)
	if err := runtime.webSocketRegistry().register(local, server.NewSessionReference("session-1")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.registerSocketDirectory(local); err != nil {
		t.Fatal(err)
	}
	directory.records["remote"] = server.ClusterSocketRecord{ID: "remote", SessionID: "session-1", InstanceID: "instance-remote"}

	if gox.Sockets.Count() != 2 {
		t.Fatalf("global count = %d, want 2", gox.Sockets.Count())
	}
	all := gox.Sockets.All()
	if len(all) != 2 {
		t.Fatalf("global sockets = %#v", all)
	}
	remote, found := gox.Sockets.GetByID("remote")
	if !found || remote == nil || remote.IsLocal() || remote.InstanceID() != "instance-remote" || remote.Session.ID() != "session-1" {
		t.Fatalf("remote proxy = %#v, found=%v", remote, found)
	}
	foundLocal, found := gox.Sockets.GetByID(local.ID())
	if !found || foundLocal != local || !foundLocal.IsLocal() {
		t.Fatal("local lookup did not preserve the real socket")
	}
	if siblings := runtime.findClusterSocketsBySessionID("session-1"); len(siblings) != 2 {
		t.Fatalf("global session sockets = %#v", siblings)
	}
}

func TestPublicSocketViewsNeverFallBackToTheLocalRegistryWhenClusterDirectoryFails(t *testing.T) {
	cluster := &failingSocketDirectoryRuntime{clusterTestRuntime: &clusterTestRuntime{}}
	runtime := &Runtime{instanceID: "instance-local", clusterRuntime: cluster}
	runtime.bindSocketRegistry()
	defer runtime.unbindSocketRegistry()
	local := &gox.Socket{}
	gox.BindSocketLocation(local, runtime.instanceID, true)
	if err := runtime.webSocketRegistry().register(local, server.NewSessionReference("session-1")); err != nil {
		t.Fatal(err)
	}

	if sockets := gox.Sockets.All(); len(sockets) != 0 {
		t.Fatalf("partial All leaked local sockets: %#v", sockets)
	}
	if count := gox.Sockets.Count(); count != 0 {
		t.Fatalf("partial Count = %d", count)
	}
	if socket, found := gox.Sockets.GetByID(local.ID()); found || socket != nil {
		t.Fatalf("partial GetByID leaked local socket: %#v", socket)
	}
	if sockets := gox.Sockets.FindBySessionID("session-1").All(); len(sockets) != 0 {
		t.Fatalf("partial session view leaked local sockets: %#v", sockets)
	}
}

func TestGlobalSocketViewsRemainRaceSafeDuringConcurrentConnectAndDisconnect(t *testing.T) {
	cluster := &clusterTestRuntime{}
	runtime := &Runtime{instanceID: "instance-local", clusterRuntime: cluster}
	runtime.bindSocketRegistry()
	defer runtime.unbindSocketRegistry()
	const total = 64
	var writers sync.WaitGroup
	for index := 0; index < total; index++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			socket := &gox.Socket{}
			gox.BindSocketLocation(socket, runtime.instanceID, true)
			if err := runtime.webSocketRegistry().register(socket, server.NewSessionReference("session")); err != nil {
				t.Error(err)
				return
			}
			if err := runtime.registerSocketDirectory(socket); err != nil {
				t.Error(err)
				return
			}
			_ = gox.Sockets.All()
			_, _ = gox.Sockets.GetByID(socket.ID())
			_ = gox.Sockets.FindBySessionID("session").All()
			_ = gox.Sockets.Count()
			if runtime.webSocketRegistry().unregister(socket.ID(), socket) {
				runtime.unregisterSocketDirectory(socket.ID())
			}
		}()
	}
	writers.Wait()
	if count := gox.Sockets.Count(); count != 0 {
		t.Fatalf("global sockets after stress = %d", count)
	}
}
