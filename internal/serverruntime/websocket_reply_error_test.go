package serverruntime

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/gox-lang/gox"
	websocketmeta "github.com/gox-lang/server/internal/websocket"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSocketPublicRequestReturnedError(t *testing.T) {
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
				return fmt.Errorf("Elige un archivo de hasta 1 MiB.")
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
	if response.Type != websocketmeta.PacketError || response.Flags != 0 {
		t.Fatalf("response = %#v", response)
	}
	requestID, _ := websocketmeta.HeaderUint(response, websocketmeta.HeaderRequestID)
	if requestID != 9 {
		t.Fatalf("request ID = %d", requestID)
	}
	if len(response.Arguments) != 1 || response.Arguments[0].Type != websocketmeta.ArgumentJSON {
		t.Fatalf("arguments = %#v", response.Arguments)
	}
	var details struct{ Message string }
	if err := json.Unmarshal(response.Arguments[0].Data, &details); err != nil {
		t.Fatal(err)
	}
	if details.Message != "Elige un archivo de hasta 1 MiB." {
		t.Fatalf("message = %q", details.Message)
	}
	var code []byte
	for _, header := range response.Headers {
		if header.Type == websocketmeta.HeaderErrorCode {
			code = header.Value
		}
	}
	if string(code) != string(gox.GoxSocketHandlerFailed) {
		t.Fatalf("code = %s", code)
	}
}
