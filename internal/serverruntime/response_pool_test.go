package serverruntime

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gox-lang/gox"
)

func TestRuntimeResponsePoolClearsState(t *testing.T) {
	firstRecorder := httptest.NewRecorder()
	response, state := newRuntimeResponse(firstRecorder, nil)
	response.Status.SetCode(201)
	response.Headers.Set("Content-Type", "application/json")
	response.Headers.Set("X-Test", "old")
	state.bodyWritten = true
	state.bodyConstructed = true
	state.done = true
	state.location = "/old"

	if !gox.ResetRuntimeBody(state.body, io.NopCloser(strings.NewReader("old")), "text/plain") {
		t.Fatal("expected pooled body to be resettable")
	}

	releaseRuntimeResponse(state)

	secondRecorder := httptest.NewRecorder()
	response, reused := newRuntimeResponse(secondRecorder, nil)
	defer releaseRuntimeResponse(reused)

	if got := response.Status.GetCode(); got != 200 {
		t.Fatalf("status leaked across pooled responses: got %d", got)
	}
	if response.Headers.Has("Content-Type") || response.Headers.Has("X-Test") {
		t.Fatalf("headers leaked across pooled responses: %#v", response.Headers.Values())
	}
	if reused.bodyWritten || reused.bodyConstructed || reused.done || reused.location != "" {
		t.Fatalf("runtime state leaked across pooled responses: %#v", reused)
	}
}
