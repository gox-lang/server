package serverruntime

import (
	"io"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

type benchmarkRuntimeSink struct {
	header http.Header
	status int
	bytes  int
}

func newBenchmarkRuntimeSink() *benchmarkRuntimeSink {
	return &benchmarkRuntimeSink{header: make(http.Header, 8)}
}
func (w *benchmarkRuntimeSink) Header() http.Header { return w.header }
func (w *benchmarkRuntimeSink) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *benchmarkRuntimeSink) Write(p []byte) (int, error) { w.bytes += len(p); return len(p), nil }
func (w *benchmarkRuntimeSink) reset() {
	w.status, w.bytes = 0, 0
	// Keep each header's backing slice. Deleting the entries makes the benchmark
	// charge the test sink's []string allocation to responseRuntimeState.applyHeaders
	// on every iteration, even though the sink itself is deliberately reused.
	for key, values := range w.header {
		w.header[key] = values[:0]
	}
}

func newHotBenchmarkRuntime(handler func(*gox.Context)) *Runtime {
	cfg := server.Config{
		Dev: server.DevConfig{
			Enabled: true,
			Logs: server.DevLogsConfig{
				Static:    false,
				GoxFile:   false,
				Trace:     false,
				TraceDeep: false,
			},
		},
		HTTP: server.HTTPConfig{MethodsAllowed: []string{http.MethodGet}},
	}
	state := &RuntimeState{
		Config:       cfg,
		Methods:      NewMethodPolicy(cfg.HTTP.MethodsAllowed),
		IPResolver:   NewIPResolverPolicyUnsafe(nil),
		PreRouter:    &PreRouter{},
		ClientAssets: NewClientAssetPolicy(nil),
	}
	rt := &Runtime{
		Config:           cfg,
		Out:              io.Discard,
		rateLimitStore:   NewRateLimitStore(),
		staticBuildGroup: newStaticSingleflightGroup(),
		goxBuildGroup:    newStaticSingleflightGroup(),
		cache404:         &Cache404{items: make(map[string]struct{})},
		goxRoutes:        make(map[string]*GoxRouteRuntime),
	}
	rt.runtimeState.Store(state)
	rt.goxRoutes["/index"] = &GoxRouteRuntime{
		RoutePath:    "/index",
		State:        GoxRouteLoaded,
		HTTPHandlers: map[string]func(*gox.Context){http.MethodGet: handler},
	}
	return rt
}

func benchmarkHotRequest() *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "http://benchmark.local/", nil)
	return withBenchmarkRequestContext(req)
}

func BenchmarkRuntimeServeHTTPPlain(b *testing.B) {
	rt := newHotBenchmarkRuntime(func(ctx *gox.Context) {
		// Deliberately owned by the handler: this benchmark does not depend on
		// the generated GoxHTTPContentTypes export.
		ctx.Response.Headers.Set("Content-Type", "text/html; charset=utf-8")
		ctx.Writer.WriteRawString("<h1>Hello</h1>")
	})
	defer rt.rateLimitStore.Close()
	req := benchmarkHotRequest()
	sink := newBenchmarkRuntimeSink()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink.reset()
		rt.ServeHTTP(sink, req)
	}
}

func BenchmarkRuntimeServeHTTPClientIslands(b *testing.B) {
	rt := newHotBenchmarkRuntime(func(ctx *gox.Context) {
		ctx.Response.Headers.Set("Content-Type", "text/html; charset=utf-8")
		ctx.Writer.WriteRawString(`<gox-isle data-script="/_gox/client/menu.js"><span>Hello</span></gox-isle>`)
	})
	defer rt.rateLimitStore.Close()
	req := benchmarkHotRequest()
	sink := newBenchmarkRuntimeSink()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink.reset()
		rt.ServeHTTP(sink, req)
	}
}

// Run with -benchtime=10000x. The benchmark reports latency percentiles for a
// hot in-process request instead of only the arithmetic mean.
func BenchmarkRuntimeServeHTTPDistribution(b *testing.B) {
	rt := newHotBenchmarkRuntime(func(ctx *gox.Context) {
		ctx.Response.Headers.Set("Content-Type", "text/html; charset=utf-8")
		ctx.Writer.WriteRawString("<h1>Hello</h1>")
	})
	defer rt.rateLimitStore.Close()
	req := benchmarkHotRequest()
	sink := newBenchmarkRuntimeSink()
	samples := make([]int64, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink.reset()
		started := time.Now()
		rt.ServeHTTP(sink, req)
		samples[i] = time.Since(started).Nanoseconds()
	}
	b.StopTimer()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) == 0 {
		return
	}
	percentile := func(p float64) float64 {
		idx := int(float64(len(samples)-1) * p)
		return float64(samples[idx])
	}
	b.ReportMetric(percentile(.50), "p50-ns")
	b.ReportMetric(percentile(.90), "p90-ns")
	b.ReportMetric(percentile(.95), "p95-ns")
	b.ReportMetric(percentile(.99), "p99-ns")
	b.ReportMetric(float64(samples[len(samples)-1]), "max-ns")
}

func TestHotBenchmarkRuntimeDisablesRequestLogs(t *testing.T) {
	rt := newHotBenchmarkRuntime(func(ctx *gox.Context) {})
	defer rt.rateLimitStore.Close()

	if shouldLogRequest(rt.Config.Dev, "gox", "/") {
		t.Fatal("hot benchmark runtime must disable gox request logs")
	}
	if shouldTraceRequest(rt.Config.Dev, "gox", "/") {
		t.Fatal("hot benchmark runtime must disable request traces")
	}
}
