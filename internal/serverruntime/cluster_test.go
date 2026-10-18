package serverruntime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gox-lang/gox/server"
)

type clusterTestAdapter struct {
	openCount atomic.Int32
	config    server.ClusterConfig
	runtime   *clusterTestRuntime
	openErr   error
}

func (a *clusterTestAdapter) GetName() string { return "cluster-test" }

func (a *clusterTestAdapter) OpenCluster(ctx context.Context, config server.ClusterConfig) (server.ClusterRuntime, error) {
	a.openCount.Add(1)
	a.config = config
	if a.openErr != nil {
		return nil, a.openErr
	}
	memory, err := server.NewMemorySessionAdapter().OpenSessionStorage(ctx, server.SessionStorageConfig{
		ProjectName: config.ProjectName,
		TTL:         config.SessionTTL,
	})
	if err != nil {
		return nil, err
	}
	a.runtime = &clusterTestRuntime{SessionStorageRuntime: memory}
	return a.runtime, nil
}

type clusterTestRuntime struct {
	server.SessionStorageRuntime
	closeCount  atomic.Int32
	directoryMu sync.RWMutex
	directory   map[string]server.ClusterSocketRecord
	groups      map[string]map[string]struct{}
	channels    map[string]map[string]struct{}
}

func (r *clusterTestRuntime) JoinChannel(_ context.Context, channelID string, socket server.ClusterSocketRecord) error {
	r.directoryMu.Lock()
	if r.channels == nil {
		r.channels = make(map[string]map[string]struct{})
	}
	members := r.channels[channelID]
	if members == nil {
		members = make(map[string]struct{})
		r.channels[channelID] = members
	}
	members[socket.ID] = struct{}{}
	r.directoryMu.Unlock()
	return nil
}
func (r *clusterTestRuntime) LeaveChannel(_ context.Context, channelID, socketID, instanceID string) error {
	r.directoryMu.Lock()
	if members := r.channels[channelID]; members != nil {
		delete(members, socketID)
		if len(members) == 0 {
			delete(r.channels, channelID)
		}
	}
	r.directoryMu.Unlock()
	return nil
}
func (r *clusterTestRuntime) LeaveAllChannels(_ context.Context, socketID, instanceID string) error {
	r.directoryMu.Lock()
	for channelID, members := range r.channels {
		delete(members, socketID)
		if len(members) == 0 {
			delete(r.channels, channelID)
		}
	}
	r.directoryMu.Unlock()
	return nil
}
func (r *clusterTestRuntime) ListChannels(context.Context) ([]string, error) {
	r.directoryMu.RLock()
	result := make([]string, 0, len(r.channels))
	for channelID := range r.channels {
		result = append(result, channelID)
	}
	r.directoryMu.RUnlock()
	return result, nil
}
func (r *clusterTestRuntime) ListChannelSockets(_ context.Context, channelID string) ([]server.ClusterSocketRecord, error) {
	r.directoryMu.RLock()
	result := []server.ClusterSocketRecord{}
	for id := range r.channels[channelID] {
		if record, ok := r.directory[id]; ok {
			result = append(result, record)
		}
	}
	r.directoryMu.RUnlock()
	return result, nil
}
func (r *clusterTestRuntime) ListSocketChannels(_ context.Context, socketID string) ([]string, error) {
	r.directoryMu.RLock()
	result := []string{}
	for channelID, members := range r.channels {
		if _, ok := members[socketID]; ok {
			result = append(result, channelID)
		}
	}
	r.directoryMu.RUnlock()
	return result, nil
}
func (r *clusterTestRuntime) CountChannels(context.Context) (int, error) {
	r.directoryMu.RLock()
	count := len(r.channels)
	r.directoryMu.RUnlock()
	return count, nil
}
func (r *clusterTestRuntime) CountChannelSockets(_ context.Context, channelID string) (int, error) {
	r.directoryMu.RLock()
	count := len(r.channels[channelID])
	r.directoryMu.RUnlock()
	return count, nil
}
func (r *clusterTestRuntime) CloseChannel(_ context.Context, channelID string) error {
	r.directoryMu.Lock()
	delete(r.channels, channelID)
	r.directoryMu.Unlock()
	return nil
}

