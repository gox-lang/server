package lsp

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBackgroundStartsOnceAndStopsWithTransport(t *testing.T) {
	server := NewServer()
	var started, stopped atomic.Int32
	server.Background = func(ctx context.Context, notify func(string, any) error) {
		started.Add(1)
		<-ctx.Done()
		stopped.Add(1)
	}
	var wire strings.Builder
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	} {
		fmt.Fprintf(&wire, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	var output bytes.Buffer
	if err := server.Serve(strings.NewReader(wire.String()), &output); err != nil {
		t.Fatal(err)
	}
	if started.Load() != 1 || stopped.Load() != 1 {
		t.Fatalf("background start=%d stop=%d", started.Load(), stopped.Load())
	}
}
