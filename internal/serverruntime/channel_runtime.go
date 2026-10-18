package serverruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/gox-lang/server/internal/project"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

const channelRoutePrefix = "/__gox/channel/"

type channelDefinitionRuntime struct {
	entry   project.ChannelEntry
	mu      sync.Mutex
	route   *GoxRouteRuntime
	handler gox.ChannelHandler
	version string
}

type preparedChannelGeneration struct {
	channel   *gox.Channel
	candidate *gox.Channel
}

// preparedChannelReload is a local transaction. While it is pending, existing
// dispatch and new Channel instances continue using the committed definition.
// Commit briefly excludes instance creation and includes any instance that
// appeared after Prepare.
type preparedChannelReload struct {
	runtime    *Runtime
	definition *channelDefinitionRuntime
	name       string
	version    string
	handler    gox.ChannelHandler
	candidates []preparedChannelGeneration
	finished   bool
}

func isChannelRoutePath(path string) bool { return strings.HasPrefix(path, channelRoutePrefix) }

func (r *Runtime) indexChannelDefinitions() error {
	if r == nil || r.project == nil {
		return nil
	}
	entries, err := r.project.Channels()
	if err != nil {
		return err
	}
	r.channelMu.Lock()
	defer r.channelMu.Unlock()
	if r.channelDefinitions == nil {
		r.channelDefinitions = make(map[string]*channelDefinitionRuntime)
	}
	for _, entry := range entries {
		if r.channelDefinitions[entry.Name] == nil {
			r.channelDefinitions[entry.Name] = &channelDefinitionRuntime{entry: entry}
		}
	}
	return nil
}

// findChannelDefinition discovers newly added files in development without
// replacing the loaded definitions or the generations serving existing rooms.
func (r *Runtime) findChannelDefinition(name string) (*channelDefinitionRuntime, error) {
	r.channelMu.RLock()
	definition := r.channelDefinitions[name]
	r.channelMu.RUnlock()
	if definition != nil {
		return definition, nil
	}
	if r.project != nil && devEnabled(r.state().Config.Dev) {
		if err := r.indexChannelDefinitions(); err != nil {
			return nil, err
		}
		r.channelMu.RLock()
		definition = r.channelDefinitions[name]
		r.channelMu.RUnlock()
	}
	if definition == nil {
		return nil, fmt.Errorf("channel definition %q was not found", name)
	}
	return definition, nil
}

func splitChannelID(id string) (name, key string, err error) {
	trimmed := strings.TrimSpace(id)
	if trimmed != id {
		return "", "", fmt.Errorf("channel ID cannot start or end with whitespace")
	}
	if id == "" {
		return "", "", fmt.Errorf("channel ID cannot be empty")
	}
	if len(id) > 255 {
		return "", "", fmt.Errorf("channel ID exceeds 255 bytes")
	}
	if !utf8.ValidString(id) {
		return "", "", fmt.Errorf("channel ID must contain valid UTF-8")
	}
	for _, value := range id {
		if unicode.IsControl(value) {
			return "", "", fmt.Errorf("channel ID cannot contain control characters")
		}
	}
	if strings.Count(id, "#") > 1 {
		return "", "", fmt.Errorf("channel ID contains more than one instance separator")
	}
	name, key, hasKey := strings.Cut(id, "#")
	if name == "" {
		return "", "", fmt.Errorf("channel name cannot be empty")
	}
	if hasKey && key == "" {
		return "", "", fmt.Errorf("channel instance key cannot be empty")
	}
	return name, key, nil
}