func (r *clusterTestRuntime) JoinGroup(_ context.Context, group string, socket server.ClusterSocketRecord) error {
	r.directoryMu.Lock()
	if r.groups == nil {
		r.groups = make(map[string]map[string]struct{})
	}
	members := r.groups[group]
	if members == nil {
		members = make(map[string]struct{})
		r.groups[group] = members
	}
	members[socket.ID] = struct{}{}
	r.directoryMu.Unlock()
	return nil
}
func (r *clusterTestRuntime) LeaveGroup(_ context.Context, group, socketID, instanceID string) error {
	r.directoryMu.Lock()
	if members := r.groups[group]; members != nil {
		delete(members, socketID)
		if len(members) == 0 {
			delete(r.groups, group)
		}
	}
	r.directoryMu.Unlock()
	return nil
}
func (r *clusterTestRuntime) LeaveAllGroups(_ context.Context, socketID, instanceID string) error {
	r.directoryMu.Lock()
	for group, members := range r.groups {
		delete(members, socketID)
		if len(members) == 0 {
			delete(r.groups, group)
		}
	}
	r.directoryMu.Unlock()
	return nil
}
func (r *clusterTestRuntime) ListGroups(context.Context) ([]string, error) {
	r.directoryMu.RLock()
	result := make([]string, 0, len(r.groups))
	for group := range r.groups {
		result = append(result, group)
	}
	r.directoryMu.RUnlock()
	return result, nil
}
func (r *clusterTestRuntime) ListGroupSockets(_ context.Context, group string) ([]server.ClusterSocketRecord, error) {
	r.directoryMu.RLock()
	result := []server.ClusterSocketRecord{}
	for id := range r.groups[group] {
		if record, ok := r.directory[id]; ok {
			result = append(result, record)
		}
	}
	r.directoryMu.RUnlock()
	return result, nil
}
func (r *clusterTestRuntime) ListSocketGroups(_ context.Context, socketID string) ([]string, error) {
	r.directoryMu.RLock()
	result := []string{}
	for group, members := range r.groups {
		if _, ok := members[socketID]; ok {
			result = append(result, group)
		}
	}
	r.directoryMu.RUnlock()
	return result, nil
}
func (r *clusterTestRuntime) CountGroups(context.Context) (int, error) {
	r.directoryMu.RLock()
	count := len(r.groups)
	r.directoryMu.RUnlock()
	return count, nil
}
func (r *clusterTestRuntime) CountGroupSockets(_ context.Context, group string) (int, error) {
	r.directoryMu.RLock()
	count := len(r.groups[group])
	r.directoryMu.RUnlock()
	return count, nil
}

func (r *clusterTestRuntime) RegisterSocket(_ context.Context, record server.ClusterSocketRecord) error {
	r.directoryMu.Lock()
	if r.directory == nil {
		r.directory = make(map[string]server.ClusterSocketRecord)
	}
	r.directory[record.ID] = record
	r.directoryMu.Unlock()
	return nil
}
func (r *clusterTestRuntime) UnregisterSocket(_ context.Context, id string) error {
	r.directoryMu.Lock()
	delete(r.directory, id)
	r.directoryMu.Unlock()
	return nil
}
func (r *clusterTestRuntime) GetSocket(_ context.Context, id string) (server.ClusterSocketRecord, bool, error) {
	r.directoryMu.RLock()
	record, ok := r.directory[id]
	r.directoryMu.RUnlock()
	return record, ok, nil
}
func (r *clusterTestRuntime) ListSockets(context.Context) ([]server.ClusterSocketRecord, error) {
	r.directoryMu.RLock()
	result := make([]server.ClusterSocketRecord, 0, len(r.directory))
	for _, record := range r.directory {
		result = append(result, record)
	}
	r.directoryMu.RUnlock()
	return result, nil
}
func (r *clusterTestRuntime) ListSessionSockets(_ context.Context, sessionID string) ([]server.ClusterSocketRecord, error) {
	r.directoryMu.RLock()
	result := []server.ClusterSocketRecord{}
	for _, record := range r.directory {
		if record.SessionID == sessionID {
			result = append(result, record)
		}
	}
	r.directoryMu.RUnlock()
	return result, nil
}
func (r *clusterTestRuntime) CountSockets(context.Context) (int, error) {
	r.directoryMu.RLock()
	count := len(r.directory)
	r.directoryMu.RUnlock()
	return count, nil
}
func (*clusterTestRuntime) RefreshInstance(context.Context, string) error { return nil }
func (r *clusterTestRuntime) RemoveInstance(_ context.Context, instanceID string) error {
	r.directoryMu.Lock()
	for id, record := range r.directory {
		if record.InstanceID == instanceID {
			delete(r.directory, id)
		}
	}
	r.directoryMu.Unlock()
	return nil
}

func (r *clusterTestRuntime) Close() error {
	r.closeCount.Add(1)
	if r.SessionStorageRuntime == nil {
		return nil
	}
	return r.SessionStorageRuntime.Close()
}

