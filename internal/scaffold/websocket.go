package scaffold

import (
	"errors"
	"os"
	"path/filepath"
)

const WebSocketIndexSource = `module websocket

import (
	gox "github.com/gox-lang/gox"
)

func Index(socket *gox.Socket) gox.WebSocket {
	socket.When("handshake", func(ctx *gox.Context) bool {
		return true
	})

	socket.When("connect", func(isReconnect bool) {
	})

	socket.When("disconnect", func(reason gox.WSDisconnectReason) {
	})

	socket.When("error", func(err gox.GoxSocketError) {
	})
}
`

// EnsureWebSocket creates the optional entrypoint without overwriting user code.
func EnsureWebSocket(root string) error {
	path := filepath.Join(root, "websocket", "index.gox")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = file.WriteString(WebSocketIndexSource)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
