package serverruntime

import "github.com/gox-lang/gox"

type contextRuntimeState struct {
	renderingErrorPage bool
	requestState       *RuntimeState
	generation         *requestGeneration
}

func registerContextRuntimeState(ctx *gox.Context, state *contextRuntimeState) {
	if ctx == nil {
		return
	}
	if state == nil {
		state = &contextRuntimeState{}
	}
	*state = contextRuntimeState{}
	gox.SetRuntimeContextState(ctx, state)
}

func unregisterContextRuntimeState(ctx *gox.Context) {
	if ctx == nil {
		return
	}
	gox.SetRuntimeContextState(ctx, nil)
}

func runtimeContextState(ctx *gox.Context) *contextRuntimeState {
	if ctx == nil {
		return nil
	}
	value := gox.RuntimeContextState(ctx)
	if value == nil {
		return nil
	}
	state, ok := value.(*contextRuntimeState)
	if !ok {
		return nil
	}
	return state
}

func isRenderingErrorPage(ctx *gox.Context) bool {
	state := runtimeContextState(ctx)
	return state != nil && state.renderingErrorPage
}

func setRenderingErrorPage(ctx *gox.Context, rendering bool) {
	state := runtimeContextState(ctx)
	if state == nil {
		return
	}
	state.renderingErrorPage = rendering
}
