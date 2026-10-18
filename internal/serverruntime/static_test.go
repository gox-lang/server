package serverruntime

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gox-lang/gox/server"
)

func newTestStaticPolicy(t testing.TB, cfg server.StaticConfig) (*StaticPolicy, string) {
	t.Helper()

	root := t.TempDir()
	public := filepath.Join(root, "public")

	if err := os.MkdirAll(filepath.Join(public, "assets", "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("<h1>hello</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "assets", "css", "app.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	policy, err := NewStaticPolicy(root, cfg)
	if err != nil {
		t.Fatalf("NewStaticPolicy failed: %v", err)
	}

	return policy, root
}

func TestStaticPolicyResolveFilePath(t *testing.T) {
	policy, root := newTestStaticPolicy(t, server.StaticConfig{})

	got, ok := policy.resolveFilePath("/assets/css/app.css")
	if !ok {
		t.Fatal("expected static path to resolve")
	}

	want := filepath.Join(root, "public", "assets", "css", "app.css")
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestStaticPolicyRejectsTraversal(t *testing.T) {
	policy, _ := newTestStaticPolicy(t, server.StaticConfig{})

	for _, path := range []string{"/../secret.txt", "/assets/../../secret.txt", "/"} {
		t.Run(path, func(t *testing.T) {
			if got, ok := policy.resolveFilePath(path); ok {
				t.Fatalf("resolveFilePath(%q) = %q, true; want false", path, got)
			}
		})
	}
}

func TestStaticContentType(t *testing.T) {
	if got := staticContentType("/tmp/app.css"); got != "text/css; charset=utf-8" {
		t.Fatalf("css content-type = %q", got)
	}

	if got := staticContentType("/tmp/file.unknown-gox-extension"); got != "application/octet-stream" {
		t.Fatalf("unknown content-type = %q", got)
	}
}

func TestStaticNotModifiedByETag(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/app.css", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("If-None-Match", `"abc", "def"`)

	if !staticNotModified(req, `"def"`, time.Now()) {
		t.Fatal("expected If-None-Match to pass")
	}
}

func TestStaticNotModifiedByDate(t *testing.T) {
	lastModified := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)

	req, err := http.NewRequest(http.MethodGet, "/app.css", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("If-Modified-Since", lastModified.Add(time.Second).Format(http.TimeFormat))

	if !staticNotModified(req, `"etag"`, lastModified) {
		t.Fatal("expected If-Modified-Since to pass")
	}
}

func TestSelectStaticEncoding(t *testing.T) {
	cfg := server.CompressionConfig{
		Enabled:       true,
		OrderPriority: []string{"gzip", "br", "zstd"},
		Gzip:          server.CompressionAlgorithmConfig{Enabled: true, Level: 3},
		Brotli:        server.CompressionAlgorithmConfig{Enabled: true, Level: 4},
		Zstd:          server.CompressionAlgorithmConfig{Enabled: true, Level: 1},
	}

	if got := selectStaticEncoding(cfg, "br;q=1, gzip;q=0.5"); got != "br" {
		t.Fatalf("encoding = %q, want br", got)
	}

	if got := selectStaticEncoding(cfg, "gzip;q=0, br;q=0"); got != staticIdentityEncoding {
		t.Fatalf("encoding = %q, want identity", got)
	}
}

func BenchmarkSelectStaticEncoding(b *testing.B) {
	cfg := server.DefaultHTTPCompressionConfig()
	const accept = "gzip;q=0.8, br;q=0.7, zstd;q=1, deflate;q=0.3"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if got := selectStaticEncoding(cfg, accept); got != "zstd" {
			b.Fatalf("encoding = %q, want zstd", got)
		}
	}
}

func TestParseStaticRange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		header    string
		fileSize  int64
		wantStart int64
		wantEnd   int64
		wantLen   int64
		wantOK    bool
	}{
		{name: "start-end", header: "bytes=10-19", fileSize: 100, wantStart: 10, wantEnd: 19, wantLen: 10, wantOK: true},
		{name: "open-end", header: "bytes=90-", fileSize: 100, wantStart: 90, wantEnd: 99, wantLen: 10, wantOK: true},
		{name: "suffix", header: "bytes=-10", fileSize: 100, wantStart: 90, wantEnd: 99, wantLen: 10, wantOK: true},
		{name: "invalid-unit", header: "items=0-1", fileSize: 100, wantOK: false},
		{name: "past-eof", header: "bytes=100-200", fileSize: 100, wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseStaticRange(tc.header, tc.fileSize)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.start != tc.wantStart || got.end != tc.wantEnd || got.length != tc.wantLen {
				t.Fatalf(
					"range = %d-%d length=%d, want %d-%d length=%d",
					got.start,
					got.end,
					got.length,
					tc.wantStart,
					tc.wantEnd,
					tc.wantLen,
				)
			}
		})
	}
}

func TestCapStaticRange(t *testing.T) {
	spec := staticRangeSpec{
		start:  10,
		end:    99,
		length: 90,
	}

	got := capStaticRange(spec, 100, staticRangeSettings{
		enabled:      true,
		maxChunkSize: 20,
	})

	if got.start != 10 || got.end != 29 || got.length != 20 {
		t.Fatalf("capped range = %d-%d length=%d, want 10-29 length=20", got.start, got.end, got.length)
	}
}

func TestStaticPolicyMatchRangeOrdered(t *testing.T) {
	policy, _ := newTestStaticPolicy(t, server.StaticConfig{
		Range: []server.StaticRangeRule{
			{Match: "/assets/*", Enabled: true, MaxChunkSize: 128},
			{Match: "*", Enabled: false},
		},
	})

	settings := policy.matchRange("/assets/css/app.css")
	if !settings.enabled || settings.maxChunkSize != 128 {
		t.Fatalf("asset settings = %#v", settings)
	}

	settings = policy.matchRange("/index.html")
	if settings.enabled {
		t.Fatalf("fallback settings = %#v, want disabled", settings)
	}
}