func (r *Runtime) ensureChannelInstance(id string) (*gox.Channel, error) {
	name, key, err := splitChannelID(id)
	if err != nil {
		return nil, err
	}
	r.channelMu.RLock()
	current := r.channelInstances[id]
	r.channelMu.RUnlock()
	if current != nil && !current.IsClosed() {
		return current, nil
	}
	definition, err := r.findChannelDefinition(name)
	if err != nil {
		return nil, err
	}

	value, err := r.getGoxBuildGroup().Do("channel-instance:"+id, func() (any, error) {
		r.channelMu.RLock()
		current := r.channelInstances[id]
		r.channelMu.RUnlock()
		if current != nil && !current.IsClosed() {
			return current, nil
		}
		handler, version, err := r.ensureChannelDefinition(definition)
		if err != nil {
			return nil, err
		}
		// Building a cold definition may itself commit a Channel generation and
		// therefore needs the reload write lock. Only exclude reload commits while
		// selecting the committed handler and publishing the new instance.
		r.channelReloadMu.RLock()
		defer r.channelReloadMu.RUnlock()
		definition.mu.Lock()
		if definition.handler != nil {
			handler = definition.handler
			version = definition.version
		}
		definition.mu.Unlock()
		channel := gox.NewChannel(id, name, key)
		gox.BindChannelRuntime(channel, gox.ChannelRuntimeBindings{
			Members: func() []*gox.Socket { return r.channelMembersGlobal(id) },
			Emit:    func(event string, values []any) error { return r.emitChannelEvent(id, event, values) },
			Leave: func(socket *gox.Socket) error {
				_, err := r.leaveChannel(socket, id, gox.ChannelDisconnectRemoved)
				return err
			},
			Close: func() error { return r.closeChannelInstance(channel) },
		})
		candidate := gox.NewChannelGenerationCandidate(channel, version)
		if err := executeChannelHandler(candidate, handler); err != nil {
			gox.AbortChannelGeneration(candidate)
			return nil, err
		}
		if err := gox.CommitChannelGeneration(channel, candidate); err != nil {
			gox.AbortChannelGeneration(candidate)
			return nil, fmt.Errorf("configure channel %q: %w", id, err)
		}
		if channel.IsClosed() {
			return nil, fmt.Errorf("channel %q closed while its definition was being configured", id)
		}
		r.channelMu.Lock()
		r.channelInstances[id] = channel
		r.channelMu.Unlock()
		return channel, nil
	})
	if err != nil {
		return nil, err
	}
	channel, ok := value.(*gox.Channel)
	if !ok || channel == nil {
		return nil, fmt.Errorf("channel %q did not produce an instance", id)
	}
	return channel, nil
}

// prepareChannelReload configures a replacement generation for every active
// local instance of one reusable Channel definition. No live generation is
// changed until Commit succeeds.
func (r *Runtime) prepareChannelReload(name, version string, handler gox.ChannelHandler) (*preparedChannelReload, error) {
	if r == nil {
		return nil, fmt.Errorf("channel reload runtime is unavailable")
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("channel reload name cannot be empty")
	}
	if strings.TrimSpace(version) == "" {
		return nil, fmt.Errorf("channel reload version cannot be empty")
	}
	if handler == nil {
		return nil, fmt.Errorf("channel reload handler is unavailable")
	}

	r.channelMu.RLock()
	definition := r.channelDefinitions[name]
	channels := make([]*gox.Channel, 0)
	for _, channel := range r.channelInstances {
		if channel != nil && channel.Name() == name && !channel.IsClosed() {
			channels = append(channels, channel)
		}
	}
	r.channelMu.RUnlock()
	if definition == nil {
		return nil, fmt.Errorf("channel definition %q was not found", name)
	}

	prepared := &preparedChannelReload{runtime: r, definition: definition, name: name, version: version, handler: handler}
	prepared.candidates = make([]preparedChannelGeneration, 0, len(channels))
	for _, channel := range channels {
		candidate := gox.NewChannelGenerationCandidate(channel, version)
		if candidate == nil {
			prepared.abortLocked()
			return nil, fmt.Errorf("prepare channel %q generation candidate failed", channel.ID())
		}
		if err := executeChannelHandler(candidate, handler); err != nil {
			gox.AbortChannelGeneration(candidate)
			prepared.abortLocked()
			return nil, fmt.Errorf("prepare channel %q reload: %w", channel.ID(), err)
		}
		if err := gox.ValidateChannelGenerationCandidate(channel, candidate); err != nil {
			gox.AbortChannelGeneration(candidate)
			prepared.abortLocked()
			return nil, fmt.Errorf("prepare channel %q reload: %w", channel.ID(), err)
		}
		prepared.candidates = append(prepared.candidates, preparedChannelGeneration{channel: channel, candidate: candidate})
	}
	return prepared, nil
}

