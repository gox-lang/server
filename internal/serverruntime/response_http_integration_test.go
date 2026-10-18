package serverruntime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gox-lang/gox"
)

func newHTTPIntegrationResponse(t *testing.T) (*gox.Response, *responseRuntimeState, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	response, state := newRuntimeResponse(recorder, nil)
	t.Cleanup(func() { unregisterRuntimeResponse(response) })
	return response, state, recorder
}

func TestDevelopmentResponsesAlwaysExpireClientCache(t *testing.T) {
	response, state, recorder := newHTTPIntegrationResponse(t)
	state.development = true
	response.Headers.Set("Cache-Control", "public, max-age=3600")
	state.WriteHeader(http.StatusOK)

	if got := recorder.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q", got)
	}
	if got := recorder.Header().Get("Expires"); got != "0" {
		t.Fatalf("Expires = %q", got)
	}
}

func TestProductionResponsesPreserveConfiguredCache(t *testing.T) {
	response, state, recorder := newHTTPIntegrationResponse(t)
	response.Headers.Set("Cache-Control", "public, max-age=3600")
	state.WriteHeader(http.StatusOK)
	if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestResponseHTTPHeadersIntegration(t *testing.T) {
	response, state, recorder := newHTTPIntegrationResponse(t)

	response.Headers.Set("X-GOX-Mode", "first")
	response.Headers.Set("x-gox-mode", "final")
	response.Headers.Append("X-Trace", "one")
	response.Headers.Append("x-trace", "two")
	response.Headers.Set("X-Removed", "value")
	response.Headers.Delete("x-removed")

	state.WriteHeader(http.StatusCreated)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if got := recorder.Header().Values("X-GOX-Mode"); len(got) != 1 || got[0] != "final" {
		t.Fatalf("X-GOX-Mode = %#v, want [final]", got)
	}
	if got := recorder.Header().Values("X-Trace"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("X-Trace = %#v, want [one two]", got)
	}
	if got := recorder.Header().Values("X-Removed"); len(got) != 0 {
		t.Fatalf("X-Removed = %#v, want no values", got)
	}
}

func TestResponseHTTPCookiesIntegration(t *testing.T) {
	t.Run("normal cookie", func(t *testing.T) {
		response, state, recorder := newHTTPIntegrationResponse(t)
		response.Cookies.Set(gox.Cookie{Name: "session", Value: "active", Path: "/", HttpOnly: true})

		state.WriteHeader(http.StatusOK)

		got := recorder.Header().Get("Set-Cookie")
		if !strings.Contains(got, "session=active") || !strings.Contains(got, "Path=/") || !strings.Contains(got, "HttpOnly") {
			t.Fatalf("Set-Cookie = %q", got)
		}
	})

	t.Run("set then delete emits nothing", func(t *testing.T) {
		response, state, recorder := newHTTPIntegrationResponse(t)
		response.Cookies.Set(gox.Cookie{Name: "session", Value: "active"})
		response.Cookies.Delete("session")

		state.WriteHeader(http.StatusOK)

		if got := recorder.Header().Values("Set-Cookie"); len(got) != 0 {
			t.Fatalf("Set-Cookie = %#v, want none", got)
		}
	})

	t.Run("expired cookie preserves explicit scope", func(t *testing.T) {
		response, state, recorder := newHTTPIntegrationResponse(t)
		response.Cookies.Set(gox.Cookie{
			Name:     "session",
			Value:    "active",
			Path:     "/account",
			Domain:   "example.com",
			Secure:   true,
			HttpOnly: true,
			SameSite: gox.SameSiteStrict,
			Expired:  true,
		})

		state.WriteHeader(http.StatusOK)

		got := recorder.Header().Get("Set-Cookie")
		for _, expected := range []string{
			"session=",
			"Path=/account",
			"Domain=example.com",
			"Expires=Thu, 01 Jan 1970 00:00:00 GMT",
			"Max-Age=0",
			"HttpOnly",
			"Secure",
			"SameSite=Strict",
		} {
			if !strings.Contains(got, expected) {
				t.Fatalf("Set-Cookie %q does not contain %q", got, expected)
			}
		}
	})

	t.Run("expired host-only cookie omits domain", func(t *testing.T) {
		response, state, recorder := newHTTPIntegrationResponse(t)
		response.Cookies.Set(gox.Cookie{Name: "session", Path: "/", Expired: true})

		state.WriteHeader(http.StatusOK)

		got := recorder.Header().Get("Set-Cookie")
		if strings.Contains(got, "Domain=") {
			t.Fatalf("Set-Cookie inferred Domain: %q", got)
		}
	})
}

func TestResponseMutationRejectedAfterHTTPWrite(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*gox.Response)
	}{
		{name: "status", mutate: func(response *gox.Response) { response.Status.SetCode(http.StatusCreated) }},
		{name: "headers", mutate: func(response *gox.Response) { response.Headers.Set("X-Late", "value") }},
		{name: "cookies", mutate: func(response *gox.Response) { response.Cookies.Set(gox.Cookie{Name: "late", Value: "value"}) }},
		{name: "import map", mutate: func(response *gox.Response) { response.ImportMap.Set("late", "/late.js") }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, state, _ := newHTTPIntegrationResponse(t)
			state.WriteHeader(http.StatusOK)

			assertInvalidResponseMutation(t, func() { test.mutate(response) })
		})
	}
}

func TestResponseMutationRejectedAfterBodyConstruction(t *testing.T) {
	response, state, _ := newHTTPIntegrationResponse(t)
	state.MarkBodyConstructed(1)

	assertInvalidResponseMutation(t, func() {
		response.Headers.Set("X-Late", "value")
	})
}

func TestResponseMutationRejectedAfterBodyWrite(t *testing.T) {
	response, state, recorder := newHTTPIntegrationResponse(t)
	if _, err := state.Write([]byte("body")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if recorder.Body.String() != "body" {
		t.Fatalf("body = %q, want body", recorder.Body.String())
	}

	assertInvalidResponseMutation(t, func() {
		response.Cookies.Set(gox.Cookie{Name: "late", Value: "value"})
	})
}

func assertInvalidResponseMutation(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		flow, ok := recovered.(gox.FlowControl)
		if !ok {
			t.Fatalf("panic = %#v, want gox.FlowControl", recovered)
		}
		if flow.Kind != gox.FlowInvalid || flow.ErrorKind != "invalid-response-mutation-after-write" {
			t.Fatalf("unexpected flow control: %#v", flow)
		}
	}()
	fn()
}
