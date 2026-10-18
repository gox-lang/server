package serverruntime

import (
	"testing"

	"github.com/gox-lang/gox"
	websocketmeta "github.com/gox-lang/server/internal/websocket"
	"github.com/gox-lang/gox/server"
)

func TestClusterEventEncodingProducesCanonicalWebSocketEvent(t *testing.T) {
	runtime := &Runtime{}
	packets, err := runtime.encodeClusterEventPackets("updated", []any{"GOX", 42, true})
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) != 1 {
		t.Fatalf("packet count = %d, want 1", len(packets))
	}
	packet, err := websocketmeta.DecodePacket(packets[0])
	if err != nil {
		t.Fatal(err)
	}
	if packet.Type != websocketmeta.PacketEvent || websocketmeta.EventName(packet) != "updated" || len(packet.Arguments) != 3 {
		t.Fatalf("WebSocket packet = %#v", packet)
	}
}

func TestClusterTargetSelectionHonorsSessionAndSocket(t *testing.T) {
	runtime := &Runtime{}
	registry := runtime.webSocketRegistry()
	first := &gox.Socket{}
	second := &gox.Socket{}
	third := &gox.Socket{}
	if err := registry.register(first, server.NewSessionReference("session-1")); err != nil {
		t.Fatal(err)
	}
	if err := registry.register(second, server.NewSessionReference("session-1")); err != nil {
		t.Fatal(err)
	}
	if err := registry.register(third, server.NewSessionReference("session-2")); err != nil {
		t.Fatal(err)
	}
	if sockets := runtime.clusterTargetSockets(server.ClusterTarget{Kind: server.ClusterTargetSession, SessionID: "session-1"}); len(sockets) != 2 {
		t.Fatalf("session target count = %d, want 2", len(sockets))
	}
	if sockets := runtime.clusterTargetSockets(server.ClusterTarget{Kind: server.ClusterTargetSocket, SocketID: third.ID()}); len(sockets) != 1 || sockets[0] != third {
		t.Fatal("socket target did not resolve the exact connection")
	}
}
