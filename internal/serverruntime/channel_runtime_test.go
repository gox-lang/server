package serverruntime

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gox-lang/server/internal/project"
	websocketmeta "github.com/gox-lang/server/internal/websocket"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

type failingChannelDirectoryRuntime struct{ *clusterTestRuntime }

func (*failingChannelDirectoryRuntime) JoinChannel(context.Context, string, server.ClusterSocketRecord) error {
	return errors.New("channel directory unavailable")
}

type channelCloseCaptureRuntime struct {
	*clusterTestRuntime
	closed chan string
}

func (runtime *channelCloseCaptureRuntime) PublishChannelClose(_ context.Context, channelID string) error {
	runtime.closed <- channelID
	return nil
}

func TestChannelInstancesLoadDefinitionOnceAndRemainIndependent(t *testing.T) {
	var configured atomic.Int64
	runtime := &Runtime{
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {
				entry: project.ChannelEntry{Name: "Chat", File: "websocket/chat/chat.gox", Function: "Chat"},
				handler: func(channel *gox.Channel) {
					configured.Add(1)
					channel.When("validate", func(*gox.Socket, string) bool { return true })
				},
			},
		},
		channelInstances: make(map[string]*gox.Channel),
	}

	const callers = 24
	results := make(chan *gox.Channel, callers)
	errors := make(chan error, callers)
	var wait sync.WaitGroup
	for index := 0; index < callers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			channel, err := runtime.ensureChannelInstance("Chat#venezuela")
			if err != nil {
				errors <- err
				return
			}
			results <- channel
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	var first *gox.Channel
	for channel := range results {
		if first == nil {
			first = channel
		}
		if channel != first {
			t.Fatal("concurrent callers received different channel instances")
		}
	}
	if configured.Load() != 1 {
		t.Fatalf("factory executions = %d", configured.Load())
	}
	if first == nil || first.ID() != "Chat#venezuela" || first.Name() != "Chat" || first.Key() != "venezuela" {
		t.Fatalf("channel identity = %#v", first)
	}

	other, err := runtime.ensureChannelInstance("Chat#peru")
	if err != nil {
		t.Fatal(err)
	}
	if other == first || configured.Load() != 2 {
		t.Fatal("different keys did not produce independent configured instances")
	}
}

func TestChannelCloseRemovesOnlyCurrentEphemeralInstance(t *testing.T) {
	runtime := &Runtime{
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: func(*gox.Channel) {}},
		},
		channelInstances: make(map[string]*gox.Channel),
	}
	runtime.bindChannels()
	defer runtime.unbindChannels()
	channel, err := runtime.ensureChannelInstance("Chat#venezuela")
	if err != nil {
		t.Fatal(err)
	}
	if selected, found := gox.Channels.Get(channel.ID()); !found || selected != channel {
		t.Fatal("active channel is absent from global registry")
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	if gox.Channels.Has(channel.ID()) || gox.Channels.Count() != 0 {
		t.Fatal("closed channel remained globally active")
	}
	recreated, err := runtime.ensureChannelInstance("Chat#venezuela")
	if err != nil {
		t.Fatal(err)
	}
	if recreated == channel || recreated.IsClosed() {
		t.Fatal("closed channel ID was not recreated as a fresh instance")
	}
}

func TestChannelIDsAreStrict(t *testing.T) {
	valid := map[string][2]string{"Chat": {"Chat", ""}, "Chat#venezuela": {"Chat", "venezuela"}, "Chat/Moderation#room-1": {"Chat/Moderation", "room-1"}}
	for id, expected := range valid {
		name, key, err := splitChannelID(id)
		if err != nil || name != expected[0] || key != expected[1] {
			t.Fatalf("split %q = %q, %q, %v", id, name, key, err)
		}
	}
	for _, id := range []string{"", " Chat", "Chat ", "#room", "Chat#", "Chat#a#b"} {
		if _, _, err := splitChannelID(id); err == nil {
			t.Fatalf("invalid channel ID %q was accepted", id)
		}
	}
}

func TestChannelBuildPathsStayInsideDedicatedWebSocketDirectory(t *testing.T) {
	root := t.TempDir()
	route := channelRoutePrefix + "Chat/Moderation"
	buildDir, version, generated, plugin, err := resolveGoxBuildPathsWithVersion(root, route, "123")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".build", "websocket", "channels", "Chat", "Moderation")
	if buildDir != want || version != "123" || generated != filepath.Join(want, "123.go") || plugin != filepath.Join(want, "123.so") {
		t.Fatalf("channel build paths = %q, %q, %q, %q", buildDir, version, generated, plugin)
	}
}

