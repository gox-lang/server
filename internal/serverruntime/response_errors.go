package serverruntime

import "github.com/gox-lang/gox"

func newResponseRuntimeError(kind string, message string) *GoxError {
	err := NewGoxError(gox.GoxErrorWriter, message, nil)
	err.Kind = kind
	err.Hint = responseErrorHint(kind)
	translateGoxError(err)
	return err
}

func newFlowRuntimeError(route *GoxRouteRuntime, ctx *gox.Context, kind string, message string, cause error) *GoxError {
	err := newGoxError(gox.GoxErrorDispatch, route, ctx, message, cause)
	err.Kind = kind
	err.Hint = responseErrorHint(kind)
	translateGoxError(err)
	return err
}

func responseErrorHint(kind string) string {
	switch kind {
	case "response-writer-nil", "response-runtime-missing", "direct-response-runtime-missing":
		return "This is an internal response writer state error. Restart the dev server and retry the request."
	case "response-writer-closed":
		return "Do not write to the response after the writer has been closed."
	case "unknown-flow-control":
		return "Use ctx.Response.DoneWith(...) or ctx.Response.Redirect.*(...) to produce supported response flow control."
	case "invalid-response-mutation-after-write":
		return "Configure response headers, cookies, status, and import map before writing the response body."
	case "invalid-response-flow-control":
		return "Check the response flow control value produced by DoneWith or Redirect."
	case "invalid-donewith-component":
		return "Pass a component function to ctx.Response.DoneWith(...)."
	case "invalid-donewith-component-signature":
		return "Use a component signature supported by Gox, such as func(ctx *gox.Context) gox.Page."
	default:
		return "Check the response writing or flow-control code for this route."
	}
}
