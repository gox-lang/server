package serverruntime

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	websocketmeta "github.com/gox-lang/server/internal/websocket"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

type clusterOriginTestRuntime struct {
	*clusterTestRuntime
	owner   *Runtime
	targets chan string
	cancels chan []byte
	reply   bool
}

type capturedClusterDisconnect struct {
	target     server.ClusterTarget
	instanceID string
	code       uint16
}

type clusterDisconnectCaptureRuntime struct {
	*clusterTestRuntime
	disconnects chan capturedClusterDisconnect
}

type clusterDisconnectSubscriptionRuntime struct {
	*clusterTestRuntime
	events chan server.ClusterEvent
}

type clusterPublishFailureRuntime struct{ *clusterTestRuntime }

func (*clusterPublishFailureRuntime) PublishSocketPacket(context.Context, server.ClusterTarget, []byte) error {
	return errors.New("publish failed")
}
func (*clusterPublishFailureRuntime) PublishSocketDisconnect(context.Context, server.ClusterTarget, string, uint16) error {
	return errors.New("publish failed")
}

func (runtime *clusterDisconnectSubscriptionRuntime) SubscribeClusterEvents(ctx context.Context, event server.ClusterEventFunc, _ server.ClusterErrorFunc) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case value := <-runtime.events:
			event(value)
		}
	}
}

func (runtime *clusterDisconnectCaptureRuntime) PublishSocketDisconnect(_ context.Context, target server.ClusterTarget, instanceID string, code uint16) error {
	runtime.disconnects <- capturedClusterDisconnect{target: target, instanceID: instanceID, code: code}
	return nil
}

func (runtime *clusterOriginTestRuntime) PublishSocketRequest(_ context.Context, socketID string, correlationID []byte, payload []byte) error {
	runtime.targets <- socketID
	if !runtime.reply {
		return nil
	}
	request, err := websocketmeta.DecodePacket(payload)
	if err != nil {
		return err
	}
	requestID, _ := websocketmeta.HeaderUint(request, websocketmeta.HeaderRequestID)
	response, err := websocketmeta.EncodePacket(websocketResponsePacket(requestID, "remote-value"))
	if err != nil {
		return err
	}
	runtime.owner.clusterRequestCoordinator().deliver(correlationID, response)
	return nil
}

func (runtime *clusterOriginTestRuntime) PublishSocketRequestCancel(_ context.Context, correlationID []byte) error {
	runtime.cancels <- append([]byte(nil), correlationID...)
	return nil
}

var _ server.ClusterRuntime = (*clusterOriginTestRuntime)(nil)

func (runtime *clusterOriginTestRuntime) GetName() string { return "cluster-origin-test" }

func TestRemoteSocketProxyPublishesDirectedDisconnect(t *testing.T) {
	cluster := &clusterDisconnectCaptureRuntime{clusterTestRuntime: &clusterTestRuntime{}, disconnects: make(chan capturedClusterDisconnect, 1)}
	runtime := &Runtime{instanceID: "local", clusterRuntime: cluster}
	proxy := runtime.newClusterSocketProxyRecord(server.ClusterSocketRecord{ID: "socket-1", SessionID: "session-1", InstanceID: "remote"})
	proxy.Disconnect(gox.WSClosePermanent)
	select {
	case command := <-cluster.disconnects:
		if command.target.Kind != server.ClusterTargetSocket || command.target.SocketID != "socket-1" || command.instanceID != "remote" || command.code != 4000 {
			t.Fatalf("disconnect command = %#v", command)
		}
	case <-time.After(time.Second):
		t.Fatal("remote disconnect was not published")
	}
}