// Commit installs the prepared generations and makes the new handler the
// definition used by future ephemeral instances. It emits no Channel lifecycle
// packets and does not touch membership indexes.
func (prepared *preparedChannelReload) Commit() error {
	if prepared == nil || prepared.runtime == nil {
		return fmt.Errorf("prepared channel reload is unavailable")
	}
	if prepared.finished {
		return fmt.Errorf("prepared channel reload is already finished")
	}
	prepared.runtime.channelReloadMu.Lock()
	defer prepared.runtime.channelReloadMu.Unlock()

	// Include instances created after Prepare. They used the previous committed
	// definition and are configured now before any generation is swapped.
	known := make(map[*gox.Channel]struct{}, len(prepared.candidates))
	for _, item := range prepared.candidates {
		known[item.channel] = struct{}{}
	}
	prepared.runtime.channelMu.RLock()
	current := make(map[*gox.Channel]struct{})
	for _, channel := range prepared.runtime.channelInstances {
		if channel != nil && channel.Name() == prepared.name && !channel.IsClosed() {
			current[channel] = struct{}{}
		}
	}
	prepared.runtime.channelMu.RUnlock()
	for channel := range current {
		if _, exists := known[channel]; exists {
			continue
		}
		candidate := gox.NewChannelGenerationCandidate(channel, prepared.version)
		if candidate == nil {
			prepared.abortLocked()
			return fmt.Errorf("commit channel %q generation candidate failed", channel.ID())
		}
		if err := executeChannelHandler(candidate, prepared.handler); err != nil {
			gox.AbortChannelGeneration(candidate)
			prepared.abortLocked()
			return fmt.Errorf("commit channel %q reload preparation: %w", channel.ID(), err)
		}
		prepared.candidates = append(prepared.candidates, preparedChannelGeneration{channel: channel, candidate: candidate})
	}

	for _, item := range prepared.candidates {
		if _, active := current[item.channel]; !active {
			continue
		}
		if err := gox.ValidateChannelGenerationCandidate(item.channel, item.candidate); err != nil {
			prepared.abortLocked()
			return fmt.Errorf("commit channel %q reload: %w", item.channel.ID(), err)
		}
	}
	for _, item := range prepared.candidates {
		if _, active := current[item.channel]; !active {
			gox.AbortChannelGeneration(item.candidate)
			continue
		}
		if err := gox.CommitChannelGeneration(item.channel, item.candidate); err != nil {
			// Every candidate was validated while instance creation remained
			// excluded, so reaching this branch indicates an internal contract
			// violation rather than a recoverable partial commit.
			prepared.abortLocked()
			return fmt.Errorf("commit channel %q reload: %w", item.channel.ID(), err)
		}
	}
	prepared.definition.mu.Lock()
	prepared.definition.handler = prepared.handler
	prepared.definition.version = prepared.version
	prepared.definition.mu.Unlock()
	prepared.finished = true
	return nil
}

func (prepared *preparedChannelReload) Abort() {
	if prepared == nil || prepared.runtime == nil || prepared.finished {
		return
	}
	prepared.abortLocked()
}

func (prepared *preparedChannelReload) abortLocked() {
	if prepared == nil || prepared.finished {
		return
	}
	for _, item := range prepared.candidates {
		gox.AbortChannelGeneration(item.candidate)
	}
	prepared.finished = true
}