func (*clusterTestRuntime) InstanceID() string                                      { return "test-instance" }
func (*clusterTestRuntime) GetName() string                                         { return "test-cluster" }
func (*clusterTestRuntime) PublishSessionInvalidated(context.Context, string) error { return nil }
func (*clusterTestRuntime) PublishSessionDestroyed(context.Context, string) error   { return nil }
func (*clusterTestRuntime) PublishSocketPacket(context.Context, server.ClusterTarget, []byte) error {
	return nil
}
func (*clusterTestRuntime) PublishSocketRequest(context.Context, string, []byte, []byte) error {
	return nil
}
func (*clusterTestRuntime) PublishSocketResponse(context.Context, string, []byte, []byte) error {
	return nil
}
func (*clusterTestRuntime) PublishSocketRequestCancel(context.Context, []byte) error { return nil }
func (*clusterTestRuntime) PublishSocketDisconnect(context.Context, server.ClusterTarget, string, uint16) error {
	return nil
}
func (*clusterTestRuntime) PublishChannelClose(context.Context, string) error { return nil }
func (*clusterTestRuntime) SubscribeClusterEvents(context.Context, server.ClusterEventFunc, server.ClusterErrorFunc) error {
	return nil
}

func TestOpenSessionRuntimeUsesMemoryWhenClusterIsNil(t *testing.T) {
	policy, err := NewSessionPolicy("memory-project", server.SessionConfig{TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	storage, cluster, err := OpenSessionRuntime(nil, policy, "test-instance")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	if cluster != nil {
		t.Fatal("memory fallback unexpectedly returned a cluster runtime")
	}
	if storage.GetName() != "memory" {
		t.Fatalf("storage name = %q, want memory", storage.GetName())
	}
}

func TestOpenSessionRuntimeOpensClusterExactlyOnce(t *testing.T) {
	adapter := &clusterTestAdapter{}
	policy, err := NewSessionPolicy("cluster-project", server.SessionConfig{TTL: 45 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	storage, cluster, err := OpenSessionRuntime(adapter, policy, "test-instance")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.openCount.Load() != 1 {
		t.Fatalf("OpenCluster calls = %d, want 1", adapter.openCount.Load())
	}
	if adapter.config.ProjectName != "cluster-project" || adapter.config.SessionTTL != 45*time.Minute || adapter.config.InstanceID != "test-instance" {
		t.Fatalf("cluster config = %#v", adapter.config)
	}
	if storage != server.SessionStorageRuntime(cluster) || cluster != adapter.runtime {
		t.Fatal("session and cluster runtimes do not share the same instance")
	}
	if cluster.InstanceID() == "" {
		t.Fatal("cluster runtime returned an empty instance ID")
	}
	if err := cluster.Close(); err != nil {
		t.Fatal(err)
	}
	if adapter.runtime.closeCount.Load() != 1 {
		t.Fatalf("Close calls = %d, want 1", adapter.runtime.closeCount.Load())
	}
}

func TestOpenSessionRuntimeRejectsAdapterIdentityReplacement(t *testing.T) {
	adapter := &clusterTestAdapter{}
	policy, err := NewSessionPolicy("cluster-project", server.SessionConfig{TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	storage, cluster, err := OpenSessionRuntime(adapter, policy, "different-instance")
	if err == nil || storage != nil || cluster != nil {
		t.Fatalf("identity replacement was accepted: storage=%#v cluster=%#v err=%v", storage, cluster, err)
	}
	if adapter.runtime == nil || adapter.runtime.closeCount.Load() != 1 {
		t.Fatal("rejected cluster runtime was not closed")
	}
}

func TestOpenSessionRuntimePassesWebSocketMessageLimitToCluster(t *testing.T) {
	adapter := &clusterTestAdapter{}
	policy, _ := NewSessionPolicy("limit-project", server.SessionConfig{TTL: time.Hour})
	storage, cluster, err := OpenSessionRuntime(adapter, policy, "test-instance", 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cluster.Close() })
	if storage == nil || adapter.config.MaxMessageSize != 16<<20 {
		t.Fatalf("cluster MaxMessageSize = %d, want %d", adapter.config.MaxMessageSize, int64(16<<20))
	}
}

func TestRuntimeCloseDoesNotCloseClusterTwice(t *testing.T) {
	adapter := &clusterTestAdapter{}
	policy, _ := NewSessionPolicy("close-project", server.SessionConfig{TTL: time.Hour})
	storage, cluster, err := OpenSessionRuntime(adapter, policy, "test-instance")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{sessionStorage: storage, clusterRuntime: cluster}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if adapter.runtime.closeCount.Load() != 1 {
		t.Fatalf("Close calls = %d, want 1", adapter.runtime.closeCount.Load())
	}
}
