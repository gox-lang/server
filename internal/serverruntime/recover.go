package serverruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"syscall"

	"github.com/gox-lang/gox"
)

// Close runs after finishRequest's initial recover, so it needs its own boundary.
func (r *Runtime) closeRequestWriter(ctx *gox.Context, req *http.Request, closer interface{ Close() error }) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok && isResponseDisconnect(err) {
				return
			}
			r.recoverRequest(ctx, req, recovered)
		}
	}()
	if err := closer.Close(); err != nil && ctx.Error == nil && !isResponseDisconnect(err) {
		panic(err)
	}
}

// A disconnected peer cannot receive an error page. Match error identities,
// including wrapped socket errors, rather than hiding arbitrary writer failures
// just because the request was canceled or its message mentions a disconnect.
func isResponseDisconnect(err error) bool {
	return errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, context.Canceled)
}

func (r *Runtime) recoverRequest(ctx *gox.Context, req *http.Request, recovered any) {
	frame := relevantPanicFrame()

	err := NewGoxPanicError(recovered)
	err.StatusCode = http.StatusInternalServerError
	err.Status = http.StatusInternalServerError
	err.StatusMessage = http.StatusText(http.StatusInternalServerError)
	err.Kind = "panic"
	err.Message = fmt.Sprint(recovered)
	err.File = normalizeGoxUserFacingPath(frame.File)
	err.Line = frame.Line
	err.AddDetail("panic.function", frame.Function)
	applyGoxErrorContext(err, nil, ctx)
	translateGoxError(err)

	r.renderGoxError(ctx, req, nil, err)
}

func relevantPanicFrame() runtime.Frame {
	pc := make([]uintptr, 20)
	n := runtime.Callers(3, pc)
	frames := runtime.CallersFrames(pc[:n])

	var fallback runtime.Frame

	for {
		frame, more := frames.Next()
		if fallback.Function == "" {
			fallback = frame
		}

		if !isInternalPanicFrame(frame.Function) {
			return frame
		}

		if !more {
			break
		}
	}

	return fallback
}

func isInternalPanicFrame(function string) bool {
	return strings.Contains(function, "runtime.") ||
		strings.Contains(function, "net/http")
}
