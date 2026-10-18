package serverruntime

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

// This measures the application pipeline after connection establishment,
// not network latency or the cryptographic cost of a TLS handshake.
func BenchmarkRuntimeTransport(b *testing.B) {
	for _, kind := range []string{"http", "https", "trusted-proxy"} {
		b.Run(kind, func(b *testing.B) {
			runtime := newHotBenchmarkRuntime(func(ctx *gox.Context) { ctx.Writer.WriteRawString("<p>Hello</p>") })
			defer runtime.Close()
			req := benchmarkHotRequest()
			if kind == "https" {
				req.TLS = &tls.ConnectionState{}
			}
			if kind == "trusted-proxy" {
				policy, err := NewIPResolverPolicy(server.ResolverIPForwardedConfig{TrustedProxies: []string{"127.0.0.1"}})
				if err != nil {
					b.Fatal(err)
				}
				runtime.state().IPResolver = policy
				req.RemoteAddr = "127.0.0.1:1234"
				req.Header.Set("X-Forwarded-Proto", "https")
			}
			sink := newBenchmarkRuntimeSink()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sink.reset()
				runtime.ServeHTTP(sink, req)
			}
		})
	}
}
func BenchmarkHTTPSRedirect(b *testing.B) {
	a := &automaticTLS{domains: []string{"example.com"}}
	handler := httpsRedirectAllowedHandler(http.NotFoundHandler(), a.allowsHost, nil)
	req := httptest.NewRequest("GET", "http://example.com/path?q=value", nil)
	sink := newBenchmarkRuntimeSink()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink.reset()
		handler.ServeHTTP(sink, req)
	}
}

func BenchmarkHTTP3Advertisement(b *testing.B) {
	for _, advertise := range []bool{false, true} {
		name := "baseline"
		if advertise {
			name = "with-alt-svc"
		}
		b.Run(name, func(b *testing.B) {
			runtime := newHotBenchmarkRuntime(func(ctx *gox.Context) { ctx.Writer.WriteRawString("<p>Hello</p>") })
			defer runtime.Close()
			var handler http.Handler = runtime
			if advertise {
				handler = advertiseHTTP3(handler)
			}
			req := benchmarkHotRequest()
			req.TLS = &tls.ConnectionState{}
			sink := newBenchmarkRuntimeSink()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sink.reset()
				handler.ServeHTTP(sink, req)
			}
		})
	}
}
