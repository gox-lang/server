package serverruntime

import (
	"net/http/httptest"
	"testing"
)

func TestSplitRequestHost(t *testing.T) {
	tests := []struct {
		input    string
		hostname string
		port     string
	}{
		{"example.com", "example.com", ""},
		{"example.com:8080", "example.com", "8080"},
		{"[::1]:8080", "::1", "8080"},
		{"[::1]", "::1", ""},
		{"::1", "::1", ""},
	}
	for _, test := range tests {
		hostname, port := splitRequestHost(test.input)
		if hostname != test.hostname || port != test.port {
			t.Fatalf("splitRequestHost(%q) = (%q, %q), want (%q, %q)", test.input, hostname, port, test.hostname, test.port)
		}
	}
}

func TestBuildRequestEmptyFastPath(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/hello", nil)
	built := buildRequest(req)

	if got := built.URL.Hostname(); got != "example.com" {
		t.Fatalf("hostname = %q", got)
	}
	if !built.Query.IsEmpty() {
		t.Fatal("empty query should remain empty")
	}
	if !built.Cookies.IsEmpty() {
		t.Fatal("empty cookies should remain empty")
	}
}

func TestBuildRequestURLStringPreservesAbsoluteRequest(t *testing.T) {
	req := httptest.NewRequest("GET", "https://example.com:8443/hello%20world?page=1", nil)
	built := buildRequest(req)

	if got := built.URL.String(); got != "https://example.com:8443/hello%20world?page=1" {
		t.Fatalf("URL.String() = %q", got)
	}
}

func TestBuildRequestURLStringPreservesUnescapedPath(t *testing.T) {
	req := httptest.NewRequest("GET", "https://example.com/health?page=1", nil)
	built := buildRequest(req)

	if got := built.URL.String(); got != "https://example.com/health?page=1" {
		t.Fatalf("URL.String() = %q", got)
	}
}
