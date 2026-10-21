package serverruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/gox-lang/gox"
)

type failingHTTPWriter struct {
	*httptest.ResponseRecorder
	calls      int
	failure    error
	short      bool
	panicWrite bool
}

func (w *failingHTTPWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.panicWrite {
		panic(w.failure)
	}
	if w.short {
		return 0, nil
	}
	return 0, w.failure
}

func TestWriteByteFailureReachesHTTPRecovery(t *testing.T) {
	for _, kind := range []string{"byte", "short", "close", "close-panic"} {
		t.Run(kind, func(t *testing.T) {
			output := &failingHTTPWriter{ResponseRecorder: httptest.NewRecorder(), failure: errors.New("transport unavailable"), short: kind == "short", panicWrite: kind == "close-panic"}
			completed := false
			runtime := newHotBenchmarkRuntime(func(ctx *gox.Context) {
				if kind == "byte" || kind == "short" {
					writer := ctx.Writer.(*GoxWriter)
					ctx.Writer.WriteRawString(strings.Repeat("x", len(writer.buffer)))
					if err := ctx.Writer.WriteByte('!'); err != nil {
						panic(err)
					}
				} else {
					ctx.Writer.WriteRawString("buffered until close")
				}
				completed = true
			})
			defer runtime.rateLimitStore.Close()
			defer runtime.cache404.Close()
			runtime.ServeHTTP(output, httptest.NewRequest("GET", "http://example/", nil))
			if completed != (kind == "close" || kind == "close-panic") {
				t.Fatalf("handler completion = %v", completed)
			}
			if output.calls != 1 {
				t.Fatalf("failed transport retried: %d writes", output.calls)
			}
		})
	}
}

func TestWriterCloseFailureRecordedAndBufferReleased(t *testing.T) {
	for _, kind := range []string{"error", "short", "panic"} {
		t.Run(kind, func(t *testing.T) {
			failure := errors.New("transport unavailable")
			if kind == "short" {
				failure = io.ErrShortWrite
			}
			output := &failingHTTPWriter{ResponseRecorder: httptest.NewRecorder(), failure: failure, short: kind == "short", panicWrite: kind == "panic"}
			response, state := newRuntimeResponse(output, nil)
			defer unregisterRuntimeResponse(response)
			req := httptest.NewRequest("GET", "/", nil)
			writer := newGoxWriter(state, req, noCompressionConfig(), false, false)
			writer.WriteByte('x')
			ctx := &gox.Context{Response: response, Writer: writer}
			runtime := &Runtime{}
			runtime.closeRequestWriter(ctx, req, writer)
			if ctx.Error == nil || !strings.Contains(ctx.Error.Error(), failure.Error()) {
				t.Fatalf("lost close failure: %v", ctx.Error)
			}
			if writer.bufferPtr != nil || !writer.closed {
				t.Fatal("buffer was not released")
			}
			if output.calls != 1 {
				t.Fatal("recovery retried failed transport")
			}
		})
	}
}

// Test both returned errors and panics from a writer: neither should turn a
// normal peer disconnect into an application error or prevent buffer cleanup.
func TestWriterCloseDisconnectReleasesBufferWithoutHTTPError(t *testing.T) {
	failures := map[string]error{
		"broken-pipe": &net.OpError{Op: "write", Net: "tcp", Err: &os.SyscallError{Syscall: "write", Err: syscall.EPIPE}},
		"reset":       fmt.Errorf("write response: %w", syscall.ECONNRESET),
		"closed":      fmt.Errorf("write response: %w", net.ErrClosed),
		"canceled":    fmt.Errorf("write response: %w", context.Canceled),
	}
	for name, failure := range failures {
		for _, panicWrite := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", name, panicWrite), func(t *testing.T) {
				output := &failingHTTPWriter{ResponseRecorder: httptest.NewRecorder(), failure: failure, panicWrite: panicWrite}
				response, state := newRuntimeResponse(output, nil)
				defer unregisterRuntimeResponse(response)
				req := httptest.NewRequest("GET", "/", nil)
				writer := newGoxWriter(state, req, noCompressionConfig(), false, false)
				if err := writer.WriteByte('x'); err != nil {
					t.Fatal(err)
				}
				ctx := &gox.Context{Response: response, Writer: writer}
				runtime := &Runtime{}
				runtime.closeRequestWriter(ctx, req, writer)
				if ctx.Error != nil {
					t.Fatalf("disconnect became an HTTP error: %v", ctx.Error)
				}
				if writer.bufferPtr != nil || !writer.closed {
					t.Fatal("buffer was not released")
				}
				if output.calls != 1 {
					t.Fatalf("disconnected transport retried: %d writes", output.calls)
				}
			})
		}
	}
}

func TestWriterCloseFailureStillReportedForCanceledRequest(t *testing.T) {
	for _, failure := range []error{errors.New("write: broken pipe"), context.DeadlineExceeded, errors.New("compression failed")} {
		t.Run(failure.Error(), func(t *testing.T) {
			output := &failingHTTPWriter{ResponseRecorder: httptest.NewRecorder(), failure: failure}
			response, state := newRuntimeResponse(output, nil)
			defer unregisterRuntimeResponse(response)
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest("GET", "/", nil).WithContext(canceled)
			writer := newGoxWriter(state, req, noCompressionConfig(), false, false)
			if err := writer.WriteByte('x'); err != nil {
				t.Fatal(err)
			}
			ctx := &gox.Context{Response: response, Writer: writer}
			runtime := &Runtime{}
			runtime.closeRequestWriter(ctx, req, writer)
			if ctx.Error == nil {
				t.Fatal("unrelated failure was hidden by request cancellation")
			}
		})
	}
}