func TestDistributedDisconnectClosesOnlyTheTargetedLocalSocket(t *testing.T) {
	cluster := &clusterDisconnectSubscriptionRuntime{clusterTestRuntime: &clusterTestRuntime{}, events: make(chan server.ClusterEvent, 1)}
	runtime := &Runtime{instanceID: "test-instance", clusterRuntime: cluster}
	target := &gox.Socket{}
	other := &gox.Socket{}
	if err := runtime.webSocketRegistry().register(target, server.NewSessionReference("session")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.webSocketRegistry().register(other, server.NewSessionReference("other")); err != nil {
		t.Fatal(err)
	}
	closed := make(chan string, 2)
	gox.BindSocketDisconnect(target, func(code gox.WSCloseCode, _ string) { closed <- target.ID() })
	gox.BindSocketDisconnect(other, func(code gox.WSCloseCode, _ string) { closed <- other.ID() })
	cancel := runtime.startClusterEventSubscription(cluster)
	defer cancel()
	cluster.events <- server.ClusterEvent{
		Kind: server.ClusterEventSocketDisconnect, InstanceID: "remote-instance", TargetInstanceID: cluster.InstanceID(),
		Target: server.ClusterTarget{Kind: server.ClusterTargetSocket, SocketID: target.ID()}, CloseCode: uint16(gox.WSClosePermanent),
	}
	select {
	case id := <-closed:
		if id != target.ID() {
			t.Fatalf("disconnected socket = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("targeted local socket was not disconnected")
	}
	select {
	case id := <-closed:
		t.Fatalf("unrelated socket was disconnected: %q", id)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestDistributedOperationsDoNotDegradeIntoLocalOnlyActionsWhenPublishFails(t *testing.T) {
	cluster := &clusterPublishFailureRuntime{clusterTestRuntime: &clusterTestRuntime{}}
	runtime := &Runtime{instanceID: "local", clusterRuntime: cluster}
	socket := &gox.Socket{}
	if err := runtime.webSocketRegistry().register(socket, server.NewSessionReference("session")); err != nil {
		t.Fatal(err)
	}
	emitted := make(chan struct{}, 1)
	closed := make(chan struct{}, 1)
	gox.BindSocketEmit(socket, func(string, []any) error { emitted <- struct{}{}; return nil })
	gox.BindSocketDisconnect(socket, func(gox.WSCloseCode, string) { closed <- struct{}{} })
	runtime.emitClusterSocketEvent(server.ClusterTarget{Kind: server.ClusterTargetAll}, "event", []any{true})
	runtime.disconnectClusterSockets(server.ClusterTarget{Kind: server.ClusterTargetAll}, "", gox.WSClosePermanent)
	select {
	case <-emitted:
		t.Fatal("failed distributed emit was applied only locally")
	default:
	}
	select {
	case <-closed:
		t.Fatal("failed distributed disconnect was applied only locally")
	default:
	}
}

func TestClusterRequestCoordinatorDeliversOnceAndRemovesPendingRequest(t *testing.T) {
	coordinator := newClusterRequestCoordinator()
	correlation, pending, err := coordinator.register()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte{1, 2, 3}
	if !coordinator.deliver(correlation, payload) {
		t.Fatal("registered correlation was not delivered")
	}
	payload[0] = 9
	result := coordinator.wait(pending, time.Second)
	if result.err != nil || !bytes.Equal(result.payload, []byte{1, 2, 3}) {
		t.Fatalf("result = %#v", result)
	}
	if coordinator.deliver(correlation, []byte{4}) {
		t.Fatal("correlation accepted a second response")
	}
	if coordinator.count() != 0 {
		t.Fatal("completed request remained registered")
	}
}

func TestClusterRequestCoordinatorTimeoutWinsExactlyOnce(t *testing.T) {
	coordinator := newClusterRequestCoordinator()
	correlation, pending, err := coordinator.register()
	if err != nil {
		t.Fatal(err)
	}
	result := coordinator.wait(pending, time.Nanosecond)
	if !errors.Is(result.err, errClusterRequestTimeout) {
		t.Fatalf("timeout error = %v", result.err)
	}
	if coordinator.deliver(correlation, []byte{1}) {
		t.Fatal("late response completed a timed-out request")
	}
	if coordinator.count() != 0 {
		t.Fatal("timed-out request remained registered")
	}
}

func TestClusterRequestCoordinatorCancelAllDrainsPendingWithoutClosingFutureRequests(t *testing.T) {
	coordinator := newClusterRequestCoordinator()
	_, first, err := coordinator.register()
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := coordinator.register()
	if err != nil {
		t.Fatal(err)
	}
	coordinator.cancelAll(errClusterRequestReloaded)
	for _, pending := range []*clusterPendingRequest{first, second} {
		result := coordinator.wait(pending, time.Second)
		if !errors.Is(result.err, errClusterRequestReloaded) {
			t.Fatalf("reload result = %#v", result)
		}
	}
	if coordinator.count() != 0 {
		t.Fatalf("pending after reload = %d", coordinator.count())
	}
	_, next, err := coordinator.register()
	if err != nil || next == nil {
		t.Fatalf("coordinator did not accept request after reload: %v", err)
	}
	coordinator.cancel(next, errClusterRequestClosed)
}

func TestClusterRequestCoordinatorResponseTimeoutRaceDoesNotLeak(t *testing.T) {
	coordinator := newClusterRequestCoordinator()
	for index := 0; index < 100; index++ {
		correlation, pending, err := coordinator.register()
		if err != nil {
			t.Fatal(err)
		}
		var wait sync.WaitGroup
		wait.Add(1)
		go func() {
			defer wait.Done()
			coordinator.deliver(correlation, []byte{1})
		}()
		result := coordinator.wait(pending, time.Nanosecond)
		if result.err != nil && !errors.Is(result.err, errClusterRequestTimeout) {
			t.Fatalf("race error = %v", result.err)
		}
		wait.Wait()
	}
	if coordinator.count() != 0 {
		t.Fatalf("pending requests after races = %d", coordinator.count())
	}
}

func TestClusterRequestCoordinatorCloseReleasesEveryWaiter(t *testing.T) {
	coordinator := newClusterRequestCoordinator()
	pending := make([]*clusterPendingRequest, 8)
	for index := range pending {
		_, request, err := coordinator.register()
		if err != nil {
			t.Fatal(err)
		}
		pending[index] = request
	}
	coordinator.close()
	for _, request := range pending {
		result := coordinator.wait(request, time.Second)
		if !errors.Is(result.err, errClusterRequestClosed) {
			t.Fatalf("close error = %v", result.err)
		}
	}
	if _, _, err := coordinator.register(); !errors.Is(err, errClusterRequestClosed) {
		t.Fatalf("register after close error = %v", err)
	}
}

func TestClusterRequestCoordinatorRejectsMalformedCorrelation(t *testing.T) {
	coordinator := newClusterRequestCoordinator()
	if coordinator.deliver([]byte("short"), []byte{1}) {
		t.Fatal("malformed correlation was delivered")
	}
}

func TestClusterSocketProxyPublishesRequestAndBindsTypedReply(t *testing.T) {
	runtime := &Runtime{}
	cluster := &clusterOriginTestRuntime{
		clusterTestRuntime: &clusterTestRuntime{},
		owner:              runtime,
		targets:            make(chan string, 1),
		cancels:            make(chan []byte, 1),
		reply:              true,
	}
	runtime.clusterRuntime = cluster
	proxy := runtime.newClusterSocketProxy("remote-socket")
	var value string
	if err := proxy.EmitAwait(&value, "get-value", 42); err != nil {
		t.Fatal(err)
	}
	if value != "remote-value" {
		t.Fatalf("reply = %q", value)
	}
	if target := <-cluster.targets; target != "remote-socket" {
		t.Fatalf("request target = %q", target)
	}
	if runtime.clusterRequestCoordinator().count() != 0 {
		t.Fatal("completed proxy request remained pending")
	}
}

func TestClusterSocketProxyTimeoutPublishesCorrelationCancellation(t *testing.T) {
	runtime := &Runtime{}
	cluster := &clusterOriginTestRuntime{
		clusterTestRuntime: &clusterTestRuntime{},
		owner:              runtime,
		targets:            make(chan string, 1),
		cancels:            make(chan []byte, 1),
	}
	runtime.clusterRuntime = cluster
	proxy := runtime.newClusterSocketProxy("missing-socket")
	var value string
	if err := runtime.requestClusterSocketWithTimeout(proxy, "get-value", nil, &value, time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	<-cluster.targets
	select {
	case correlation := <-cluster.cancels:
		if len(correlation) != clusterCorrelationSize {
			t.Fatalf("cancel correlation size = %d", len(correlation))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout did not publish cancellation")
	}
	if runtime.clusterRequestCoordinator().count() != 0 {
		t.Fatal("timed-out proxy request remained pending")
	}
}

func TestCanceledRoutedRequestIsRemovedAndLateBrowserReplyIsIgnored(t *testing.T) {
	correlation := bytes.Repeat([]byte{5}, clusterCorrelationSize)
	key, ok := clusterCorrelationKey(correlation)
	if !ok {
		t.Fatal("test correlation is invalid")
	}
	route := &webSocketClusterRequest{correlationID: correlation, correlationKey: key}
	connection := &webSocketRuntimeConnection{
		clusterRequests:   map[uint64]*webSocketClusterRequest{17: route},
		clusterRequestIDs: map[[clusterCorrelationSize]byte]uint64{key: 17},
	}
	if !connection.cancelClusterRequest(correlation) {
		t.Fatal("active distributed request was not canceled")
	}
	if len(connection.clusterRequests) != 0 || len(connection.clusterRequestIDs) != 0 {
		t.Fatal("canceled distributed request remained active")
	}
	err := connection.dispatchResponse(websocketResponsePacket(17, "late"))
	if err != nil {
		t.Fatalf("late response became a protocol failure: %v", err)
	}
	if len(connection.ignoredClusterResponses) != 0 {
		t.Fatal("late response tombstone was not removed")
	}
}

func websocketResponsePacket(requestID uint64, value string) websocketmeta.Packet {
	return websocketmeta.Packet{
		Type:      websocketmeta.PacketResponse,
		Headers:   []websocketmeta.Header{{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)}},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentText, Data: []byte(value)}},
	}
}
