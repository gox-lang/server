package serverruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	websocketmeta "github.com/gox-lang/server/internal/websocket"

	"github.com/gorilla/websocket"
	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

type clusterResponseCaptureRuntime struct {
	*clusterTestRuntime
	responses chan capturedClusterResponse
}

type capturedClusterResponse struct {
	targetNodeID  string
	correlationID []byte
	payload       []byte
}

func (runtime *clusterResponseCaptureRuntime) PublishSocketResponse(_ context.Context, targetNodeID string, correlationID []byte, payload []byte) error {
	runtime.responses <- capturedClusterResponse{
		targetNodeID:  targetNodeID,
		correlationID: append([]byte(nil), correlationID...),
		payload:       append([]byte(nil), payload...),
	}
	return nil
}

var _ server.ClusterRuntime = (*clusterResponseCaptureRuntime)(nil)

func TestWebSocketIncomingFileAndFilesReachStrictGoHandler(t *testing.T) {
	socket := &gox.Socket{}
	var avatar gox.File
	var gallery []gox.File
	dispatched := make(chan struct{})
	socket.On("upload", func(file gox.File, files []gox.File) {
		avatar = file
		gallery = files
		close(dispatched)
	})
	connection := &webSocketRuntimeConnection{socket: socket}
	addTransfer := func(id uint64, name string, mime string, data []byte) {
		t.Helper()
		packets := []websocketmeta.Packet{
			{Type: websocketmeta.PacketTransferBegin, Headers: []websocketmeta.Header{
				{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(id)},
				{Type: websocketmeta.HeaderTotalSize, Value: websocketmeta.EncodeCompactUint(uint64(len(data)))},
				{Type: websocketmeta.HeaderFileName, Value: []byte(name)},
				{Type: websocketmeta.HeaderMIMEType, Value: []byte(mime)},
			}},
		}
		if len(data) > 0 {
			packets = append(packets, websocketmeta.Packet{Type: websocketmeta.PacketTransferChunk, Headers: []websocketmeta.Header{
				{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(id)},
				{Type: websocketmeta.HeaderSequence, Value: websocketmeta.EncodeCompactUint(0)},
			}, Payload: data})
		}
		packets = append(packets, websocketmeta.Packet{Type: websocketmeta.PacketTransferEnd, Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(id)},
			{Type: websocketmeta.HeaderTotalSize, Value: websocketmeta.EncodeCompactUint(uint64(len(data)))},
		}})
		for _, packet := range packets {
			if err := connection.receiveTransfer(packet); err != nil {
				t.Fatal(err)
			}
		}
	}
	addTransfer(1, "avatar.png", "image/png", []byte{1, 2, 3})
	addTransfer(2, "a.txt", "text/plain", []byte("A"))
	addTransfer(3, "b.txt", "text/plain", []byte("B"))
	fileRef, _ := websocketmeta.EncodeFileReference(1)
	filesRef, _ := websocketmeta.EncodeFilesReference([]uint64{2, 3})
	connection.dispatchEvent(websocketmeta.Packet{Type: websocketmeta.PacketEvent, Headers: []websocketmeta.Header{
		{Type: websocketmeta.HeaderEventName, Value: []byte("upload")},
		{Type: websocketmeta.HeaderEventID, Value: websocketmeta.EncodeCompactUint(1)},
	}, Arguments: []websocketmeta.Argument{
		{Type: websocketmeta.ArgumentFile, Data: fileRef},
		{Type: websocketmeta.ArgumentFiles, Data: filesRef},
	}})
	select {
	case <-dispatched:
	case <-time.After(time.Second):
		t.Fatal("upload handler was not dispatched")
	}
	if avatar.Name() != "avatar.png" || avatar.ContentType() != "image/png" {
		t.Fatalf("avatar = %#v", avatar)
	}
	reader, err := avatar.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(data, []byte{1, 2, 3}) {
		t.Fatalf("avatar data = %v, %v", data, err)
	}
	if len(gallery) != 2 || gallery[0].Name() != "a.txt" || gallery[1].Name() != "b.txt" {
		t.Fatalf("gallery = %#v", gallery)
	}
	if len(connection.incoming) != 0 {
		t.Fatalf("completed transfers were not released: %#v", connection.incoming)
	}
}

