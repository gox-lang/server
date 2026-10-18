package scaffold

import (
	"strings"
	"testing"
)

func TestProjectFilesIncludeWebSocketConfigScaffold(t *testing.T) {
	files := ProjectFiles(Options{
		ProjectName:      "demo",
		GoVersion:        "1.26",
		GoxModule:        "github.com/gox-lang/gox",
		GoxModuleVersion: "v0.1.0",
	})

	contents := map[string]string{}
	for _, file := range files {
		contents[file.Path] = file.Content
	}

	webSocket, ok := contents["app/websocket.gox"]
	if !ok {
		t.Fatal("websocket config scaffold missing")
	}

	for _, want := range []string{
		"module websocket",
		"server.WebSocketConfig{",
		"Enabled: false",
		"HandshakeTimeout: 10 * time.Second",
		"MaxMessageSize: 8 << 20",
		"WriteTimeout:   10 * time.Second",
		"PingInterval: 25 * time.Second",
		"PongTimeout:  60 * time.Second",
		"MaxMessages: 256",
		"MaxBytes:    16 << 20",
		"ReadSize:  4096",
		"WriteSize: 4096",
		"Enabled: false",
		"Level:   1",
	} {
		if !strings.Contains(webSocket, want) {
			t.Fatalf("websocket scaffold missing %q:\n%s", want, webSocket)
		}
	}

	config := contents["app/config.gox"]
	for _, want := range []string{
		`"websocket.gox"`,
		"WebSocket: websocket.Config",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config scaffold missing %q:\n%s", want, config)
		}
	}

	session := contents["app/session.gox"]
	if strings.Contains(session, "Storage:") || strings.Contains(session, "NewMemorySessionAdapter") {
		t.Fatalf("session scaffold must use GOX's internal memory fallback:\n%s", session)
	}

	if _, ok := contents["websocket/index.gox"]; ok {
		t.Fatal("optional websocket entrypoint created eagerly")
	}
	index := WebSocketIndexSource

	for _, want := range []string{
		"module websocket",
		"func Index(socket *gox.Socket) gox.WebSocket",
		`socket.When("handshake", func(ctx *gox.Context) bool`,
		`socket.When("connect", func(isReconnect bool)`,
		`socket.When("disconnect", func(reason gox.WSDisconnectReason)`,
		`socket.When("error", func(err gox.GoxSocketError)`,
	} {
		if !strings.Contains(index, want) {
			t.Fatalf("websocket index scaffold missing %q:\n%s", want, index)
		}
	}
}