func (r *Runtime) joinChannel(socket *gox.Socket, id string, values []any) (bool, error) {
	if r == nil || socket == nil || socket.ID() == "" {
		return false, fmt.Errorf("channel join requires a live socket")
	}
	channel, err := r.ensureChannelInstance(id)
	if err != nil {
		return false, err
	}
	if r.hasChannelMember(id, socket.ID()) {
		return true, nil
	}
	if !r.reserveChannelValidation(channel) {
		return false, fmt.Errorf("channel %q is no longer active", id)
	}
	defer func() { r.releaseChannelValidation(channel); r.pruneChannelIfUnused(channel) }()
	args := make([]any, 0, len(values)+1)
	args = append(args, socket)
	args = append(args, values...)
	accepted := true
	if result, handled, dispatchErr := channel.DispatchWhen("validate", args...); dispatchErr != nil {
		r.dispatchChannelLifecycleError(channel, "validate", dispatchErr)
		return false, dispatchErr
	} else if handled {
		accepted = result.(bool)
	}
	if !accepted {
		return false, nil
	}
	if !r.addChannelMember(channel, socket) {
		if r.hasChannelMember(id, socket.ID()) {
			return true, nil
		}
		return false, fmt.Errorf("channel %q closed before the socket could join", id)
	}
	if err := r.joinChannelDirectory(socket, id); err != nil {
		r.removeChannelMember(channel.ID(), socket)
		return false, err
	}
	if _, _, dispatchErr := channel.DispatchWhen("connected", socket); dispatchErr != nil {
		if err := r.leaveChannelDirectory(socket, id); err != nil {
			dispatchErr = errors.Join(dispatchErr, err)
		}
		r.removeChannelMember(channel.ID(), socket)
		r.dispatchChannelLifecycleError(channel, "connected", dispatchErr)
		return false, dispatchErr
	}
	r.recordChannelDynamicWarmup(channel.Name())
	return true, nil
}

func (r *Runtime) recordChannelDynamicWarmup(name string) {
	if r == nil || r.dynamicWarmup == nil || strings.TrimSpace(name) == "" {
		return
	}
	r.channelMu.RLock()
	definition := r.channelDefinitions[name]
	r.channelMu.RUnlock()
	if definition == nil {
		return
	}
	definition.mu.Lock()
	route := definition.route
	definition.mu.Unlock()
	r.recordDynamicWarmup(route)
}

func (r *Runtime) reserveChannelValidation(channel *gox.Channel) bool {
	if r == nil || channel == nil {
		return false
	}
	id := channel.ID()
	r.channelMu.Lock()
	defer r.channelMu.Unlock()
	if r.channelInstances[id] != channel || channel.IsClosed() {
		return false
	}
	if r.channelPending == nil {
		r.channelPending = make(map[string]int)
	}
	r.channelPending[id]++
	return true
}

func (r *Runtime) releaseChannelValidation(channel *gox.Channel) {
	if r == nil || channel == nil {
		return
	}
	id := channel.ID()
	r.channelMu.Lock()
	if pending := r.channelPending[id]; pending > 1 {
		r.channelPending[id] = pending - 1
	} else {
		delete(r.channelPending, id)
	}
	r.channelMu.Unlock()
}

func (r *Runtime) hasChannelMember(channelID, socketID string) bool {
	r.channelMu.RLock()
	_, exists := r.channelMembers[channelID][socketID]
	r.channelMu.RUnlock()
	return exists
}

func (r *Runtime) addChannelMember(channel *gox.Channel, socket *gox.Socket) bool {
	if channel == nil || socket == nil || socket.ID() == "" {
		return false
	}
	id := channel.ID()
	r.channelMu.Lock()
	defer r.channelMu.Unlock()
	if r.channelInstances[id] != channel || channel.IsClosed() {
		return false
	}
	if r.channelMembers == nil {
		r.channelMembers = make(map[string]map[string]*gox.Socket)
	}
	if r.socketChannels == nil {
		r.socketChannels = make(map[string]map[string]struct{})
	}
	members := r.channelMembers[id]
	if members == nil {
		members = make(map[string]*gox.Socket)
		r.channelMembers[id] = members
	}
	if members[socket.ID()] != nil {
		return false
	}
	members[socket.ID()] = socket
	channels := r.socketChannels[socket.ID()]
	if channels == nil {
		channels = make(map[string]struct{})
		r.socketChannels[socket.ID()] = channels
	}
	channels[id] = struct{}{}
	return true
}

func (r *Runtime) removeChannelMember(channelID string, socket *gox.Socket) bool {
	if socket == nil || socket.ID() == "" {
		return false
	}
	r.channelMu.Lock()
	defer r.channelMu.Unlock()
	members := r.channelMembers[channelID]
	if members == nil || members[socket.ID()] != socket {
		return false
	}
	delete(members, socket.ID())
	if len(members) == 0 {
		delete(r.channelMembers, channelID)
	}
	if channels := r.socketChannels[socket.ID()]; channels != nil {
		delete(channels, channelID)
		if len(channels) == 0 {
			delete(r.socketChannels, socket.ID())
		}
	}
	return true
}