func TestChannelMembershipRunsStrictLifecycleAndPrunesEmptyInstance(t *testing.T) {
	connected := make(chan string, 1)
	disconnected := make(chan gox.ChannelDisconnectReason, 1)
	runtime := &Runtime{
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {
				entry: project.ChannelEntry{Name: "Chat"},
				handler: func(channel *gox.Channel) {
					channel.When("validate", func(socket *gox.Socket, token string) bool { return socket.ID() != "" && token == "valid" })
					channel.When("connected", func(socket *gox.Socket) { connected <- socket.ID() })
					channel.When("disconnected", func(_ *gox.Socket, reason gox.ChannelDisconnectReason) { disconnected <- reason })
				},
			},
		},
		channelInstances: make(map[string]*gox.Channel),
		channelMembers:   make(map[string]map[string]*gox.Socket),
		socketChannels:   make(map[string]map[string]struct{}),
		channelPending:   make(map[string]int),
	}
	socket := newRuntimeTestSocket("socket-1")
	accepted, err := runtime.joinChannel(socket, "Chat#room", []any{"invalid"})
	if err != nil || accepted {
		t.Fatalf("rejected join = %t, %v", accepted, err)
	}
	if len(runtime.channelInstances) != 0 {
		t.Fatal("rejected empty instance was not pruned")
	}

	accepted, err = runtime.joinChannel(socket, "Chat#room", []any{"valid"})
	if err != nil || !accepted {
		t.Fatalf("accepted join = %t, %v", accepted, err)
	}
	if id := <-connected; id != socket.ID() {
		t.Fatalf("connected socket = %q", id)
	}
	channel := runtime.channelInstances["Chat#room"]
	if channel == nil || channel.Count() != 1 || !channel.Has(socket) {
		t.Fatal("accepted member is absent")
	}
	if err := channel.Leave(socket); err != nil {
		t.Fatal(err)
	}
	if reason := <-disconnected; reason != gox.ChannelDisconnectRemoved {
		t.Fatalf("disconnect reason = %q", reason)
	}
	if len(runtime.channelInstances) != 0 || !channel.IsClosed() {
		t.Fatal("last member departure did not close the ephemeral instance")
	}
}

func TestChannelMembershipCommitsToDistributedDirectoryBeforeConnected(t *testing.T) {
	cluster := &clusterTestRuntime{directory: map[string]server.ClusterSocketRecord{}}
	runtime := &Runtime{
		instanceID: "test-instance", clusterRuntime: cluster,
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: func(channel *gox.Channel) {
				channel.When("connected", func(socket *gox.Socket) {
					cluster.directoryMu.RLock()
					_, committed := cluster.channels["Chat#room"][socket.ID()]
					cluster.directoryMu.RUnlock()
					if !committed {
						panic("connected ran before distributed membership commit")
					}
				})
			}},
		},
		channelInstances: make(map[string]*gox.Channel), channelMembers: make(map[string]map[string]*gox.Socket),
		socketChannels: make(map[string]map[string]struct{}), channelPending: make(map[string]int),
	}
	socket := newRuntimeTestSocket("socket-distributed")
	cluster.directory[socket.ID()] = server.ClusterSocketRecord{ID: socket.ID(), InstanceID: runtime.instanceID}
	accepted, err := runtime.joinChannel(socket, "Chat#room", nil)
	if err != nil || !accepted {
		t.Fatalf("distributed join=%t err=%v", accepted, err)
	}
	left, err := runtime.leaveChannel(socket, "Chat#room", gox.ChannelDisconnectRemoved)
	if err != nil || !left {
		t.Fatalf("distributed leave=%t err=%v", left, err)
	}
	if len(cluster.channels) != 0 || len(runtime.channelMembers) != 0 {
		t.Fatal("distributed leave retained membership")
	}
}

