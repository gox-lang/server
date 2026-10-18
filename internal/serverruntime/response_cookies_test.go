package serverruntime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gox-lang/gox"
)

func TestApplyHeadersWritesCookiesInNameOrder(t *testing.T) {
	recorder := httptest.NewRecorder()
	response, state := newRuntimeResponse(recorder, nil)
	response.Cookies.Set(gox.Cookie{Name: "zeta", Value: "last", Path: "/"})
	response.Cookies.Set(gox.Cookie{Name: "alpha", Value: "first", Path: "/"})

	state.applyHeaders()

	got := recorder.Header().Values("Set-Cookie")
	if len(got) != 2 {
		t.Fatalf("Set-Cookie count = %d, want 2: %#v", len(got), got)
	}
	if got[0] != "alpha=first; Path=/" || got[1] != "zeta=last; Path=/" {
		t.Fatalf("unexpected Set-Cookie order: %#v", got)
	}
}

func TestToHTTPCookiePreservesNormalCookie(t *testing.T) {
	expires := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	got := toHTTPCookie(gox.Cookie{
		Name:     "session",
		Value:    "active",
		Path:     "/account",
		Domain:   "example.com",
		MaxAge:   3600,
		Expires:  expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: gox.SameSiteStrict,
	})

	if got.Name != "session" || got.Value != "active" {
		t.Fatalf("identity fields changed: %#v", got)
	}
	if got.Path != "/account" || got.Domain != "example.com" {
		t.Fatalf("scope fields changed: %#v", got)
	}
	if got.MaxAge != 3600 || !got.Expires.Equal(expires) {
		t.Fatalf("lifetime fields changed: %#v", got)
	}
	if !got.HttpOnly || !got.Secure || got.SameSite != http.SameSiteStrictMode {
		t.Fatalf("security fields changed: %#v", got)
	}
}

func TestToHTTPCookieAppliesExpiredLifetime(t *testing.T) {
	got := toHTTPCookie(gox.Cookie{
		Name:     "session",
		Value:    "active",
		Path:     "/account",
		Domain:   "example.com",
		MaxAge:   3600,
		Expires:  time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC),
		HttpOnly: true,
		Secure:   true,
		SameSite: gox.SameSiteNone,
		Expired:  true,
	})

	if got.Value != "" {
		t.Fatalf("Value = %q, want empty", got.Value)
	}
	if got.MaxAge != -1 {
		t.Fatalf("MaxAge = %d, want -1", got.MaxAge)
	}
	if !got.Expires.Equal(time.Unix(0, 0)) {
		t.Fatalf("Expires = %v, want %v", got.Expires, time.Unix(0, 0))
	}
	if got.Name != "session" || got.Path != "/account" || got.Domain != "example.com" {
		t.Fatalf("scope fields changed: %#v", got)
	}
	if !got.HttpOnly || !got.Secure || got.SameSite != http.SameSiteNoneMode {
		t.Fatalf("security fields changed: %#v", got)
	}
}

func TestToHTTPCookieExpiredDoesNotInferScope(t *testing.T) {
	got := toHTTPCookie(gox.Cookie{Name: "session", Expired: true})

	if got.Path != "" {
		t.Fatalf("Path = %q, want empty", got.Path)
	}
	if got.Domain != "" {
		t.Fatalf("Domain = %q, want empty", got.Domain)
	}
}

func TestApplyHeadersWritesExpiredCookie(t *testing.T) {
	recorder := httptest.NewRecorder()
	response, state := newRuntimeResponse(recorder, nil)
	response.Cookies.Set(gox.Cookie{
		Name:     "session",
		Value:    "active",
		Path:     "/account",
		HttpOnly: true,
		Expired:  true,
	})

	state.applyHeaders()

	got := recorder.Header().Get("Set-Cookie")
	if !strings.HasPrefix(got, "session=; Path=/account;") {
		t.Fatalf("Set-Cookie = %q", got)
	}
	if !strings.Contains(got, "Expires=Thu, 01 Jan 1970 00:00:00 GMT") {
		t.Fatalf("Set-Cookie does not contain past Expires: %q", got)
	}
	if !strings.Contains(got, "Max-Age=0") {
		t.Fatalf("Set-Cookie does not contain deletion Max-Age: %q", got)
	}
	if !strings.Contains(got, "HttpOnly") {
		t.Fatalf("Set-Cookie does not preserve HttpOnly: %q", got)
	}
}
