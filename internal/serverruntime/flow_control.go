package serverruntime

import (
	"fmt"
	"net/http"
	"reflect"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/gox-lang/gox"
)

var (
	goxWriterReflectType  = reflect.TypeOf((*gox.Writer)(nil)).Elem()
	goxContextReflectType = reflect.TypeOf((*gox.Context)(nil))
)

func executeResponseFlow(ctx *gox.Context, route *GoxRouteRuntime, flow gox.FlowControl, trace *requestPipelineTrace) *GoxError {
	if ctx == nil {
		return nil
	}

	if res, ok := runtimeResponse(ctx); ok && res != nil {
		res.ApplyFlow(flow)
	}

	switch flow.Kind {
	case gox.FlowDoneWith:
		if trace != nil {
			trace.addDeepField("flow.donewith", fmt.Sprint(flow.StatusCode))
		}
		discardPendingFlowBody(ctx)
		if flow.Component != nil {
			name := componentDisplayName(flow.Component)
			if name != "" {
				if trace != nil {
					trace.addDeepField("flow.component", name)
				}
			}
			var start time.Time
			if trace != nil {
				start = trace.nowDeep()
			}
			err := renderFlowComponent(ctx, route, flow.Component)
			if trace != nil {
				trace.addDeep("flow.component.render", start)
			}
			return err
		}
		if !isHTTPErrorStatus(flow.StatusCode) {
			renderDoneWithStatusText(ctx, flow.StatusCode)
		}
		return nil

	case gox.FlowRedirect:
		if trace != nil {
			trace.addDeepField("flow.redirect", fmt.Sprint(flow.StatusCode))
			if flow.Location != "" {
				trace.addDeepField("flow.location", flow.Location)
			}
		}
		renderRedirect(ctx, flow.StatusCode, flow.Location)
		return nil

	case gox.FlowInvalid:
		if trace != nil {
			trace.addDeepField("flow.invalid", flow.Message)
		}
		message := flow.Message
		if message == "" {
			message = "invalid response flow control"
		}
		err := newFlowRuntimeError(route, ctx, "invalid-response-flow-control", message, nil)
		if flow.File != "" {
			err.File = flow.File
		}
		if flow.Line > 0 {
			err.Line = flow.Line
		}
		if flow.ErrorKind != "" {
			err.Kind = flow.ErrorKind
			err.Hint = responseErrorHint(flow.ErrorKind)
		}
		applyGoxSourceMapDiagnostic(err, route)
		err.File = normalizeGoxUserFacingPath(err.File)
		translateGoxError(err)
		return err

	default:
		return newFlowRuntimeError(route, ctx, "unknown-flow-control", "unknown response flow control", nil)
	}
}

func discardPendingFlowBody(ctx *gox.Context) {
	if ctx == nil || ctx.Writer == nil {
		return
	}
	if discard, ok := ctx.Writer.(interface{ DiscardBuffered() }); ok {
		discard.DiscardBuffered()
	}
}

func renderRedirect(ctx *gox.Context, status int, location string) {
	discardPendingFlowBody(ctx)
	if ctx == nil || ctx.Response == nil {
		return
	}
	ctx.Response.Headers.Set("Location", location)
	writeResponseHeader(ctx, status)
}

func renderDoneWithStatusText(ctx *gox.Context, status int) {
	if ctx == nil || ctx.Response == nil {
		return
	}
	writeResponseHeader(ctx, status)
	if status == http.StatusNoContent || status == http.StatusNotModified {
		return
	}
	message := http.StatusText(status)
	if message == "" {
		return
	}
	_, _ = ctx.Writer.Write([]byte(message))
}

func renderFlowComponent(ctx *gox.Context, route *GoxRouteRuntime, component gox.Component) (err *GoxError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if flow, ok := recovered.(gox.FlowControl); ok {
				err = executeResponseFlow(ctx, route, flow, nil)
				return
			}
			err = newGoxPanicRuntimeError(route, ctx, recovered)
		}
	}()

	if component == nil {
		return nil
	}

	value := reflect.ValueOf(component)
	if !value.IsValid() || value.Kind() != reflect.Func {
		return newFlowRuntimeError(route, ctx, "invalid-donewith-component", "invalid response component", nil)
	}

	fnType := value.Type()
	args, ok := buildComponentCallArgs(ctx, fnType)
	if !ok {
		return newFlowRuntimeError(route, ctx, "invalid-donewith-component-signature", "invalid response component signature", nil)
	}

	value.Call(args)
	return nil
}

func buildComponentCallArgs(ctx *gox.Context, fnType reflect.Type) ([]reflect.Value, bool) {
	if ctx == nil || fnType == nil {
		return nil, false
	}
	if fnType.IsVariadic() {
		return nil, false
	}

	args := make([]reflect.Value, 0, fnType.NumIn())
	for i := 0; i < fnType.NumIn(); i++ {
		argType := fnType.In(i)
		switch {
		case argType == goxContextReflectType:
			args = append(args, reflect.ValueOf(ctx))

		case isWriterArgType(argType):
			if ctx.Writer == nil {
				return nil, false
			}
			writerValue := reflect.ValueOf(ctx.Writer)
			if !writerValue.Type().AssignableTo(argType) {
				if writerValue.Type().ConvertibleTo(argType) {
					writerValue = writerValue.Convert(argType)
				} else if argType.Kind() == reflect.Interface && writerValue.Type().Implements(argType) {
					// reflect can pass concrete values to interface parameters directly.
				} else {
					return nil, false
				}
			}
			args = append(args, writerValue)

		default:
			return nil, false
		}
	}
	return args, true
}

func isWriterArgType(argType reflect.Type) bool {
	if argType == nil {
		return false
	}
	if argType == goxWriterReflectType {
		return true
	}
	return argType.Kind() == reflect.Interface && goxWriterReflectType.AssignableTo(argType)
}

func componentDisplayName(component gox.Component) string {
	value := reflect.ValueOf(component)
	if !value.IsValid() || value.Kind() != reflect.Func {
		return ""
	}
	fn := goruntime.FuncForPC(value.Pointer())
	if fn == nil {
		return ""
	}
	name := fn.Name()
	if idx := strings.LastIndex(name, "."); idx >= 0 && idx+1 < len(name) {
		name = name[idx+1:]
	}
	return strings.TrimSuffix(name, "-fm")
}
