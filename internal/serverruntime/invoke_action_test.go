package serverruntime

import (
	"net/http"
	"testing"

	"github.com/gox-lang/gox"
)

func newInvokeActionTestContext(method string, action string) *gox.Context {
	headerValues := map[string][]string{}
	if action != "" {
		headerValues["Invoke-Action"] = []string{action}
	}
	headers := gox.NewCollection[string](headerValues)
	return &gox.Context{Request: gox.NewRequest(gox.RequestInput{
		Method:  method,
		URL:     gox.NewURL(gox.URLInput{Path: "/"}),
		Headers: headers,
	})}
}

func TestInvokeActionRequiresPOST(t *testing.T) {
	ctx := newInvokeActionTestContext(http.MethodGet, "MyAction")
	if isGoxActionRequest(ctx) {
		t.Fatal("GET with Invoke-Action must not be treated as an action")
	}
	if got := resolveGoxRouteMethod(ctx); got != http.MethodGet {
		t.Fatalf("method = %q, want GET", got)
	}

	ctx = newInvokeActionTestContext(http.MethodPost, "MyAction")
	if !isGoxActionRequest(ctx) {
		t.Fatal("POST with Invoke-Action should be treated as an action")
	}
	if got := resolveGoxRouteMethod(ctx); got != "MyAction" {
		t.Fatalf("method = %q, want MyAction", got)
	}
}

func TestPreRouteDoesNotMarkGETInvokeActionAsAction(t *testing.T) {
	rt := &Runtime{}
	ctx := newInvokeActionTestContext(http.MethodGet, "MyAction")
	rt.preRoute(ctx)

	if ctx.Request.Route.IsAction() {
		t.Fatal("GET with Invoke-Action must keep route IsAction=false")
	}
	if got := ctx.Request.Route.Method(); got != http.MethodGet {
		t.Fatalf("route method = %q, want GET", got)
	}
}

func TestPreRouteMarksPOSTInvokeActionAsAction(t *testing.T) {
	rt := &Runtime{}
	ctx := newInvokeActionTestContext(http.MethodPost, "MyAction")
	rt.preRoute(ctx)

	if !ctx.Request.Route.IsAction() {
		t.Fatal("POST with Invoke-Action should mark route IsAction=true")
	}
	if got := ctx.Request.Route.Method(); got != "MyAction" {
		t.Fatalf("route method = %q, want MyAction", got)
	}
}