func TestWebSocketIncomingTransferRejectsDuplicateAndOutOfOrderChunks(t *testing.T) {
	connection := &webSocketRuntimeConnection{socket: &gox.Socket{}, maxTransferSize: 8}
	begin := websocketmeta.Packet{Type: websocketmeta.PacketTransferBegin, Headers: []websocketmeta.Header{
		{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(1)},
		{Type: websocketmeta.HeaderTotalSize, Value: websocketmeta.EncodeCompactUint(3)},
		{Type: websocketmeta.HeaderFileName, Value: []byte("a.txt")},
		{Type: websocketmeta.HeaderMIMEType, Value: []byte("text/plain")},
	}}
	if err := connection.receiveTransfer(begin); err != nil {
		t.Fatal(err)
	}
	if err := connection.receiveTransfer(begin); err == nil {
		t.Fatal("duplicate transfer was accepted")
	}
	chunk := websocketmeta.Packet{Type: websocketmeta.PacketTransferChunk, Headers: []websocketmeta.Header{
		{Type: websocketmeta.HeaderTransferID, Value: websocketmeta.EncodeCompactUint(1)},
		{Type: websocketmeta.HeaderSequence, Value: websocketmeta.EncodeCompactUint(1)},
	}, Payload: []byte{1}}
	if err := connection.receiveTransfer(chunk); err == nil {
		t.Fatal("out-of-order chunk was accepted")
	}
	oversized := begin
	oversized.Headers = append([]websocketmeta.Header(nil), begin.Headers...)
	oversized.Headers[0].Value = websocketmeta.EncodeCompactUint(2)
	oversized.Headers[1].Value = websocketmeta.EncodeCompactUint(9)
	if err := connection.receiveTransfer(oversized); err == nil {
		t.Fatal("oversized transfer was accepted")
	}
}

func TestWebSocketModuleResolvesOutsidePages(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "websocket", "index.gox")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("module websocket\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := &Runtime{ProjectRoot: root}
	file, ok, err := runtime.resolveGoxRouteFile(webSocketModuleRoutePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("websocket/index.gox was not resolved")
	}
	if file.absPath != path {
		t.Fatalf("resolved path = %q, want %q", file.absPath, path)
	}
}

func TestWebSocketModuleUsesDedicatedBuildDirectory(t *testing.T) {
	root := t.TempDir()
	buildDir, version, generatedPath, pluginPath, err := resolveGoxBuildPathsWithVersion(root, webSocketModuleRoutePath, "123")
	if err != nil {
		t.Fatal(err)
	}
	if version != "123" {
		t.Fatalf("build version = %q", version)
	}
	wantDir := filepath.Join(root, ".build", "websocket")
	if buildDir != wantDir {
		t.Fatalf("build directory = %q, want %q", buildDir, wantDir)
	}
	if generatedPath != filepath.Join(wantDir, "123.go") {
		t.Fatalf("generated path = %q", generatedPath)
	}
	if pluginPath != filepath.Join(wantDir, "123.so") {
		t.Fatalf("plugin path = %q", pluginPath)
	}
}

func TestWebSocketBuildCleanupAcceptsDedicatedDirectory(t *testing.T) {
	root := t.TempDir()
	buildDir := filepath.Join(root, ".build", "websocket")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	generatedPath := filepath.Join(buildDir, "current.go")
	pluginPath := filepath.Join(buildDir, "current.so")
	for _, path := range []string{generatedPath, pluginPath, filepath.Join(buildDir, "stale.go")} {
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	route := &GoxRouteRuntime{
		RoutePath:     webSocketModuleRoutePath,
		BuildDir:      buildDir,
		GeneratedPath: generatedPath,
		PluginPath:    pluginPath,
	}
	if err := cleanupGoxRouteBuildArtifacts(root, route); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(buildDir, "stale.go")); !os.IsNotExist(err) {
		t.Fatalf("stale WebSocket artifact was not removed: %v", err)
	}
}

func newEnabledWebSocketRouteTestRuntime() *Runtime {
	runtime := newHotBenchmarkRuntime(func(*gox.Context) {})
	runtime.Config.WebSocket.Enabled = true
	runtime.state().Config.WebSocket.Enabled = true
	return runtime
}

