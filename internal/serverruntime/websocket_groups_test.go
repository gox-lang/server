package serverruntime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

var errTestGroupDirectory = errors.New("group directory failed")

type groupPacketCaptureRuntime struct {
	*clusterTestRuntime
	targets chan server.ClusterTarget
}

func (runtime *groupPacketCaptureRuntime) PublishSocketPacket(_ context.Context, target server.ClusterTarget, payload []byte) error {
	runtime.targets <- target
	return nil
}

type failingGroupDirectoryRuntime struct{ *clusterTestRuntime }

func (*failingGroupDirectoryRuntime) ListGroups(context.Context) ([]string, error) {
	return nil, errTestGroupDirectory
}
func (*failingGroupDirectoryRuntime) ListGroupSockets(context.Context, string) ([]server.ClusterSocketRecord, error) {
	return nil, errTestGroupDirectory
}
func (*failingGroupDirectoryRuntime) CountGroups(context.Context) (int, error) {
	return 0, errTestGroupDirectory
}
func (*failingGroupDirectoryRuntime) CountGroupSockets(context.Context, string) (int, error) {
	return 0, errTestGroupDirectory
}

func registerLocalGroupSocket(t *testing.T, runtime *Runtime, sessionID string) *gox.Socket {
	t.Helper()
	socket := &gox.Socket{}
	if err := runtime.webSocketRegistry().register(socket, server.NewSessionReference(sessionID)); err != nil {
		t.Fatal(err)
	}
	runtime.bindSocketGroups(socket)
	return socket
}

func TestLocalGroupsAreEphemeralIdempotentAndSorted(t *testing.T) {
	runtime := &Runtime{}
	runtime.bindGroups()
	defer runtime.unbindGroups()
	socket := registerLocalGroupSocket(t, runtime, "session")
	if err := socket.Join("beta"); err != nil {
		t.Fatal(err)
	}
	if err := socket.Join("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := socket.Join("alpha"); err != nil {
		t.Fatal(err)
	}
	if names := gox.Groups.List(); len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("groups = %#v", names)
	}
	if names := socket.Groups(); len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("socket groups = %#v", names)
	}
	if !gox.Groups.Has("alpha") || gox.Groups.CountMembers("alpha") != 1 {
		t.Fatal("alpha membership is missing")
	}
	if err := socket.Leave("alpha"); err != nil {
		t.Fatal(err)
	}
	if gox.Groups.Has("alpha") {
		t.Fatal("empty group did not disappear")
	}
	if err := socket.Leave("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := socket.LeaveAll(); err != nil {
		t.Fatal(err)
	}
	if gox.Groups.Count() != 0 {
		t.Fatalf("groups after LeaveAll = %#v", gox.Groups.List())
	}
}

func TestLocalEmitToDoesNotRequireSenderMembershipAndMissingGroupIsNoOp(t *testing.T) {
	runtime := &Runtime{}
	runtime.bindGroups()
	defer runtime.unbindGroups()
	sender := registerLocalGroupSocket(t, runtime, "sender")
	receiver := registerLocalGroupSocket(t, runtime, "receiver")
	received := make(chan string, 1)
	gox.BindSocketEmit(receiver, func(event string, values []any) error { received <- event; return nil })
	if err := receiver.Join("room"); err != nil {
		t.Fatal(err)
	}
	sender.EmitTo("room", "message", "hello")
	if event := <-received; event != "message" {
		t.Fatalf("received event = %q", event)
	}
	sender.EmitTo("missing", "ignored", true)
	gox.Groups.EmitTo("missing", "ignored", true)
}

func TestLocalGroupMembershipCleanupRemovesEveryEmptyGroup(t *testing.T) {
	runtime := &Runtime{}
	runtime.bindGroups()
	defer runtime.unbindGroups()
	socket := registerLocalGroupSocket(t, runtime, "session")
	_ = socket.Join("one")
	_ = socket.Join("two")
	runtime.socketGroupRegistry().leaveAll(socket.ID())
	if gox.Groups.Count() != 0 || len(socket.Groups()) != 0 {
		t.Fatalf("groups survived cleanup: %#v", gox.Groups.List())
	}
}