func (r *Runtime) leaveChannel(socket *gox.Socket, channelID string, reason gox.ChannelDisconnectReason) (bool, error) {
	r.channelMu.RLock()
	channel := r.channelInstances[channelID]
	r.channelMu.RUnlock()
	if channel == nil || !r.hasChannelMember(channelID, socket.ID()) {
		return false, nil
	}
	if err := r.leaveChannelDirectory(socket, channelID); err != nil {
		return false, err
	}
	if !r.removeChannelMember(channelID, socket) {
		return false, nil
	}
	if reason != gox.ChannelDisconnectSocketDisconnected {
		if err := gox.LeaveSocketChannel(socket, channelID, reason); err != nil {
			r.dispatchChannelLifecycleError(channel, "leave", err)
		}
	}
	if _, _, err := channel.DispatchWhen("disconnected", socket, reason); err != nil {
		r.dispatchChannelLifecycleError(channel, "disconnected", err)
	}
	r.pruneChannelIfUnused(channel)
	return true, nil
}

func (r *Runtime) leaveAllChannels(socket *gox.Socket, reason gox.ChannelDisconnectReason) {
	if r == nil || socket == nil {
		return
	}
	r.channelMu.RLock()
	set := r.socketChannels[socket.ID()]
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	r.channelMu.RUnlock()
	if len(ids) == 0 {
		return
	}
	if err := r.leaveAllChannelDirectory(socket); err != nil {
		// A dead physical socket must still be removed locally. The socket
		// directory unregister operation performs a second atomic Redis cleanup.
		if reason != gox.ChannelDisconnectSocketDisconnected {
			return
		}
	}
	for _, id := range ids {
		r.leaveLocalChannel(socket, id, reason)
	}
}

func (r *Runtime) leaveLocalChannel(socket *gox.Socket, channelID string, reason gox.ChannelDisconnectReason) bool {
	r.channelMu.RLock()
	channel := r.channelInstances[channelID]
	r.channelMu.RUnlock()
	if channel == nil || !r.removeChannelMember(channelID, socket) {
		return false
	}
	if reason != gox.ChannelDisconnectSocketDisconnected {
		if err := gox.LeaveSocketChannel(socket, channelID, reason); err != nil {
			r.dispatchChannelLifecycleError(channel, "leave", err)
		}
	}
	if _, _, err := channel.DispatchWhen("disconnected", socket, reason); err != nil {
		r.dispatchChannelLifecycleError(channel, "disconnected", err)
	}
	r.pruneChannelIfUnused(channel)
	return true
}

func (r *Runtime) channelDirectory() (server.ClusterChannelDirectory, server.ClusterRuntime) {
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

func (r *Runtime) dispatchChannelDirectoryError(cluster server.ClusterRuntime, operation string, err error) {
	if err != nil {
		r.dispatchClusterError(cluster, "channel-directory-"+operation, "Distributed Channel directory operation failed", err)
	}
}

func (r *Runtime) channelSocketRecord(socket *gox.Socket) server.ClusterSocketRecord {
	return r.socketGroupRecord(socket)
}

func (r *Runtime) joinChannelDirectory(socket *gox.Socket, channelID string) error {
	directory, cluster := r.channelDirectory()
	if directory == nil {
		return nil
	}
	ctx, cancel := r.directoryContext()
	err := directory.JoinChannel(ctx, channelID, r.channelSocketRecord(socket))
	cancel()
	if err != nil {
		r.dispatchChannelDirectoryError(cluster, "join", err)
	}
	return err
}

func (r *Runtime) leaveChannelDirectory(socket *gox.Socket, channelID string) error {
	directory, cluster := r.channelDirectory()
	if directory == nil {
		return nil
	}
	record := r.channelSocketRecord(socket)
	ctx, cancel := r.directoryContext()
	err := directory.LeaveChannel(ctx, channelID, record.ID, record.InstanceID)
	cancel()
	if err != nil {
		r.dispatchChannelDirectoryError(cluster, "leave", err)
	}
	return err
}

func (r *Runtime) leaveAllChannelDirectory(socket *gox.Socket) error {
	directory, cluster := r.channelDirectory()
	if directory == nil {
		return nil
	}
	record := r.channelSocketRecord(socket)
	ctx, cancel := r.directoryContext()
	err := directory.LeaveAllChannels(ctx, record.ID, record.InstanceID)
	cancel()
	if err != nil {
		r.dispatchChannelDirectoryError(cluster, "leave-all", err)
	}
	return err
}

func (r *Runtime) localChannelMembers(id string) []*gox.Socket {
	r.channelMu.RLock()
	members := r.channelMembers[id]
	result := make([]*gox.Socket, 0, len(members))
	for _, socket := range members {
		if socket != nil {
			result = append(result, socket)
		}
	}
	r.channelMu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID() < result[j].ID() })
	return result
}