func TestWebSocketClientRoute(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_gox/websocket/client.js", nil)
	runtime.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !strings.Contains(recorder.Body.String(), "export class Socket") {
		t.Fatalf("unexpected client body: %q", recorder.Body.String())
	}
}

func TestWebSocketHandshakeUpgradesAndDispatchesLifecycle(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	connected := make(chan bool, 1)
	disconnected := make(chan gox.WSDisconnectReason, 1)
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.When("connect", func(isReconnect bool) { connected <- isReconnect })
			socket.When("disconnect", func(reason gox.WSDisconnectReason) { disconnected <- reason })
		},
	}

	server := httptest.NewServer(runtime)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/_gox/websocket/handshake"
	conn, response, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("upgrade failed with status %d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	select {
	case isReconnect := <-connected:
		if isReconnect {
			t.Fatal("first connection was reported as a reconnection")
		}
	case <-time.After(time.Second):
		t.Fatal("connect lifecycle was not dispatched")
	}
	if err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done")); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	select {
	case reason := <-disconnected:
		if reason.Code != gox.WSCloseNormal || reason.Initiator != gox.WSDisconnectInitiatorClient || reason.WillReconnect {
			t.Fatalf("unexpected disconnect reason: %#v", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect lifecycle was not dispatched")
	}
}

func TestWebSocketHandshakeSnapshotIsAvailableToLifecycleHandlers(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	handshakes := make(chan gox.SocketHandshake, 2)
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool {
				handshakes <- socket.Handshake
				return true
			})
			socket.When("connect", func(bool) {
				handshakes <- socket.Handshake
			})
		},
	}

	server := httptest.NewServer(runtime)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/_gox/websocket/handshake?room=general"
	headers := http.Header{
		"User-Agent": {"GOX Browser"},
		"X-Trace-ID": {"trace-1"},
		"Cookie":     {"theme=dark"},
	}
	conn, response, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("upgrade failed with status %d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer conn.Close()

	for index := 0; index < 2; index++ {
		select {
		case handshake := <-handshakes:
			if handshake.Method() != http.MethodGet || handshake.IP() == "" {
				t.Fatalf("missing handshake identity: %#v", handshake)
			}

			traceID, ok := handshake.Headers.Get("X-Trace-ID")
			if handshake.UserAgent() != "GOX Browser" || !ok || traceID != "trace-1" {
				t.Fatal("handshake headers were not preserved")
			}
			theme, themeOK := handshake.Cookies.Get("theme")
			room, roomOK := handshake.Query.Get("room")

			if !themeOK || theme != "dark" || !roomOK || room != "general" {
				t.Fatal("handshake cookies or query were not preserved")
			}
		case <-time.After(time.Second):
			t.Fatal("handshake snapshot was not available")
		}
	}
}

func TestServerSocketDisconnectSendsCodeAndReason(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	disconnected := make(chan gox.WSDisconnectReason, 1)
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.When("connect", func(bool) { socket.Disconnect(gox.WSClosePermanent) })
			socket.When("disconnect", func(reason gox.WSDisconnectReason) { disconnected <- reason })
		},
	}

	server := httptest.NewServer(runtime)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/_gox/websocket/handshake"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _, err = conn.NextReader()
	closeErr, ok := err.(*websocket.CloseError)
	if !ok || closeErr.Code != int(gox.WSClosePermanent) || closeErr.Text != gox.WSCloseReason(gox.WSClosePermanent) {
		t.Fatalf("unexpected client close error: %#v", err)
	}
	select {
	case reason := <-disconnected:
		if reason.Code != gox.WSClosePermanent || reason.Reason != gox.WSCloseReason(gox.WSClosePermanent) || reason.Initiator != gox.WSDisconnectInitiatorServer || reason.WillReconnect {
			t.Fatalf("unexpected server disconnect reason: %#v", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("server disconnect lifecycle was not dispatched")
	}
}

func TestWebSocketReconnectMarkerReachesServerLifecycle(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	connected := make(chan bool, 1)
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.When("connect", func(isReconnect bool) { connected <- isReconnect })
		},
	}
	server := httptest.NewServer(runtime)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/_gox/websocket/handshake?_gox_reconnect=1"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case isReconnect := <-connected:
		if !isReconnect {
			t.Fatal("reconnect marker was not dispatched")
		}
	case <-time.After(time.Second):
		t.Fatal("connect lifecycle was not dispatched")
	}
}

