package serverruntime

import (
	"net/http"
	"testing"

	"github.com/gox-lang/gox"
)

func newDispatchTestContext(method string, isAction bool) *gox.Context {
	return &gox.Context{Request: gox.NewRequest(gox.RequestInput{
		Method: method,
		Route: gox.NewRoute(gox.RouteInput{
			Target:   "/index",
			Pattern:  "/index",
			Method:   method,
			IsAction: isAction,
		}),
	})}
}

func TestGoxDispatchHandlerMissingUsesMethodNotAllowed(t *testing.T) {
	ctx := newDispatchTestContext(http.MethodDelete, false)
	err := newGoxDispatchError(&GoxRouteRuntime{RoutePath: "/index", RelPath: "pages/index.gox"}, ctx, "method not allowed", http.StatusMethodNotAllowed)

	if err.StatusCode != http.StatusMethodNotAllowed || err.Status != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d/%d, want 405", err.StatusCode, err.Status)
	}
	if err.Message != "method not allowed" {
		t.Fatalf("message = %q", err.Message)
	}
}

func TestGoxDispatchActionMissingUsesMethodNotAllowed(t *testing.T) {
	ctx := newDispatchTestContext("MyAction", true)
	err := newGoxDispatchError(&GoxRouteRuntime{RoutePath: "/index", RelPath: "pages/index.gox"}, ctx, "method not allowed", http.StatusMethodNotAllowed)

	if err.StatusCode != http.StatusMethodNotAllowed || err.Status != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d/%d, want 405", err.StatusCode, err.Status)
	}
	if !err.IsAction {
		t.Fatal("expected action dispatch error to preserve IsAction")
	}
}

func TestGoxDispatchMethodNotAllowedDoesNotLogDiagnosticBox(t *testing.T) {
	ctx := newDispatchTestContext(http.MethodOptions, false)
	err := newGoxDispatchError(&GoxRouteRuntime{RoutePath: "/index", RelPath: "pages/index.gox"}, ctx, "method not allowed", http.StatusMethodNotAllowed)

	if shouldLogGoxRequestError(err) {
		t.Fatal("405 dispatch errors should not print the diagnostic error box")
	}
}
