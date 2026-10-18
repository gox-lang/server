package serverruntime

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

var dynamicCompressionBenchmarkBytes int

// benchmarkResponseWriter is a minimal response sink for core writer benchmarks.
// It records only status and byte count, avoiding the second body copy performed by
// httptest.ResponseRecorder.
type benchmarkResponseWriter struct {
	header http.Header
	status int
	bytes  int
}

func newBenchmarkResponseWriter() *benchmarkResponseWriter {
	return &benchmarkResponseWriter{header: make(http.Header, 4)}
}

func (w *benchmarkResponseWriter) Header() http.Header { return w.header }

func (w *benchmarkResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *benchmarkResponseWriter) Write(p []byte) (int, error) {
	w.bytes += len(p)
	return len(p), nil
}

func (w *benchmarkResponseWriter) reset() {
	w.status = 0
	w.bytes = 0
	for key, values := range w.header {
		w.header[key] = values[:0]
	}
}

type benchmarkWriterCoreFixture struct {
	sink   *benchmarkResponseWriter
	state  *responseRuntimeState
	writer *GoxWriter
}

func newBenchmarkWriterCoreFixture(contentType string) *benchmarkWriterCoreFixture {
	return newBenchmarkWriterCoreCompressionFixture(contentType, noCompressionConfig(), "")
}

func newBenchmarkWriterCoreCompressionFixture(contentType string, cfg server.CompressionConfig, acceptEncoding string) *benchmarkWriterCoreFixture {
	sink := newBenchmarkResponseWriter()
	response := gox.NewResponse()
	if contentType != "" {
		response.Headers.Set("Content-Type", contentType)
	}
	state := &responseRuntimeState{
		response: response,
		writer:   sink,
	}
	req := &http.Request{
		Method: http.MethodGet,
		Header: make(http.Header),
	}
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	return &benchmarkWriterCoreFixture{
		sink:   sink,
		state:  state,
		writer: newGoxWriter(state, req, cfg, false, false),
	}
}

func BenchmarkGoxWriterLifecycleCoreZstd3KB(b *testing.B) {
	benchmarkGoxWriterLifecycleCoreCompression3KB(b, "zstd", zstdCompressionConfig())
}

func BenchmarkGoxWriterLifecycleCoreGzip3KB(b *testing.B) {
	benchmarkGoxWriterLifecycleCoreCompression3KB(b, "gzip", gzipCompressionConfig())
}

func BenchmarkGoxWriterLifecycleCoreBrotli3KB(b *testing.B) {
	benchmarkGoxWriterLifecycleCoreCompression3KB(b, "br", brotliCompressionConfig())
}

func BenchmarkGoxWriterLifecycleCoreDeflate3KB(b *testing.B) {
	benchmarkGoxWriterLifecycleCoreCompression3KB(b, "deflate", deflateCompressionConfig())
}

func benchmarkGoxWriterLifecycleCoreCompression3KB(b *testing.B, encoding string, cfg server.CompressionConfig) {
	b.Helper()
	html := `<html><head></head><body>` + strings.Repeat("compressible html payload ", 115) + `</body></html>`
	if err := prepareDynamicCompression(cfg); err != nil {
		b.Fatal(err)
	}
	fixture := newBenchmarkWriterCoreCompressionFixture("text/html; charset=utf-8", cfg, encoding)
	defer fixture.release()

	b.ReportAllocs()
	b.SetBytes(int64(len(html)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fixture.reset(html)
		if err := fixture.writer.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDynamicZstdLegacy3KB(b *testing.B) {
	benchmarkDynamicCompressionLegacy3KB(b, "zstd", 1)
}

func BenchmarkDynamicGzipLegacy3KB(b *testing.B) {
	benchmarkDynamicCompressionLegacy3KB(b, "gzip", 1)
}

func BenchmarkDynamicBrotliLegacy3KB(b *testing.B) {
	benchmarkDynamicCompressionLegacy3KB(b, "br", 4)
}

func BenchmarkDynamicDeflateLegacy3KB(b *testing.B) {
	benchmarkDynamicCompressionLegacy3KB(b, "deflate", 1)
}

func benchmarkDynamicCompressionLegacy3KB(b *testing.B, encoding string, level int) {
	b.Helper()
	payload := []byte(`<html><head></head><body>` + strings.Repeat("compressible html payload ", 115) + `</body></html>`)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var dst bytes.Buffer
		if err := compressStatic(encoding, &dst, bytes.NewReader(payload), level); err != nil {
			b.Fatal(err)
		}
		dynamicCompressionBenchmarkBytes = dst.Len()
	}
}

func BenchmarkDynamicZstdEncodeAll3KB(b *testing.B) {
	benchmarkDynamicCompressionPooled3KB(b, "zstd", 1, zstdCompressionConfig())
}

func BenchmarkDynamicGzipPooled3KB(b *testing.B) {
	benchmarkDynamicCompressionPooled3KB(b, "gzip", 1, gzipCompressionConfig())
}

func BenchmarkDynamicBrotliPooled3KB(b *testing.B) {
	benchmarkDynamicCompressionPooled3KB(b, "br", 4, brotliCompressionConfig())
}

func BenchmarkDynamicDeflatePooled3KB(b *testing.B) {
	benchmarkDynamicCompressionPooled3KB(b, "deflate", 1, deflateCompressionConfig())
}

func benchmarkDynamicCompressionPooled3KB(b *testing.B, encoding string, level int, cfg server.CompressionConfig) {
	b.Helper()
	payload := []byte(`<html><head></head><body>` + strings.Repeat("compressible html payload ", 115) + `</body></html>`)
	if err := prepareDynamicCompression(cfg); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded, err := encodeDynamicCompression(encoding, payload, level)
		if err != nil {
			b.Fatal(err)
		}
		dynamicCompressionBenchmarkBytes = len(encoded.data)
		encoded.release()
	}
}

func BenchmarkDynamicCompressionPooledParallel3KB(b *testing.B) {
	payload := []byte(`<html><head></head><body>` + strings.Repeat("compressible html payload ", 115) + `</body></html>`)
	cases := []struct {
		name     string
		encoding string
		level    int
		cfg      server.CompressionConfig
	}{
		{name: "zstd", encoding: "zstd", level: 1, cfg: zstdCompressionConfig()},
		{name: "gzip", encoding: "gzip", level: 1, cfg: gzipCompressionConfig()},
		{name: "brotli", encoding: "br", level: 4, cfg: brotliCompressionConfig()},
		{name: "deflate", encoding: "deflate", level: 1, cfg: deflateCompressionConfig()},
	}
	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			if err := prepareDynamicCompression(tc.cfg); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					encoded, err := encodeDynamicCompression(tc.encoding, payload, tc.level)
					if err != nil {
						b.Error(err)
						return
					}
					if len(encoded.data) == 0 {
						b.Error("empty compressed response")
						encoded.release()
						return
					}
					encoded.release()
				}
			})
		})
	}
}