func TestWebSocketHandshakeRejectsFalse(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return false })
		},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/_gox/websocket/handshake", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	runtime.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestWebSocketHandshakeDefaultRequiresSameOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://example.com/_gox/websocket/handshake", nil)
	request.Header.Set("Origin", "https://other.example")
	if sameWebSocketOrigin(request) {
		t.Fatal("cross-origin request was accepted")
	}
	request.Header.Set("Origin", "http://example.com")
	if !sameWebSocketOrigin(request) {
		t.Fatal("same-origin request was rejected")
	}
}

func TestWebSocketHandlerFailureDispatchesTypedSocketError(t *testing.T) {
	socket := &gox.Socket{}
	received := make(chan gox.GoxSocketError, 1)
	socket.When("connect", func(bool) { panic("boom") })
	socket.When("error", func(err gox.GoxSocketError) { received <- err })

	dispatchWebSocketConnect(socket, false)
	select {
	case err := <-received:
		if err.Code != gox.GoxSocketHandlerFailed || err.Phase != gox.GoxSocketPhaseHandler || err.Event != "connect" {
			t.Fatalf("unexpected socket error: %#v", err)
		}
		if err.Cause == nil || !strings.Contains(err.Cause.Error(), "boom") {
			t.Fatalf("missing handler cause: %v", err.Cause)
		}
	case <-time.After(time.Second):
		t.Fatal("typed socket error was not dispatched")
	}
}

