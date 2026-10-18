package serverruntime

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gox-lang/gox"
)

func TestRuntimeFinalizesEmptyResponses(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		status       int
		panicHandler bool
		body         string
	}{
		{name: "options", method: http.MethodOptions, status: 204},
		{name: "head", method: http.MethodHead, status: 200},
		{name: "empty get", method: http.MethodGet, status: 200},
		{name: "created", method: http.MethodPost, status: 201},
		{name: "not modified", method: http.MethodGet, status: 304},
		{name: "panic after pending headers", method: http.MethodGet, status: 204, panicHandler: true},
		{name: "existing body", method: http.MethodGet, status: 201, body: "created"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newHotBenchmarkRuntime(func(ctx *gox.Context) {
				ctx.Response.Headers.Set("Allow", "POST, OPTIONS")
				ctx.Response.Headers.Set("X-Empty-Test", "retained")
				ctx.Response.Cookies.Set(gox.Cookie{Name: "empty-test", Value: "yes", Path: "/"})
				ctx.Response.Status.SetCode(tc.status)
				if tc.panicHandler {
					panic("empty response test")
				}
				if tc.body != "" {
					ctx.Writer.WriteRawString(tc.body)
				}
			})
			defer rt.rateLimitStore.Close()
			state := rt.runtimeState.Load()
			state.Methods = NewMethodPolicy([]string{tc.method})
			route := rt.goxRoutes["/index"]
			route.HTTPHandlers[tc.method] = route.HTTPHandlers[http.MethodGet]
			rec := httptest.NewRecorder()
			rt.ServeHTTP(rec, httptest.NewRequest(tc.method, "http://example.test/", nil))
			result := rec.Result()
			defer result.Body.Close()
			if tc.panicHandler {
				if result.StatusCode != 500 {
					t.Fatalf("panic status = %d, want 500", result.StatusCode)
				}
				if rec.Body.Len() == 0 {
					t.Fatal("panic response missing error body")
				}
				return
			}
			if result.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", result.StatusCode, tc.status)
			}
			if result.Header.Get("Allow") != "POST, OPTIONS" || result.Header.Get("X-Empty-Test") != "retained" {
				t.Fatalf("missing response headers: %v", result.Header)
			}
			if len(result.Cookies()) != 1 || result.Cookies()[0].Value != "yes" {
				t.Fatalf("cookies = %v", result.Cookies())
			}
			if rec.Body.String() != tc.body {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.body)
			}
		})
	}
}
