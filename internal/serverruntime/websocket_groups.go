package serverruntime

import (
	"fmt"
	"sort"
	"sync"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

type socketGroupRegistry struct {
	mu       sync.RWMutex
	byGroup  map[string]map[string]struct{}
	bySocket map[string]map[string]struct{}
}

func newSocketGroupRegistry() *socketGroupRegistry {
	return &socketGroupRegistry{
		byGroup:  make(map[string]map[string]struct{}),
		bySocket: make(map[string]map[string]struct{}),
	}
}

func (r *Runtime) socketGroupRegistry() *socketGroupRegistry {
	if r == nil {
		return nil
	}
	r.groupsOnce.Do(func() { r.groups = newSocketGroupRegistry() })
	return r.groups
}

func (registry *socketGroupRegistry) join(group, socketID string) {
	if registry == nil || group == "" || socketID == "" {
		return
	}
	registry.mu.Lock()
	members := registry.byGroup[group]
	if members == nil {
		members = make(map[string]struct{})
		registry.byGroup[group] = members
	}
	members[socketID] = struct{}{}
	groups := registry.bySocket[socketID]
	if groups == nil {
		groups = make(map[string]struct{})
		registry.bySocket[socketID] = groups
	}
	groups[group] = struct{}{}
	registry.mu.Unlock()
}

func (registry *socketGroupRegistry) leave(group, socketID string) {
	if registry == nil || group == "" || socketID == "" {
		return
	}
	registry.mu.Lock()
	if members := registry.byGroup[group]; members != nil {
		delete(members, socketID)
		if len(members) == 0 {
			delete(registry.byGroup, group)
		}
	}
	if groups := registry.bySocket[socketID]; groups != nil {
		delete(groups, group)
		if len(groups) == 0 {
			delete(registry.bySocket, socketID)
		}
	}
	registry.mu.Unlock()
}

func (registry *socketGroupRegistry) leaveAll(socketID string) {
	if registry == nil || socketID == "" {
		return
	}
	registry.mu.Lock()
	for group := range registry.bySocket[socketID] {
		members := registry.byGroup[group]
		delete(members, socketID)
		if len(members) == 0 {
			delete(registry.byGroup, group)
		}
	}
	delete(registry.bySocket, socketID)
	registry.mu.Unlock()
}

func (registry *socketGroupRegistry) list() []string {
	if registry == nil {
		return []string{}
	}
	registry.mu.RLock()
	result := make([]string, 0, len(registry.byGroup))
	for group := range registry.byGroup {
		result = append(result, group)
	}
	registry.mu.RUnlock()
	sort.Strings(result)
	return result
}

func (registry *socketGroupRegistry) groups(socketID string) []string {
	if registry == nil || socketID == "" {
		return []string{}
	}
	registry.mu.RLock()
	result := make([]string, 0, len(registry.bySocket[socketID]))
	for group := range registry.bySocket[socketID] {
		result = append(result, group)
	}
	registry.mu.RUnlock()
	sort.Strings(result)
	return result
}

func (registry *socketGroupRegistry) memberIDs(group string) []string {
	if registry == nil || group == "" {
		return []string{}
	}
	registry.mu.RLock()
	result := make([]string, 0, len(registry.byGroup[group]))
	for socketID := range registry.byGroup[group] {
		result = append(result, socketID)
	}
	registry.mu.RUnlock()
	sort.Strings(result)
	return result
}

func (registry *socketGroupRegistry) count() int {
	if registry == nil {
		return 0
	}
	registry.mu.RLock()
	count := len(registry.byGroup)
	registry.mu.RUnlock()
	return count
}

func (registry *socketGroupRegistry) countMembers(group string) int {
	if registry == nil || group == "" {
		return 0
	}
	registry.mu.RLock()
	count := len(registry.byGroup[group])
	registry.mu.RUnlock()
	return count
}

func (r *Runtime) localGroupMembers(group string) []*gox.Socket {
	ids := r.socketGroupRegistry().memberIDs(group)
	result := make([]*gox.Socket, 0, len(ids))
	for _, socketID := range ids {
		if socket, found := r.webSocketRegistry().findByID(socketID); found {
			result = append(result, socket)
		}
	}
	return result
}

func (r *Runtime) emitToGroup(group, event string, values []any) {
	r.sessionStorageMu.RLock()
	clustered := r.clusterRuntime != nil
	r.sessionStorageMu.RUnlock()
	if clustered {
		r.emitClusterSocketEvent(server.ClusterTarget{Kind: server.ClusterTargetGroup, GroupName: group}, event, values)
		return
	}
	for _, socket := range r.localGroupMembers(group) {
		socket.Emit(event, values...)
	}
}

func (r *Runtime) groupDirectory() (server.ClusterGroupDirectory, server.ClusterRuntime) {
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

func (r *Runtime) dispatchGroupDirectoryError(cluster server.ClusterRuntime, operation string, err error) {
	if err != nil {
		r.dispatchClusterError(cluster, "group-directory-"+operation, "Distributed group directory operation failed", err)
	}
}

func (r *Runtime) socketGroupRecord(socket *gox.Socket) server.ClusterSocketRecord {
	if socket == nil {
		return server.ClusterSocketRecord{}
	}
	instanceID := socket.InstanceID()
	if instanceID == "" {
		instanceID = r.instanceID
	}
	return server.ClusterSocketRecord{ID: socket.ID(), SessionID: socket.Session.ID(), InstanceID: instanceID}
}

func (r *Runtime) joinSocketGroup(socket *gox.Socket, group string) error {
	if socket == nil || socket.ID() == "" {
		return fmt.Errorf("socket is not connected")
	}
	directory, cluster := r.groupDirectory()
	if directory == nil {
		current, found := r.webSocketRegistry().findByID(socket.ID())
		if !found || current != socket {
			return fmt.Errorf("socket is not connected")
		}
		r.socketGroupRegistry().join(group, socket.ID())
		return nil
	}
	record := r.socketGroupRecord(socket)
	ctx, cancel := r.directoryContext()
	err := directory.JoinGroup(ctx, group, record)
	cancel()
	if err != nil {
		r.dispatchGroupDirectoryError(cluster, "join", err)
		return err
	}
	if record.InstanceID == r.instanceID {
		r.socketGroupRegistry().join(group, socket.ID())
	}
	return nil
}

func (r *Runtime) leaveSocketGroup(socket *gox.Socket, group string) error {
	if socket == nil || socket.ID() == "" {
		return nil
	}
	record := r.socketGroupRecord(socket)
	directory, cluster := r.groupDirectory()
	if directory != nil {
		ctx, cancel := r.directoryContext()
		err := directory.LeaveGroup(ctx, group, record.ID, record.InstanceID)
		cancel()
		if err != nil {
			r.dispatchGroupDirectoryError(cluster, "leave", err)
			return err
		}
	}
	if record.InstanceID == r.instanceID || directory == nil {
		r.socketGroupRegistry().leave(group, socket.ID())
	}
	return nil
}

func (r *Runtime) leaveAllSocketGroups(socket *gox.Socket) error {
	if socket == nil || socket.ID() == "" {
		return nil
	}
	record := r.socketGroupRecord(socket)
	directory, cluster := r.groupDirectory()
	var result error
	if directory != nil {
		ctx, cancel := r.directoryContext()
		result = directory.LeaveAllGroups(ctx, record.ID, record.InstanceID)
		cancel()
		if result != nil {
			r.dispatchGroupDirectoryError(cluster, "leave-all", result)
		}
	}
	if record.InstanceID == r.instanceID || directory == nil {
		r.socketGroupRegistry().leaveAll(socket.ID())
	}
	return result
}

func (r *Runtime) socketGroups(socket *gox.Socket) []string {
	if socket == nil || socket.ID() == "" {
		return []string{}
	}
	directory, cluster := r.groupDirectory()
	if directory == nil {
		return r.socketGroupRegistry().groups(socket.ID())
	}
	ctx, cancel := r.directoryContext()
	groups, err := directory.ListSocketGroups(ctx, socket.ID())
	cancel()
	if err != nil {
		r.dispatchGroupDirectoryError(cluster, "list-socket", err)
		return []string{}
	}
	sort.Strings(groups)
	return groups
}

func (r *Runtime) listGroups() []string {
	directory, cluster := r.groupDirectory()
	if directory == nil {
		return r.socketGroupRegistry().list()
	}
	ctx, cancel := r.directoryContext()
	groups, err := directory.ListGroups(ctx)
	cancel()
	if err != nil {
		r.dispatchGroupDirectoryError(cluster, "list", err)
		return []string{}
	}
	sort.Strings(groups)
	return groups
}

func (r *Runtime) groupMembers(group string) []*gox.Socket {
	directory, cluster := r.groupDirectory()
	if directory == nil {
		return r.localGroupMembers(group)
	}
	ctx, cancel := r.directoryContext()
	records, err := directory.ListGroupSockets(ctx, group)
	cancel()
	if err != nil {
		r.dispatchGroupDirectoryError(cluster, "members", err)
		return []*gox.Socket{}
	}
	return r.materializeClusterSockets(records)
}

// targetGroupSockets resolves only physical connections owned by this
// instance. In cluster mode the global directory is authoritative, which also
// covers memberships created through a remote socket proxy.
func (r *Runtime) targetGroupSockets(group string) []*gox.Socket {
	directory, cluster := r.groupDirectory()
	if directory == nil {
		return r.localGroupMembers(group)
	}
	ctx, cancel := r.directoryContext()
	records, err := directory.ListGroupSockets(ctx, group)
	cancel()
	if err != nil {
		r.dispatchGroupDirectoryError(cluster, "route-members", err)
		return []*gox.Socket{}
	}
	result := make([]*gox.Socket, 0, len(records))
	for _, record := range records {
		if record.InstanceID != r.instanceID {
			continue
		}
		if socket, found := r.webSocketRegistry().findByID(record.ID); found {
			result = append(result, socket)
		}
	}
	return result
}

func (r *Runtime) countGroups() int {
	directory, cluster := r.groupDirectory()
	if directory == nil {
		return r.socketGroupRegistry().count()
	}
	ctx, cancel := r.directoryContext()
	count, err := directory.CountGroups(ctx)
	cancel()
	if err != nil {
		r.dispatchGroupDirectoryError(cluster, "count", err)
		return 0
	}
	return count
}

func (r *Runtime) countGroupMembers(group string) int {
	directory, cluster := r.groupDirectory()
	if directory == nil {
		return r.socketGroupRegistry().countMembers(group)
	}
	ctx, cancel := r.directoryContext()
	count, err := directory.CountGroupSockets(ctx, group)
	cancel()
	if err != nil {
		r.dispatchGroupDirectoryError(cluster, "count-members", err)
		return 0
	}
	return count
}

func (r *Runtime) bindSocketGroups(socket *gox.Socket) {
	if r == nil || socket == nil {
		return
	}
	gox.BindSocketGroups(socket, gox.SocketGroupBindings{
		Join:     func(group string) error { return r.joinSocketGroup(socket, group) },
		Leave:    func(group string) error { return r.leaveSocketGroup(socket, group) },
		LeaveAll: func() error { return r.leaveAllSocketGroups(socket) },
		Groups:   func() []string { return r.socketGroups(socket) },
		EmitTo:   r.emitToGroup,
	})
}

func (r *Runtime) bindGroups() {
	if r == nil {
		return
	}
	r.socketGroupRegistry()
	r.unbindGroups = gox.BindGroups(gox.GroupRegistryBindings{
		List:         r.listGroups,
		Members:      r.groupMembers,
		Count:        r.countGroups,
		CountMembers: r.countGroupMembers,
		EmitTo:       r.emitToGroup,
	})
}

func (r *Runtime) synchronizeGroupDirectory() {
	directory, cluster := r.groupDirectory()
	if directory == nil {
		return
	}
	for _, group := range r.socketGroupRegistry().list() {
		for _, socket := range r.localGroupMembers(group) {
			ctx, cancel := r.directoryContext()
			err := directory.JoinGroup(ctx, group, r.socketGroupRecord(socket))
			cancel()
			if err != nil {
				r.dispatchGroupDirectoryError(cluster, "synchronize", err)
				return
			}
		}
	}
}