func TestClientBinaryEventBindsStrictGoHandler(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	type user struct {
		ID int `json:"id"`
	}
	received := make(chan []any, 1)
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.On("user", func(name string, age int, admin bool, profile user) {
				received <- []any{name, age, admin, profile}
			})
		},
	}
	server := httptest.NewServer(runtime)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/_gox/websocket/handshake", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketEvent,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte("user")},
			{Type: websocketmeta.HeaderEventID, Value: websocketmeta.EncodeCompactUint(1)},
		},
		Arguments: []websocketmeta.Argument{
			{Type: websocketmeta.ArgumentText, Data: []byte("Javier")},
			{Type: websocketmeta.ArgumentNumber, Data: []byte("42")},
			{Type: websocketmeta.ArgumentBoolean, Data: []byte{1}},
			{Type: websocketmeta.ArgumentJSON, Data: []byte(`{"id":7}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, packet); err != nil {
		t.Fatal(err)
	}
	select {
	case values := <-received:
		if values[0] != "Javier" || values[1] != 42 || values[2] != true || values[3].(user).ID != 7 {
			t.Fatalf("handler values = %#v", values)
		}
	case <-time.After(time.Second):
		t.Fatal("On handler was not dispatched")
	}
}

func TestWebSocketInternalSignatureRequestReturnsOnParameterNames(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.OnRegistered("profile", []string{"name", "age", "avatar"}, true, func(string, int, gox.File) gox.EventReply { return nil })
		},
	}
	server := httptest.NewServer(runtime)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/_gox/websocket/handshake", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type:  websocketmeta.PacketRequest,
		Flags: websocketmeta.FlagInternal,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte("gox:get-signature")},
			{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(7)},
		},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentText, Data: []byte("profile")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, request); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	response, err := websocketmeta.DecodePacket(data)
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != websocketmeta.PacketResponse || response.Flags != websocketmeta.FlagInternal {
		t.Fatalf("response = %#v", response)
	}
	requestID, _ := websocketmeta.HeaderUint(response, websocketmeta.HeaderRequestID)
	if requestID != 7 {
		t.Fatalf("request ID = %d", requestID)
	}
	var signature webSocketEventSignature
	if err := json.Unmarshal(response.Arguments[0].Data, &signature); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(signature.Parameters, []string{"name", "age", "avatar"}) || !signature.HasReply {
		t.Fatalf("signature = %#v", signature)
	}
}

func TestWebSocketPublicRequestReturnsSingleEventReply(t *testing.T) {
	type profile struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.OnRegistered("profile", []string{"profileID"}, true, func(profileID string) gox.EventReply {
				return profile{ID: profileID, Name: "Javier"}
			})
		},
	}
	server := httptest.NewServer(runtime)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/_gox/websocket/handshake", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketRequest,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte("profile")},
			{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(9)},
		},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentText, Data: []byte("profile-42")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, request); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	response, err := websocketmeta.DecodePacket(data)
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != websocketmeta.PacketResponse || response.Flags != 0 {
		t.Fatalf("response = %#v", response)
	}
	requestID, _ := websocketmeta.HeaderUint(response, websocketmeta.HeaderRequestID)
	if requestID != 9 {
		t.Fatalf("request ID = %d", requestID)
	}
	if len(response.Arguments) != 1 || response.Arguments[0].Type != websocketmeta.ArgumentJSON {
		t.Fatalf("arguments = %#v", response.Arguments)
	}
	var received profile
	if err := json.Unmarshal(response.Arguments[0].Data, &received); err != nil {
		t.Fatal(err)
	}
	if received.ID != "profile-42" || received.Name != "Javier" {
		t.Fatalf("reply = %#v", received)
	}
}

func TestServerEmitAwaitReceivesTypedJavaScriptReplyWithoutBlockingReader(t *testing.T) {
	type theme struct {
		Name     string `json:"name"`
		Contrast string `json:"contrast"`
	}
	result := make(chan theme, 1)
	errors := make(chan error, 1)
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.When("connect", func(bool) {
				var received theme
				if err := socket.EmitAwait(&received, "get-theme", "dark"); err != nil {
					errors <- err
					return
				}
				result <- received
			})
		},
	}
	server := httptest.NewServer(runtime)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/_gox/websocket/handshake", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	request, err := websocketmeta.DecodePacket(data)
	if err != nil {
		t.Fatal(err)
	}
	if request.Type != websocketmeta.PacketRequest || websocketmeta.EventName(request) != "get-theme" {
		t.Fatalf("request = %#v", request)
	}
	requestID, _ := websocketmeta.HeaderUint(request, websocketmeta.HeaderRequestID)
	response, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type:      websocketmeta.PacketResponse,
		Headers:   []websocketmeta.Header{{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)}},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentJSON, Data: []byte(`{"name":"dark","contrast":"high"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, response); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-result:
		if received.Name != "dark" || received.Contrast != "high" {
			t.Fatalf("reply = %#v", received)
		}
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("EmitAwait remained blocked")
	}
}

func TestDistributedRequestRoutesThroughOwningBrowserAndPublishesDirectedResponse(t *testing.T) {
	connected := make(chan *gox.Socket, 1)
	cluster := &clusterResponseCaptureRuntime{
		clusterTestRuntime: &clusterTestRuntime{},
		responses:          make(chan capturedClusterResponse, 1),
	}
	runtime := newEnabledWebSocketRouteTestRuntime()
	runtime.clusterRuntime = cluster
	defer runtime.rateLimitStore.Close()
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.When("connect", func(bool) { connected <- socket })
		},
	}
	httpServer := httptest.NewServer(runtime)
	defer httpServer.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"/_gox/websocket/handshake", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	socket := <-connected
	originRaw := bytes.Repeat([]byte{7}, clusterCorrelationSize)
	originNodeID := base64.RawURLEncoding.EncodeToString(originRaw)
	correlation := bytes.Repeat([]byte{8}, clusterCorrelationSize)
	request, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketRequest,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte("browser-profile")},
			{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(91)},
		},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentText, Data: []byte("Javier")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := gox.RouteSocketClusterRequest(socket, originNodeID, correlation, request); err != nil {
		t.Fatal(err)
	}
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	routed, err := websocketmeta.DecodePacket(data)
	if err != nil {
		t.Fatal(err)
	}
	requestID, _ := websocketmeta.HeaderUint(routed, websocketmeta.HeaderRequestID)
	if requestID == 0 || requestID == 91 || websocketmeta.EventName(routed) != "browser-profile" {
		t.Fatalf("routed request = %#v", routed)
	}
	reply, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type:      websocketmeta.PacketResponse,
		Headers:   []websocketmeta.Header{{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(requestID)}},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentText, Data: []byte("ready")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.WriteMessage(websocket.BinaryMessage, reply); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-cluster.responses:
		if response.targetNodeID != originNodeID || !bytes.Equal(response.correlationID, correlation) {
			t.Fatalf("cluster response = %#v", response)
		}
		packet, err := websocketmeta.DecodePacket(response.payload)
		if err != nil || packet.Type != websocketmeta.PacketResponse || string(packet.Arguments[0].Data) != "ready" {
			t.Fatalf("response packet = %#v, err=%v", packet, err)
		}
	case <-time.After(time.Second):
		t.Fatal("browser response was not returned to the origin node")
	}
}