// targetChannelSockets resolves only the physical connections owned by this
// instance. In cluster mode Redis is authoritative; no local fallback is used
// when the distributed lookup fails.
func (r *Runtime) targetChannelSockets(id string) []*gox.Socket {
	directory, cluster := r.channelDirectory()
	if directory == nil {
		return r.localChannelMembers(id)
	}
	ctx, cancel := r.directoryContext()
	records, err := directory.ListChannelSockets(ctx, id)
	cancel()
	if err != nil {
		r.dispatchChannelDirectoryError(cluster, "route-members", err)
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

func (r *Runtime) channelMembersGlobal(id string) []*gox.Socket {
	directory, cluster := r.channelDirectory()
	if directory == nil {
		return r.localChannelMembers(id)
	}
	ctx, cancel := r.directoryContext()
	records, err := directory.ListChannelSockets(ctx, id)
	cancel()
	if err != nil {
		r.dispatchChannelDirectoryError(cluster, "members", err)
		return []*gox.Socket{}
	}
	return r.materializeClusterSockets(records)
}

func (r *Runtime) getGlobalChannel(id string) (*gox.Channel, bool) {
	if _, _, err := splitChannelID(id); err != nil {
		return nil, false
	}
	directory, cluster := r.channelDirectory()
	if directory == nil {
		r.channelMu.RLock()
		channel := r.channelInstances[id]
		r.channelMu.RUnlock()
		return channel, channel != nil && !channel.IsClosed()
	}
	ctx, cancel := r.directoryContext()
	members, err := directory.ListChannelSockets(ctx, id)
	cancel()
	if err != nil {
		r.dispatchChannelDirectoryError(cluster, "get", err)
		return nil, false
	}
	if len(members) == 0 {
		return nil, false
	}
	channel, err := r.ensureChannelInstance(id)
	if err != nil {
		if cluster != nil {
			r.dispatchChannelDirectoryError(cluster, "materialize", err)
		}
		return nil, false
	}
	return channel, !channel.IsClosed()
}

func (r *Runtime) listGlobalChannels() []*gox.Channel {
	directory, cluster := r.channelDirectory()
	if directory == nil {
		r.channelMu.RLock()
		channels := make([]*gox.Channel, 0, len(r.channelInstances))
		for _, channel := range r.channelInstances {
			if channel != nil && !channel.IsClosed() {
				channels = append(channels, channel)
			}
		}
		r.channelMu.RUnlock()
		sort.Slice(channels, func(i, j int) bool { return channels[i].ID() < channels[j].ID() })
		return channels
	}
	ctx, cancel := r.directoryContext()
	ids, err := directory.ListChannels(ctx)
	cancel()
	if err != nil {
		r.dispatchChannelDirectoryError(cluster, "list", err)
		return []*gox.Channel{}
	}
	channels := make([]*gox.Channel, 0, len(ids))
	for _, id := range ids {
		channel, err := r.ensureChannelInstance(id)
		if err != nil {
			r.dispatchChannelDirectoryError(cluster, "materialize", err)
			continue
		}
		channels = append(channels, channel)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].ID() < channels[j].ID() })
	return channels
}

func (r *Runtime) countGlobalChannels() int {
	directory, cluster := r.channelDirectory()
	if directory == nil {
		return len(r.listGlobalChannels())
	}
	ctx, cancel := r.directoryContext()
	count, err := directory.CountChannels(ctx)
	cancel()
	if err != nil {
		r.dispatchChannelDirectoryError(cluster, "count", err)
		return 0
	}
	return count
}

