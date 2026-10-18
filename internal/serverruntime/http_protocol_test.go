package serverruntime

import (
	"crypto/tls"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gox-lang/gox"
	"github.com/quic-go/quic-go/http3"
)

// Exercise the actual listeners and GOX pipeline, rather than a recorder or
// httptest TLS server (which would configure HTTP/2 independently of GOX).
func TestRuntimeHTTPProtocols(t *testing.T) {
	for _, tc := range []struct {
		name       string
		secure, h2 bool
		major      int
	}{
		{"http1", false, false, 1}, {"https1", true, false, 1}, {"https2", true, true, 2}, {"https3", true, false, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"response", "stream", "panic"} {
				t.Run(mode, func(t *testing.T) {
					root, cfg, pool := manualFixture(t)
					release := make(chan struct{})
					runtime := newHotBenchmarkRuntime(func(ctx *gox.Context) {
						if mode == "panic" {
							panic("protocol recovery test")
						}
						ctx.Writer.WriteRawString("first\n")
						if mode == "stream" {
							if err := ctx.Writer.(*GoxWriter).Flush(); err != nil {
								panic(err)
							}
							<-release
						}
						ctx.Writer.WriteRawString("last\n")
					})
					defer runtime.Close()
					group, err := openRuntimeListeners(cfg, root, runtime)
					if err != nil {
						t.Fatal(err)
					}
					defer group.Close()
					defer close(release)
					group.Serve()
					transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, ForceAttemptHTTP2: tc.h2}
					defer transport.CloseIdleConnections()
					client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
					if tc.major == 3 {
						h3 := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
						defer h3.Close()
						client.Transport = h3
					}
					index := 0
					if tc.secure {
						index = 1
					}
					res, err := client.Get(group.schemes[index] + "://" + group.listeners[index].Addr().String() + "/")
					if err != nil {
						t.Fatal(err)
					}
					defer res.Body.Close()
					if res.ProtoMajor != tc.major {
						t.Fatalf("protocol=%s, want major %d", res.Proto, tc.major)
					}
					if tc.h2 && res.TLS.NegotiatedProtocol != "h2" {
						t.Fatal("HTTP/2 was not negotiated via ALPN")
					}
					if tc.secure && tc.major < 3 && res.Header.Get("Alt-Svc") != `h3=":443"; ma=86400` {
						t.Fatalf("Alt-Svc=%q", res.Header.Get("Alt-Svc"))
					}
					if !tc.secure && res.Header.Get("Alt-Svc") != "" {
						t.Fatal("HTTP must not advertise QUIC")
					}
					if tc.major == 3 && res.TLS.NegotiatedProtocol != "h3" {
						t.Fatal("missing h3 ALPN")
					}
					if mode == "panic" {
						if res.StatusCode != 500 {
							t.Fatalf("recovery status=%d", res.StatusCode)
						}
						if _, err := io.ReadAll(res.Body); err != nil {
							t.Fatal(err)
						}
						return
					}
					if res.StatusCode != 200 {
						t.Fatalf("status=%d", res.StatusCode)
					}
					if mode == "stream" {
						first := make([]byte, len("first\n"))
						if _, err := io.ReadFull(res.Body, first); err != nil {
							t.Fatal(err)
						}
						if string(first) != "first\n" {
							t.Fatalf("first chunk=%q", first)
						}
						// The first chunk must arrive before the handler can finish.
						release <- struct{}{}
						rest, err := io.ReadAll(res.Body)
						if err != nil || string(rest) != "last\n" {
							t.Fatalf("rest=%q error=%v", rest, err)
						}
					} else {
						body, err := io.ReadAll(res.Body)
						if err != nil || string(body) != "first\nlast\n" {
							t.Fatalf("body=%q error=%v", body, err)
						}
					}
				})
			}
		})
	}
}