func TestServerAwaitReturnsOnlyReplyBindingErrors(t *testing.T) {
	var destination int
	pending := &webSocketPendingRequest{event: "count", out: &destination, done: make(chan struct{})}
	connection := &webSocketRuntimeConnection{
		socket:          &gox.Socket{},
		pendingRequests: map[uint64]*webSocketPendingRequest{4: pending},
	}
	err := connection.dispatchResponse(websocketmeta.Packet{
		Type:      websocketmeta.PacketResponse,
		Headers:   []websocketmeta.Header{{Type: websocketmeta.HeaderRequestID, Value: websocketmeta.EncodeCompactUint(4)}},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentText, Data: []byte("not-a-number")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	<-pending.done
	if pending.castErr == nil || !strings.Contains(pending.castErr.Error(), "expected int") {
		t.Fatalf("cast error = %v", pending.castErr)
	}
	if destination != 0 {
		t.Fatalf("destination changed to %d", destination)
	}
}

func TestServerEmitWritesBinaryEventToClient(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.When("connect", func(bool) {
				socket.Emit("server-values", "GOX", 42, uint64(1<<53))
			})
		},
	}
	server := httptest.NewServer(runtime)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/_gox/websocket/handshake", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("message type = %d", messageType)
	}
	packet, err := websocketmeta.DecodePacket(data)
	if err != nil {
		t.Fatal(err)
	}
	if websocketmeta.EventName(packet) != "server-values" {
		t.Fatalf("event = %q", websocketmeta.EventName(packet))
	}
	if len(packet.Arguments) != 3 || packet.Arguments[0].Type != websocketmeta.ArgumentText || packet.Arguments[1].Type != websocketmeta.ArgumentNumber || packet.Arguments[2].Type != websocketmeta.ArgumentBigInt {
		t.Fatalf("arguments = %#v", packet.Arguments)
	}
}

func TestBindingFailureReturnsCorrelatedErrorToClient(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	localErrors := make(chan gox.GoxSocketError, 1)
	called := make(chan struct{}, 1)
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.When("error", func(err gox.GoxSocketError) { localErrors <- err })
			socket.On("user", func(string, int, bool) { called <- struct{}{} })
		},
	}
	server := httptest.NewServer(runtime)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/_gox/websocket/handshake", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	packet, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketEvent,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte("user")},
			{Type: websocketmeta.HeaderEventID, Value: websocketmeta.EncodeCompactUint(77)},
		},
		Arguments: []websocketmeta.Argument{
			{Type: websocketmeta.ArgumentText, Data: []byte("Javier")},
			{Type: websocketmeta.ArgumentNumber, Data: []byte("42")},
			{Type: websocketmeta.ArgumentText, Data: []byte("hola mundo")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, packet); err != nil {
		t.Fatal(err)
	}
	messageType, responseData, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("message type = %d", messageType)
	}
	response, err := websocketmeta.DecodePacket(responseData)
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != websocketmeta.PacketError {
		t.Fatalf("packet type = 0x%02x", response.Type)
	}
	if websocketmeta.EventName(response) != "user" || websocketmeta.HeaderText(response, websocketmeta.HeaderErrorCode) != "ARGUMENT_TYPE_MISMATCH" {
		t.Fatalf("error headers = %#v", response.Headers)
	}
	if eventID, _ := websocketmeta.HeaderUint(response, websocketmeta.HeaderEventID); eventID != 77 {
		t.Fatalf("event ID = %d", eventID)
	}
	var details webSocketEventErrorDetails
	if err := json.Unmarshal(response.Arguments[0].Data, &details); err != nil {
		t.Fatal(err)
	}
	if details.Argument != 2 || details.Received != "TEXT" || details.Expected != "bool" {
		t.Fatalf("error details = %#v", details)
	}
	select {
	case socketErr := <-localErrors:
		if socketErr.Code != gox.GoxSocketArgumentTypeMismatch || socketErr.EventID != 77 {
			t.Fatalf("local error = %#v", socketErr)
		}
	case <-time.After(time.Second):
		t.Fatal("server error lifecycle was not dispatched")
	}
	select {
	case <-called:
		t.Fatal("handler ran after binding failure")
	default:
	}
}