func (r *Runtime) synchronizeChannelDirectory() {
	directory, cluster := r.channelDirectory()
	if directory == nil {
		return
	}
	r.channelMu.RLock()
	type membership struct {
		channelID string
		socket    *gox.Socket
	}
	memberships := make([]membership, 0)
	for channelID, members := range r.channelMembers {
		for _, socket := range members {
			if socket != nil {
				memberships = append(memberships, membership{channelID: channelID, socket: socket})
			}
		}
	}
	r.channelMu.RUnlock()
	for _, current := range memberships {
		ctx, cancel := r.directoryContext()
		err := directory.JoinChannel(ctx, current.channelID, r.channelSocketRecord(current.socket))
		cancel()
		if err != nil {
			r.dispatchChannelDirectoryError(cluster, "synchronize", err)
			return
		}
	}
}

func (r *Runtime) pruneChannelIfUnused(channel *gox.Channel) {
	if r == nil || channel == nil {
		return
	}
	id := channel.ID()
	r.channelMu.Lock()
	if r.channelInstances[id] != channel || len(r.channelMembers[id]) != 0 || r.channelPending[id] != 0 {
		r.channelMu.Unlock()
		return
	}
	delete(r.channelInstances, id)
	delete(r.channelMembers, id)
	delete(r.channelPending, id)
	r.channelMu.Unlock()
	_ = channel.Close()
}

func (r *Runtime) closeChannelInstance(channel *gox.Channel) error {
	return r.closeChannelInstanceWithReason(channel, gox.ChannelDisconnectClosed)
}

func (r *Runtime) closeChannelInstanceWithReason(channel *gox.Channel, reason gox.ChannelDisconnectReason) error {
	if r == nil || channel == nil {
		return nil
	}
	id := channel.ID()
	r.channelMu.RLock()
	current := r.channelInstances[id]
	r.channelMu.RUnlock()
	if current != channel {
		return nil
	}
	directory, cluster := r.channelDirectory()
	if directory != nil {
		ctx, cancel := clusterOperationContext()
		err := cluster.PublishChannelClose(ctx, id)
		cancel()
		if err != nil {
			r.dispatchClusterError(cluster, "publish-channel-close", "Distributed Channel close could not be published", err)
			return err
		}
		ctx, cancel = r.directoryContext()
		err = directory.CloseChannel(ctx, id)
		cancel()
		if err != nil {
			r.dispatchChannelDirectoryError(cluster, "close", err)
			r.closeLocalChannelInstance(channel, reason)
			return err
		}
		r.closeLocalChannelInstance(channel, reason)
		return nil
	}
	return r.closeLocalChannelInstanceDistributed(channel, reason)
}

func (r *Runtime) closeLocalChannelInstanceDistributed(channel *gox.Channel, reason gox.ChannelDisconnectReason) error {
	id := channel.ID()
	r.channelMu.RLock()
	if r.channelInstances[id] != channel {
		r.channelMu.RUnlock()
		return nil
	}
	members := make([]*gox.Socket, 0, len(r.channelMembers[id]))
	for _, socket := range r.channelMembers[id] {
		members = append(members, socket)
	}
	r.channelMu.RUnlock()
	var result error
	for _, socket := range members {
		if _, err := r.leaveChannel(socket, id, reason); err != nil {
			result = errors.Join(result, err)
		}
	}
	if result != nil {
		return result
	}
	r.channelMu.Lock()
	if r.channelInstances[id] == channel {
		delete(r.channelMembers, id)
		delete(r.channelPending, id)
		delete(r.channelInstances, id)
	}
	r.channelMu.Unlock()
	return nil
}

func (r *Runtime) closeLocalChannelInstance(channel *gox.Channel, reason gox.ChannelDisconnectReason) {
	if r == nil || channel == nil {
		return
	}
	id := channel.ID()
	for _, socket := range r.localChannelMembers(id) {
		r.leaveLocalChannel(socket, id, reason)
	}
	r.channelMu.Lock()
	if r.channelInstances[id] == channel {
		delete(r.channelMembers, id)
		delete(r.channelPending, id)
		delete(r.channelInstances, id)
	}
	r.channelMu.Unlock()
}

