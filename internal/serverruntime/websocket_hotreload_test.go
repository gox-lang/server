package serverruntime

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gox-lang/gox"
	websocketmeta "github.com/gox-lang/server/internal/websocket"
)

func hotReloadSocketHandler(version string) gox.WebSocketHandler {
	return func(socket *gox.Socket) {
		socket.When("handshake", func(*gox.Context) bool { return true })
		socket.When("connect", func(bool) { socket.Emit("version", version) })
	}
}

func TestWebSocketHotReloadReconnectsWithNewHandlers(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	root := t.TempDir()
	runtime.ProjectRoot = root
	route := &GoxRouteRuntime{RoutePath: webSocketModuleRoutePath, State: GoxRouteLoaded, BuildVersion: "old", WebSocketHandler: hotReloadSocketHandler("old")}
	runtime.goxRoutes[webSocketModuleRoutePath] = route
	server := httptest.NewServer(runtime)
	defer server.Close()
	dial := func() *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+websocketmeta.HandshakePath, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		return conn
	}
	version := func(conn *websocket.Conn) string {
		t.Helper()
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		packet, err := websocketmeta.DecodePacket(data)
		if err != nil {
			t.Fatal(err)
		}
		return string(packet.Arguments[0].Data)
	}
	old := dial()
	if got := version(old); got != "old" {
		t.Fatal(got)
	}
	buildDir := filepath.Join(root, ".build", "websocket")
	candidate := &GoxRouteRuntime{RoutePath: webSocketModuleRoutePath, State: GoxRouteLoaded, BuildVersion: "new", BuildDir: buildDir, GeneratedPath: filepath.Join(buildDir, "new.go"), PluginPath: filepath.Join(buildDir, "new.so"), WebSocketHandler: hotReloadSocketHandler("new")}
	if err := runtime.commitGoxRouteCandidate(route, candidate); err != nil {
		t.Fatal(err)
	}
	_, _, err := old.ReadMessage()
	if !websocket.IsCloseError(err, int(gox.WSCloseReconnect)) {
		t.Fatalf("expected automatic reconnect, got %v", err)
	}
	if got := version(dial()); got != "new" {
		t.Fatalf("new connection uses %q", got)
	}
}

func TestWebSocketHotReloadRejectsMissingHandler(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	route := &GoxRouteRuntime{RoutePath: webSocketModuleRoutePath, State: GoxRouteLoaded, BuildVersion: "old", WebSocketHandler: hotReloadSocketHandler("old")}
	if err := runtime.commitGoxRouteCandidate(route, &GoxRouteRuntime{BuildVersion: "bad"}); err == nil {
		t.Fatal("missing handler accepted")
	}
	if route.BuildVersion != "old" || route.WebSocketHandler == nil {
		t.Fatal("working module was replaced")
	}
}

func TestWebSocketHotReloadCatchesHandshakeInProgress(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	root := t.TempDir()
	runtime.ProjectRoot = root
	started, release := make(chan struct{}), make(chan struct{})
	route := &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath, State: GoxRouteLoaded, BuildVersion: "old",
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool {
				close(started)
				<-release
				return true
			})
		},
	}
	runtime.goxRoutes[webSocketModuleRoutePath] = route
	server := httptest.NewServer(runtime)
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+websocketmeta.HandshakePath, nil)
		if err == nil {
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, _, err = conn.ReadMessage()
		}
		done <- err
	}()
	<-started
	dir := filepath.Join(root, ".build", "websocket")
	err := runtime.commitGoxRouteCandidate(route, &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath, State: GoxRouteLoaded, BuildVersion: "new",
		BuildDir: dir, GeneratedPath: filepath.Join(dir, "new.go"), PluginPath: filepath.Join(dir, "new.so"),
		WebSocketHandler: hotReloadSocketHandler("new"),
	})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; !websocket.IsCloseError(err, int(gox.WSCloseReconnect)) {
		t.Fatalf("stale handshake was not reconnected: %v", err)
	}
}