func (f *benchmarkWriterCoreFixture) reset(payload string) {
	f.sink.reset()
	f.state.wroteHeader = false
	f.state.bodyWritten = false
	f.state.bodyConstructed = false
	f.state.done = false
	f.state.component = nil
	f.state.location = ""
	writer := f.writer
	if writer.bufferPtr == nil {
		writer.bufferPtr = takeGoxWriterBuffer(goxWriterBuffer4K)
		writer.buffer = *writer.bufferPtr
	}
	writer.closed = false
	writer.closing = false
	writer.pos = copy(writer.buffer, payload)
	writer.stats = GoxWriterStats{
		BytesIn:    int64(len(payload)),
		BufferSize: len(writer.buffer),
	}
}

func (f *benchmarkWriterCoreFixture) release() {
	if f == nil || f.writer == nil {
		return
	}
	if f.writer.bufferPtr != nil {
		putGoxWriterBuffer(f.writer.bufferPtr)
		f.writer.bufferPtr = nil
		f.writer.buffer = nil
	}
}

// BenchmarkGoxWriterCloseCore measures the GoX writer lifecycle with a reusable
// request/response state and a response sink that does not retain the body.
func BenchmarkGoxWriterLifecycleCoreFallbackContentType(b *testing.B) {
	html := `<html><head></head><body>` + strings.Repeat("x", 4096-42) + `</body></html>`
	fixture := newBenchmarkWriterCoreFixture("")
	defer fixture.release()

	b.ReportAllocs()
	b.SetBytes(int64(len(html)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fixture.reset(html)
		if err := fixture.writer.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGoxWriterLifecycleCoreConfigured represents the normal hot route: the
// handler has already selected the response content type before Close.
func BenchmarkGoxWriterLifecycleCoreConfigured(b *testing.B) {
	html := `<html><head></head><body>` + strings.Repeat("x", 4096-42) + `</body></html>`
	fixture := newBenchmarkWriterCoreFixture("text/html; charset=utf-8")
	defer fixture.release()

	b.ReportAllocs()
	b.SetBytes(int64(len(html)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fixture.reset(html)
		if err := fixture.writer.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// benchmarkRuntimeResponseLifecycle deliberately includes httptest request and
// recorder creation. It measures the full test HTTP lifecycle, not Close alone.
func benchmarkRuntimeResponseLifecycle(b *testing.B, html string, compression bool) {
	b.Helper()
	cfg := noCompressionConfig()
	encoding := ""
	if compression {
		cfg = gzipCompressionConfig()
		encoding = "gzip"
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(html)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writer, _ := newTestGoxWriter(b, cfg, encoding, false)
		writer.WriteRawString(html)
		if err := writer.Close(); err != nil {
			b.Fatal(err)
		}
		unregisterRuntimeResponse(writer.res.response)
	}
}

func BenchmarkRuntimeResponseLifecyclePlainHTML(b *testing.B) {
	benchmarkRuntimeResponseLifecycle(b, `<html><head></head><body>`+strings.Repeat("x", 4096)+`</body></html>`, false)
}

func BenchmarkRuntimeResponseLifecycleClientIsland(b *testing.B) {
	html := `<html><head></head><body><link rel="stylesheet" href="/_gox/client/menu.css"><gox-isle data-script="/_gox/client/menu.js" data-isolate="true" data-assets="[&quot;/_gox/client/menu.css&quot;]">` + strings.Repeat("x", 4096) + `</gox-isle></body></html>`
	benchmarkRuntimeResponseLifecycle(b, html, false)
}

func BenchmarkRuntimeResponseLifecycleGzip(b *testing.B) {
	benchmarkRuntimeResponseLifecycle(b, `<html><head></head><body>`+strings.Repeat("compressible payload ", 256)+`</body></html>`, true)
}