func (r *Runtime) dispatchChannelLifecycleError(channel *gox.Channel, event string, cause error) {
	if channel == nil || cause == nil {
		return
	}
	channelErr, ok := cause.(gox.ChannelError)
	if !ok {
		channelErr = gox.ChannelError{Code: gox.ChannelHandlerFailed, ChannelID: channel.ID(), Event: event, Message: "Channel lifecycle handler failed", Cause: cause}
	}
	_, _, _ = channel.DispatchWhen("error", channelErr)
}

func (r *Runtime) ensureChannelDefinition(definition *channelDefinitionRuntime) (gox.ChannelHandler, string, error) {
	definition.mu.Lock()
	if definition.handler != nil {
		handler, version := definition.handler, definition.version
		definition.mu.Unlock()
		return handler, version, nil
	}
	if definition.route == nil {
		route, err := r.newChannelDefinitionRoute(definition.entry)
		if err != nil {
			definition.mu.Unlock()
			return nil, "", err
		}
		definition.route = route
	}
	route := definition.route
	definition.mu.Unlock()

	if err := r.ensureGoxRouteReady(route); err != nil {
		return nil, "", err
	}

	definition.mu.Lock()
	defer definition.mu.Unlock()
	// The transactional commit normally installs these fields. Retain this
	// fallback for local/custom loaders that only populate the route export.
	if definition.handler == nil {
		route.mu.Lock()
		handler, version := route.ChannelHandler, route.BuildVersion
		route.mu.Unlock()
		if handler == nil {
			return nil, "", fmt.Errorf("channel definition %q did not export GoxChannelHandler", definition.entry.Name)
		}
		definition.handler = handler
		definition.version = version
	}
	return definition.handler, definition.version, nil
}

func (r *Runtime) newChannelDefinitionRoute(entry project.ChannelEntry) (*GoxRouteRuntime, error) {
	absPath := filepath.Join(r.projectRoot(), filepath.FromSlash(entry.File))
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("resolve channel %s: %w", entry.Name, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("resolve channel %s: definition path is a directory", entry.Name)
	}
	goBinary, err := r.resolveRepositoryGoBinary()
	if err != nil {
		return nil, err
	}
	buildDir := filepath.Join(r.projectRoot(), ".build", "websocket", "channels", filepath.FromSlash(entry.Name))
	version := fmt.Sprintf("%d", info.ModTime().UnixNano())
	return &GoxRouteRuntime{
		RoutePath:     channelRoutePrefix + entry.Name,
		AbsPath:       absPath,
		RelPath:       entry.File,
		ModTime:       info.ModTime(),
		Size:          info.Size(),
		BuildDir:      buildDir,
		BuildVersion:  version,
		GeneratedPath: filepath.Join(buildDir, version+".go"),
		PluginPath:    filepath.Join(buildDir, version+".so"),
		GoBinary:      goBinary,
		State:         GoxRouteCold,
	}, nil
}

func executeChannelHandler(channel *gox.Channel, handler gox.ChannelHandler) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("configure channel %q: %v", channel.ID(), recovered)
		}
	}()
	handler(channel)
	return nil
}

func (r *Runtime) bindChannels() {
	if r == nil {
		return
	}
	r.unbindChannels = gox.BindChannelRegistry(gox.ChannelRegistryBindings{
		Get:   r.getGlobalChannel,
		List:  r.listGlobalChannels,
		Count: r.countGlobalChannels,
	})
}

func (r *Runtime) closeChannelInstances() {
	if r == nil {
		return
	}
	r.channelMu.RLock()
	channels := make([]*gox.Channel, 0, len(r.channelInstances))
	for _, channel := range r.channelInstances {
		if channel != nil {
			channels = append(channels, channel)
		}
	}
	r.channelMu.RUnlock()
	for _, channel := range channels {
		// ClusterRuntime has already performed RemoveInstance and closed by this
		// stage of shutdown, so only the in-process lifecycle remains.
		id := channel.ID()
		for _, socket := range r.localChannelMembers(id) {
			r.leaveLocalChannel(socket, id, gox.ChannelDisconnectServerStopped)
		}
		_ = channel.Close()
	}
}