func TestChannelDirectoryJoinFailureRollsBackLocalMembership(t *testing.T) {
	cluster := &failingChannelDirectoryRuntime{clusterTestRuntime: &clusterTestRuntime{}}
	runtime := &Runtime{
		instanceID: "test-instance", clusterRuntime: cluster,
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: func(*gox.Channel) {}},
		},
		channelInstances: make(map[string]*gox.Channel), channelMembers: make(map[string]map[string]*gox.Socket),
		socketChannels: make(map[string]map[string]struct{}), channelPending: make(map[string]int),
	}
	accepted, err := runtime.joinChannel(newRuntimeTestSocket("socket-failure"), "Chat#room", nil)
	if err == nil || accepted {
		t.Fatalf("failed distributed join=%t err=%v", accepted, err)
	}
	if len(runtime.channelMembers) != 0 || len(runtime.socketChannels) != 0 || len(runtime.channelInstances) != 0 {
		t.Fatal("failed distributed join degraded into a local membership")
	}
}

func TestGlobalChannelsMaterializeRemoteMembersAndCountDirectory(t *testing.T) {
	cluster := &clusterTestRuntime{
		directory: map[string]server.ClusterSocketRecord{
			"remote-socket": {ID: "remote-socket", SessionID: "remote-session", InstanceID: "remote-instance"},
		},
		channels: map[string]map[string]struct{}{"Chat#remote": {"remote-socket": {}}},
	}
	runtime := &Runtime{
		instanceID: "local-instance", clusterRuntime: cluster,
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: func(*gox.Channel) {}},
		},
		channelInstances: make(map[string]*gox.Channel), channelMembers: make(map[string]map[string]*gox.Socket),
		socketChannels: make(map[string]map[string]struct{}), channelPending: make(map[string]int),
	}
	runtime.bindChannels()
	defer runtime.unbindChannels()
	channel, found := gox.Channels.Get("Chat#remote")
	if !found || channel == nil {
		t.Fatal("remote Channel was not materialized")
	}
	if gox.Channels.Count() != 1 || len(gox.Channels.List()) != 1 {
		t.Fatal("global Channel registry ignored the directory")
	}
	members := channel.Members().All()
	if len(members) != 1 || members[0].ID() != "remote-socket" || members[0].IsLocal() {
		t.Fatalf("global members=%#v", members)
	}
	if channel.Count() != 1 || !channel.Has(members[0]) {
		t.Fatal("Channel member helpers are not global")
	}
}

func TestChannelClosePublishesAndRemovesOnlyChannelMembership(t *testing.T) {
	cluster := &channelCloseCaptureRuntime{clusterTestRuntime: &clusterTestRuntime{}, closed: make(chan string, 1)}
	runtime := &Runtime{
		instanceID: "local-instance", clusterRuntime: cluster,
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: func(*gox.Channel) {}},
		},
		channelInstances: make(map[string]*gox.Channel), channelMembers: make(map[string]map[string]*gox.Socket),
		socketChannels: make(map[string]map[string]struct{}), channelPending: make(map[string]int),
	}
	socket := newRuntimeTestSocket("socket-close")
	gox.BindSocketChannelTransport(socket, gox.SocketChannelTransport{Leave: func(string, gox.ChannelDisconnectReason) error { return nil }})
	cluster.directory = map[string]server.ClusterSocketRecord{socket.ID(): {ID: socket.ID(), InstanceID: runtime.instanceID}}
	accepted, err := runtime.joinChannel(socket, "Chat#close", nil)
	if err != nil || !accepted {
		t.Fatalf("join=%t err=%v", accepted, err)
	}
	channel := runtime.channelInstances["Chat#close"]
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	if id := <-cluster.closed; id != channel.ID() {
		t.Fatalf("published close=%q", id)
	}
	if len(cluster.channels) != 0 || len(runtime.channelMembers) != 0 {
		t.Fatal("Channel close retained membership")
	}
	if socket.ID() == "" {
		t.Fatal("Channel close disconnected the physical socket")
	}
}

func TestPhysicalSocketDisconnectLeavesEveryChannelIndependently(t *testing.T) {
	reasons := make(chan gox.ChannelDisconnectReason, 2)
	runtime := &Runtime{
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: func(channel *gox.Channel) {
				channel.When("disconnected", func(_ *gox.Socket, reason gox.ChannelDisconnectReason) { reasons <- reason })
			}},
		},
		channelInstances: make(map[string]*gox.Channel),
		channelMembers:   make(map[string]map[string]*gox.Socket),
		socketChannels:   make(map[string]map[string]struct{}),
		channelPending:   make(map[string]int),
	}
	socket := newRuntimeTestSocket("socket-1")
	for _, id := range []string{"Chat#one", "Chat#two"} {
		accepted, err := runtime.joinChannel(socket, id, nil)
		if err != nil || !accepted {
			t.Fatalf("join %s = %t, %v", id, accepted, err)
		}
	}
	runtime.leaveAllChannels(socket, gox.ChannelDisconnectSocketDisconnected)
	for index := 0; index < 2; index++ {
		if reason := <-reasons; reason != gox.ChannelDisconnectSocketDisconnected {
			t.Fatalf("reason = %q", reason)
		}
	}
	if len(runtime.channelInstances) != 0 || len(runtime.socketChannels) != 0 {
		t.Fatal("physical disconnect leaked channel membership")
	}
}

