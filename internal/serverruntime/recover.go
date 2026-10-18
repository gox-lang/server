package serverruntime

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/gox-lang/gox"
)

// Close runs after finishRequest's initial recover, so it needs its own boundary.
func (r *Runtime) closeRequestWriter(ctx *gox.Context, req *http.Request, closer interface{ Close() error }) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.recoverRequest(ctx, req, recovered)
		}
	}()
	if err := closer.Close(); err != nil && ctx.Error == nil {
		panic(err)
	}
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