func TestLocalGroupRegistryIsRaceSafe(t *testing.T) {
	runtime := &Runtime{}
	runtime.bindGroups()
	defer runtime.unbindGroups()
	const total = 64
	var group sync.WaitGroup
	for index := 0; index < total; index++ {
		socket := registerLocalGroupSocket(t, runtime, "session")
		group.Add(1)
		go func(socket *gox.Socket) {
			defer group.Done()
			_ = socket.Join("room")
			_ = socket.Groups()
			_ = gox.Groups.List()
			_ = gox.Groups.Members("room").All()
			_ = socket.LeaveAll()
		}(socket)
	}
	group.Wait()
	if gox.Groups.Count() != 0 {
		t.Fatalf("groups after concurrent cleanup = %#v", gox.Groups.List())
	}
}

func TestDistributedGroupDirectoryOwnsMembershipAndGlobalQueries(t *testing.T) {
	cluster := &clusterTestRuntime{}
	runtime := &Runtime{instanceID: "local", clusterRuntime: cluster}
	runtime.bindGroups()
	defer runtime.unbindGroups()
	socket := registerLocalGroupSocket(t, runtime, "session")
	gox.BindSocketLocation(socket, runtime.instanceID, true)
	if err := runtime.registerSocketDirectory(socket); err != nil {
		t.Fatal(err)
	}
	if err := socket.Join("room"); err != nil {
		t.Fatal(err)
	}
	if gox.Groups.Count() != 1 || gox.Groups.CountMembers("room") != 1 {
		t.Fatalf("distributed counts groups=%d members=%d", gox.Groups.Count(), gox.Groups.CountMembers("room"))
	}
	if members := gox.Groups.Members("room").All(); len(members) != 1 || members[0] != socket {
		t.Fatalf("distributed members = %#v", members)
	}
	if groups := socket.Groups(); len(groups) != 1 || groups[0] != "room" {
		t.Fatalf("distributed socket groups = %#v", groups)
	}
}

func TestDistributedGroupEmitPublishesGroupTargetAndDeliversLocally(t *testing.T) {
	cluster := &groupPacketCaptureRuntime{clusterTestRuntime: &clusterTestRuntime{}, targets: make(chan server.ClusterTarget, 1)}
	runtime := &Runtime{instanceID: "local", clusterRuntime: cluster}
	runtime.bindGroups()
	defer runtime.unbindGroups()
	socket := registerLocalGroupSocket(t, runtime, "session")
	gox.BindSocketLocation(socket, runtime.instanceID, true)
	if err := runtime.registerSocketDirectory(socket); err != nil {
		t.Fatal(err)
	}
	if err := socket.Join("room"); err != nil {
		t.Fatal(err)
	}
	received := make(chan string, 1)
	gox.BindSocketEmit(socket, func(event string, values []any) error { received <- event; return nil })
	gox.Groups.EmitTo("room", "message", "hello")
	if target := <-cluster.targets; target.Kind != server.ClusterTargetGroup || target.GroupName != "room" {
		t.Fatalf("target = %#v", target)
	}
	if event := <-received; event != "message" {
		t.Fatalf("local event = %q", event)
	}
}

func TestRemoteSocketProxyCanJoinGroupOwnedByAnotherInstance(t *testing.T) {
	cluster := &clusterTestRuntime{}
	owner := &Runtime{instanceID: "owner", clusterRuntime: cluster}
	remote := &Runtime{instanceID: "remote", clusterRuntime: cluster}
	owner.bindGroups()
	defer owner.unbindGroups()
	localSocket := registerLocalGroupSocket(t, owner, "session")
	gox.BindSocketLocation(localSocket, owner.instanceID, true)
	if err := owner.registerSocketDirectory(localSocket); err != nil {
		t.Fatal(err)
	}
	proxy, found := remote.findClusterSocketByID(localSocket.ID())
	if !found || proxy == nil || proxy.IsLocal() {
		t.Fatalf("remote proxy = %#v found=%v", proxy, found)
	}
	if err := proxy.Join("room"); err != nil {
		t.Fatal(err)
	}
	resolved := owner.targetGroupSockets("room")
	if len(resolved) != 1 || resolved[0] != localSocket {
		t.Fatalf("owner resolved = %#v", resolved)
	}
}

func TestDistributedGroupQueriesFailClosedOnDirectoryError(t *testing.T) {
	cluster := &failingGroupDirectoryRuntime{clusterTestRuntime: &clusterTestRuntime{}}
	runtime := &Runtime{instanceID: "local", clusterRuntime: cluster}
	runtime.bindGroups()
	defer runtime.unbindGroups()
	runtime.socketGroupRegistry().join("local-only", "socket")
	if gox.Groups.Count() != 0 || len(gox.Groups.List()) != 0 || len(gox.Groups.Members("local-only").All()) != 0 {
		t.Fatal("distributed query leaked partial local membership")
	}
}