func TestChannelLifecycleFailureDispatchesIsolatedErrorAndRollsBackJoin(t *testing.T) {
	errors := make(chan gox.ChannelError, 1)
	runtime := &Runtime{
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: func(channel *gox.Channel) {
				channel.When("connected", func(*gox.Socket) { panic("broken connected handler") })
				channel.When("error", func(err gox.ChannelError) { errors <- err })
			}},
		},
		channelInstances: make(map[string]*gox.Channel),
		channelMembers:   make(map[string]map[string]*gox.Socket),
		socketChannels:   make(map[string]map[string]struct{}),
		channelPending:   make(map[string]int),
	}
	accepted, err := runtime.joinChannel(newRuntimeTestSocket("socket-1"), "Chat", nil)
	if err == nil || accepted {
		t.Fatalf("failed lifecycle join = %t, %v", accepted, err)
	}
	received := <-errors
	if received.Code != gox.ChannelHandlerFailed || received.ChannelID != "Chat" || received.Event != "connected" {
		t.Fatalf("channel error = %#v", received)
	}
	if len(runtime.channelInstances) != 0 || len(runtime.channelMembers) != 0 {
		t.Fatal("failed connected handler leaked membership")
	}
}

func TestConcurrentValidationCannotPruneInstanceAcceptedByAnotherSocket(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	runtime := &Runtime{
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: func(channel *gox.Channel) {
				channel.When("validate", func(_ *gox.Socket, token string) bool {
					started <- struct{}{}
					<-release
					return token == "accept"
				})
			}},
		},
		channelInstances: make(map[string]*gox.Channel),
		channelMembers:   make(map[string]map[string]*gox.Socket),
		socketChannels:   make(map[string]map[string]struct{}),
		channelPending:   make(map[string]int),
	}
	type result struct {
		accepted bool
		err      error
	}
	results := make(chan result, 2)
	go func() {
		accepted, err := runtime.joinChannel(newRuntimeTestSocket("reject"), "Chat#room", []any{"reject"})
		results <- result{accepted, err}
	}()
	go func() {
		accepted, err := runtime.joinChannel(newRuntimeTestSocket("accept"), "Chat#room", []any{"accept"})
		results <- result{accepted, err}
	}()
	<-started
	<-started
	close(release)
	acceptedCount := 0
	for index := 0; index < 2; index++ {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.accepted {
			acceptedCount++
		}
	}
	channel := runtime.channelInstances["Chat#room"]
	if acceptedCount != 1 || channel == nil || channel.Count() != 1 {
		t.Fatalf("accepted=%d channel=%#v", acceptedCount, channel)
	}
}