func TestMalformedClientFrameClosesProtocolAndSkipsHandlers(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	errorsReceived := make(chan gox.GoxSocketError, 1)
	called := make(chan struct{}, 1)
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.When("error", func(err gox.GoxSocketError) { errorsReceived <- err })
			socket.On("event", func() { called <- struct{}{} })
		},
	}
	server := httptest.NewServer(runtime)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/_gox/websocket/handshake", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("not binary")); err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.ReadMessage()
	closeErr, ok := err.(*websocket.CloseError)
	if !ok || closeErr.Code != websocket.CloseProtocolError {
		t.Fatalf("close error = %#v", err)
	}
	select {
	case socketErr := <-errorsReceived:
		if socketErr.Code != gox.GoxSocketProtocolError {
			t.Fatalf("socket error = %#v", socketErr)
		}
	case <-time.After(time.Second):
		t.Fatal("protocol error lifecycle was not dispatched")
	}
	select {
	case <-called:
		t.Fatal("On handler ran for malformed frame")
	default:
	}
}

func TestClientHandlerFailureReturnsToServerErrorLifecycle(t *testing.T) {
	runtime := newEnabledWebSocketRouteTestRuntime()
	defer runtime.rateLimitStore.Close()
	remoteErrors := make(chan gox.GoxSocketError, 1)
	runtime.goxRoutes[webSocketModuleRoutePath] = &GoxRouteRuntime{
		RoutePath: webSocketModuleRoutePath,
		State:     GoxRouteLoaded,
		WebSocketHandler: func(socket *gox.Socket) {
			socket.When("handshake", func(*gox.Context) bool { return true })
			socket.When("connect", func(bool) { socket.Emit("client-event", 42) })
			socket.When("error", func(err gox.GoxSocketError) { remoteErrors <- err })
		},
	}
	server := httptest.NewServer(runtime)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/_gox/websocket/handshake", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, eventData, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	eventPacket, err := websocketmeta.DecodePacket(eventData)
	if err != nil {
		t.Fatal(err)
	}
	eventID, _ := websocketmeta.HeaderUint(eventPacket, websocketmeta.HeaderEventID)
	details, _ := json.Marshal(webSocketEventErrorDetails{Message: "client handler failed", Argument: -1})
	errorPacket, err := websocketmeta.EncodePacket(websocketmeta.Packet{
		Type: websocketmeta.PacketError,
		Headers: []websocketmeta.Header{
			{Type: websocketmeta.HeaderEventName, Value: []byte("client-event")},
			{Type: websocketmeta.HeaderErrorCode, Value: []byte("HANDLER_FAILED")},
			{Type: websocketmeta.HeaderEventID, Value: websocketmeta.EncodeCompactUint(eventID)},
		},
		Arguments: []websocketmeta.Argument{{Type: websocketmeta.ArgumentJSON, Data: details}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, errorPacket); err != nil {
		t.Fatal(err)
	}
	select {
	case socketErr := <-remoteErrors:
		if socketErr.Code != gox.GoxSocketHandlerFailed || !socketErr.Remote || socketErr.EventID != eventID || socketErr.Event != "client-event" {
			t.Fatalf("remote error = %#v", socketErr)
		}
	case <-time.After(time.Second):
		t.Fatal("remote client error was not dispatched")
	}
}
