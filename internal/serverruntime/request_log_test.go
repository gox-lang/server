package serverruntime

import (
	"net/http"
	"testing"

	"github.com/gox-lang/gox"
)

func TestRequestLogMethodUsesResolvedRouteMethod(t *testing.T) {
	ctx := &gox.Context{Request: gox.Request{Route: gox.NewRoute(gox.RouteInput{Method: "Greeting"})}}
	req, err := http.NewRequest(http.MethodPost, "/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := requestLogMethod(ctx, req); got != "Greeting" {
		t.Fatalf("method = %q, want Greeting", got)
	}
}

func TestRequestLogMethodFallsBackToHTTPMethod(t *testing.T) {
	ctx := &gox.Context{}
	req, err := http.NewRequest(http.MethodGet, "/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := requestLogMethod(ctx, req); got != http.MethodGet {
		t.Fatalf("method = %q, want GET", got)
	}
}