func TestChannelEventRoutesOnlyThroughChannelNamespace(t *testing.T) {
	received := make(chan string, 1)
	runtime := &Runtime{
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: func(channel *gox.Channel) {
				channel.On("message", func(socket *gox.Socket, message string) { received <- socket.ID() + ":" + message })
			}},
		},
		channelInstances: make(map[string]*gox.Channel),
		channelMembers:   make(map[string]map[string]*gox.Socket),
		socketChannels:   make(map[string]map[string]struct{}),
		channelPending:   make(map[string]int),
	}
	socket := newRuntimeTestSocket("socket-1")
	accepted, err := runtime.joinChannel(socket, "Chat#room", nil)
	if err != nil || !accepted {
		t.Fatalf("join = %t, %v", accepted, err)
	}
	connection := &webSocketRuntimeConnection{runtime: runtime, socket: socket}
	connection.dispatchChannelEvent(websocketmeta.Packet{Type: websocketmeta.PacketChannelEvent, Headers: []websocketmeta.Header{
		{Type: websocketmeta.HeaderEventName, Value: []byte("message")},
		{Type: websocketmeta.HeaderEventID, Value: websocketmeta.EncodeCompactUint(1)},
		{Type: websocketmeta.HeaderChannelID, Value: []byte("Chat#room")},
	}, Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentText, Data: []byte("hello")}}})
	select {
	case value := <-received:
		if value != "socket-1:hello" {
			t.Fatalf("event = %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("Channel event was not dispatched")
	}
}

func TestUnusedChannelJoinTransfersAreReleasedAtomically(t *testing.T) {
	file, err := websocketmeta.EncodeFileReference(7)
	if err != nil {
		t.Fatal(err)
	}
	files, err := websocketmeta.EncodeFilesReference([]uint64{7, 8})
	if err != nil {
		t.Fatal(err)
	}
	connection := &webSocketRuntimeConnection{incoming: map[uint64]*webSocketIncomingTransfer{
		7: {total: 1, data: []byte{1}, complete: true},
		8: {total: 1, data: []byte{2}, complete: true},
	}}
	packet := websocketmeta.Packet{Arguments: []websocketmeta.Argument{
		{Type: websocketmeta.ArgumentFile, Data: file},
		{Type: websocketmeta.ArgumentFiles, Data: files},
	}}
	if err := connection.releasePacketTransfers(packet); err != nil {
		t.Fatal(err)
	}
	if len(connection.incoming) != 0 {
		t.Fatalf("released transfers remain: %#v", connection.incoming)
	}

	connection.incoming = map[uint64]*webSocketIncomingTransfer{
		7: {total: 1, data: []byte{1}, complete: true},
		8: {total: 2, data: []byte{2}, complete: false},
	}
	if err := connection.releasePacketTransfers(packet); err == nil {
		t.Fatal("incomplete transfer was accepted")
	}
	if len(connection.incoming) != 2 {
		t.Fatalf("failed release mutated transfers: %#v", connection.incoming)
	}
}

func TestPreparedChannelReloadCommitsAllActiveInstancesWithoutMembershipChanges(t *testing.T) {
	oldHandler := func(channel *gox.Channel) {
		channel.On("version", func(*gox.Socket) gox.EventReply { return "old" })
	}
	runtime := &Runtime{
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: oldHandler, version: "v1"},
		},
		channelInstances: make(map[string]*gox.Channel),
		channelMembers:   make(map[string]map[string]*gox.Socket),
		socketChannels:   make(map[string]map[string]struct{}),
		channelPending:   make(map[string]int),
	}
	first, err := runtime.ensureChannelInstance("Chat#first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.ensureChannelInstance("Chat#second")
	if err != nil {
		t.Fatal(err)
	}
	firstSocket := newRuntimeTestSocket("socket-first")
	secondSocket := newRuntimeTestSocket("socket-second")
	if !runtime.addChannelMember(first, firstSocket) || !runtime.addChannelMember(second, secondSocket) {
		t.Fatal("test members were not added")
	}

	newHandler := func(channel *gox.Channel) {
		channel.On("version", func(*gox.Socket) gox.EventReply { return "new:" + channel.Key() })
	}
	prepared, err := runtime.prepareChannelReload("Chat", "v2", newHandler)
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := first.DispatchOn("version", firstSocket)
	if err != nil || before != "old" {
		t.Fatalf("prepare changed active generation: value=%v err=%v", before, err)
	}
	if first.Count() != 1 || second.Count() != 1 {
		t.Fatal("prepare changed membership")
	}
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}

	firstValue, _, err := first.DispatchOn("version", firstSocket)
	if err != nil || firstValue != "new:first" {
		t.Fatalf("first generation = %v err=%v", firstValue, err)
	}
	secondValue, _, err := second.DispatchOn("version", secondSocket)
	if err != nil || secondValue != "new:second" {
		t.Fatalf("second generation = %v err=%v", secondValue, err)
	}
	if first.GenerationVersion() != "v2" || second.GenerationVersion() != "v2" {
		t.Fatal("committed generation version is inconsistent")
	}
	if first.Count() != 1 || second.Count() != 1 || !first.Has(firstSocket) || !second.Has(secondSocket) {
		t.Fatal("commit changed membership")
	}

	third, err := runtime.ensureChannelInstance("Chat#third")
	if err != nil {
		t.Fatal(err)
	}
	thirdValue, _, err := third.DispatchOn("version", newRuntimeTestSocket("socket-third"))
	if err != nil || thirdValue != "new:third" || third.GenerationVersion() != "v2" {
		t.Fatalf("future instance did not use committed definition: value=%v version=%q err=%v", thirdValue, third.GenerationVersion(), err)
	}
}

func TestPreparedChannelReloadFailureAndAbortPreservePreviousGeneration(t *testing.T) {
	oldHandler := func(channel *gox.Channel) { channel.On("version", func() gox.EventReply { return "old" }) }
	runtime := &Runtime{
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: oldHandler, version: "v1"},
		},
		channelInstances: make(map[string]*gox.Channel),
		channelMembers:   make(map[string]map[string]*gox.Socket),
		socketChannels:   make(map[string]map[string]struct{}),
		channelPending:   make(map[string]int),
	}
	channel, err := runtime.ensureChannelInstance("Chat#room")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.prepareChannelReload("Chat", "broken", func(*gox.Channel) { panic("broken factory") }); err == nil {
		t.Fatal("panicking reload factory was accepted")
	}
	value, _, err := channel.DispatchOn("version")
	if err != nil || value != "old" || channel.GenerationVersion() != "v1" {
		t.Fatalf("failed prepare changed generation: value=%v version=%q err=%v", value, channel.GenerationVersion(), err)
	}

	prepared, err := runtime.prepareChannelReload("Chat", "aborted", func(candidate *gox.Channel) {
		candidate.On("version", func() gox.EventReply { return "wrong" })
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared.Abort()
	value, _, err = channel.DispatchOn("version")
	if err != nil || value != "old" || channel.GenerationVersion() != "v1" {
		t.Fatalf("abort changed generation: value=%v version=%q err=%v", value, channel.GenerationVersion(), err)
	}

	// Abort must release instance creation for the next normal operation.
	if _, err := runtime.ensureChannelInstance("Chat#after-abort"); err != nil {
		t.Fatalf("reload exclusion remained locked after abort: %v", err)
	}
}

func TestPreparedChannelReloadIncludesInstancesCreatedBeforeCommit(t *testing.T) {
	oldHandler := func(channel *gox.Channel) { channel.On("version", func() gox.EventReply { return "old" }) }
	runtime := &Runtime{
		goxBuildGroup: newStaticSingleflightGroup(),
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Chat": {entry: project.ChannelEntry{Name: "Chat"}, handler: oldHandler, version: "v1"},
		},
		channelInstances: make(map[string]*gox.Channel),
		channelMembers:   make(map[string]map[string]*gox.Socket),
		socketChannels:   make(map[string]map[string]struct{}),
		channelPending:   make(map[string]int),
	}
	prepared, err := runtime.prepareChannelReload("Chat", "v2", func(channel *gox.Channel) {
		channel.On("version", func() gox.EventReply { return "new" })
	})
	if err != nil {
		t.Fatal(err)
	}

	channel, err := runtime.ensureChannelInstance("Chat#concurrent")
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := channel.DispatchOn("version")
	if err != nil || before != "old" || channel.GenerationVersion() != "v1" {
		t.Fatalf("instance created during prepare did not use committed definition: value=%v version=%q err=%v", before, channel.GenerationVersion(), err)
	}
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	value, _, err := channel.DispatchOn("version")
	if err != nil || value != "new" || channel.GenerationVersion() != "v2" {
		t.Fatalf("instance created during prepare was omitted from commit: value=%v version=%q err=%v", value, channel.GenerationVersion(), err)
	}
}

func TestChannelEmitPublishesDedicatedClusterTargetAndDeliversLocally(t *testing.T) {
	cluster := &groupPacketCaptureRuntime{clusterTestRuntime: &clusterTestRuntime{}, targets: make(chan server.ClusterTarget, 1)}
	runtime := &Runtime{
		clusterRuntime: cluster,
		channelMembers: map[string]map[string]*gox.Socket{},
	}
	socket := newRuntimeTestSocket("socket-1")
	received := make(chan string, 1)
	gox.BindSocketChannelTransport(socket, gox.SocketChannelTransport{Emit: func(channelID, event string, values []any) error {
		received <- channelID + ":" + event
		return nil
	}})
	runtime.channelMembers["Chat#room"] = map[string]*gox.Socket{socket.ID(): socket}
	if err := runtime.emitChannelEvent("Chat#room", "message", []any{"hello"}); err != nil {
		t.Fatal(err)
	}
	target := <-cluster.targets
	if target.Kind != server.ClusterTargetChannel || target.ChannelID != "Chat#room" {
		t.Fatalf("target = %#v", target)
	}
	if event := <-received; event != "Chat#room:message" {
		t.Fatalf("local event = %q", event)
	}
	if !clusterOutboundPacket(websocketmeta.PacketChannelEvent) {
		t.Fatal("Channel event cannot cross the cluster transport")
	}
}
