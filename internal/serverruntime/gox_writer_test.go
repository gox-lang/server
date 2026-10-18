package serverruntime

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/gox-lang/gox/server"
	"github.com/klauspost/compress/zstd"
)

func newTestGoxWriter(t testing.TB, cfg server.CompressionConfig, acceptEncoding string, isAction bool) (*GoxWriter, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	res, state := newRuntimeResponse(rec, nil)
	if !strings.HasPrefix(t.Name(), "Benchmark") {
		t.Cleanup(func() { unregisterRuntimeResponse(res) })
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	return newGoxWriter(state, req, cfg, isAction, false), rec
}

func noCompressionConfig() server.CompressionConfig {
	return server.CompressionConfig{Enabled: false}
}

func gzipCompressionConfig() server.CompressionConfig {
	return server.CompressionConfig{
		Enabled:       true,
		OrderPriority: []string{"gzip"},
		Gzip:          server.CompressionAlgorithmConfig{Enabled: true, Level: gzip.BestSpeed},
	}
}

func zstdCompressionConfig() server.CompressionConfig {
	return server.CompressionConfig{
		Enabled:       true,
		OrderPriority: []string{"zstd"},
		Zstd:          server.CompressionAlgorithmConfig{Enabled: true, Level: 1},
	}
}

func brotliCompressionConfig() server.CompressionConfig {
	return server.CompressionConfig{
		Enabled:       true,
		OrderPriority: []string{"br"},
		Brotli:        server.CompressionAlgorithmConfig{Enabled: true, Level: 4},
	}
}

func deflateCompressionConfig() server.CompressionConfig {
	return server.CompressionConfig{
		Enabled:       true,
		OrderPriority: []string{"deflate"},
		Deflate:       server.CompressionAlgorithmConfig{Enabled: true, Level: zlib.BestSpeed},
	}
}

func TestGoxWriterCloseCommitsHeaderOnlyActionResponse(t *testing.T) {
	writer, rec := newTestGoxWriter(t, noCompressionConfig(), "", true)
	writer.res.response.Headers.Set("Action-Response", "success")
	writer.res.response.Headers.Set("Action-Type", "file")
	writer.res.response.Headers.Set("Content-Type", "text/plain")
	writer.res.response.Headers.Set("Content-Length", "0")

	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := rec.Header().Get("Action-Response"); got != "success" {
		t.Fatalf("Action-Response = %q, want success", got)
	}
	if got := rec.Header().Get("Action-Type"); got != "file" {
		t.Fatalf("Action-Type = %q, want file", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "0" {
		t.Fatalf("Content-Length = %q, want 0", got)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body length = %d, want 0", rec.Body.Len())
	}
}

func TestGoxWriterCloseDoesNotCommitEmptyPageResponse(t *testing.T) {
	writer, rec := newTestGoxWriter(t, noCompressionConfig(), "", false)
	writer.res.response.Headers.Set("X-Gox-Test", "pending")

	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := rec.Header().Get("X-Gox-Test"); got != "" {
		t.Fatalf("X-Gox-Test = %q, want empty", got)
	}
}

func TestGoxWriterWriteRawString(t *testing.T) {
	writer, rec := newTestGoxWriter(t, noCompressionConfig(), "", false)
	writer.WriteRawString("<h1>Hello</h1>")
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := rec.Body.String(); got != "<h1>Hello</h1>" {
		t.Fatalf("body = %q", got)
	}
}

func TestGoxWriterWriteTextStringEscapesHTML(t *testing.T) {
	writer, rec := newTestGoxWriter(t, noCompressionConfig(), "", false)
	writer.WriteTextString(`<span title="a&b">'x'</span>`)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	want := `&lt;span title=&#34;a&amp;b&#34;&gt;&#39;x&#39;&lt;/span&gt;`
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestGoxWriterWriteTextScalars(t *testing.T) {
	writer, rec := newTestGoxWriter(t, noCompressionConfig(), "", false)
	writer.WriteTextInt(-42)
	writer.WriteByte(' ')
	writer.WriteTextBool(true)
	writer.WriteByte(' ')
	writer.WriteTextFloat64(3.5)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := rec.Body.String(); got != "-42 true 3.5" {
		t.Fatalf("body = %q", got)
	}
}

func TestGoxWriterCloseCompressesGzip(t *testing.T) {
	writer, rec := newTestGoxWriter(t, gzipCompressionConfig(), "gzip", false)
	writer.WriteRawString(strings.Repeat("hello world ", 200))
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("content-encoding = %q, want gzip", got)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip reader failed: %v", err)
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip read failed: %v", err)
	}
	if got, want := string(body), strings.Repeat("hello world ", 200); got != want {
		t.Fatalf("decompressed body length = %d, want %d", len(got), len(want))
	}
}

func TestGoxWriterCloseCompressesZstd(t *testing.T) {
	writer, rec := newTestGoxWriter(t, zstdCompressionConfig(), "zstd", false)
	want := strings.Repeat("hello world ", 200)
	writer.WriteRawString(want)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "zstd" {
		t.Fatalf("content-encoding = %q, want zstd", got)
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("zstd reader failed: %v", err)
	}
	defer decoder.Close()
	body, err := decoder.DecodeAll(rec.Body.Bytes(), nil)
	if err != nil {
		t.Fatalf("zstd decode failed: %v", err)
	}
	if got := string(body); got != want {
		t.Fatalf("decompressed body length = %d, want %d", len(got), len(want))
	}
}

func TestGoxWriterCloseCompressesBrotli(t *testing.T) {
	want := strings.Repeat("hello world ", 200)
	writer, rec := newTestGoxWriter(t, brotliCompressionConfig(), "br", false)
	writer.WriteRawString(want)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("content-encoding = %q, want br", got)
	}
	body, err := io.ReadAll(brotli.NewReader(rec.Body))
	if err != nil {
		t.Fatalf("brotli read failed: %v", err)
	}
	if got := string(body); got != want {
		t.Fatalf("decompressed body length = %d, want %d", len(got), len(want))
	}
}

func TestGoxWriterCloseCompressesDeflate(t *testing.T) {
	want := strings.Repeat("hello world ", 200)
	writer, rec := newTestGoxWriter(t, deflateCompressionConfig(), "deflate", false)
	writer.WriteRawString(want)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "deflate" {
		t.Fatalf("content-encoding = %q, want deflate", got)
	}
	zr, err := zlib.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("deflate reader failed: %v", err)
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("deflate read failed: %v", err)
	}
	if got := string(body); got != want {
		t.Fatalf("decompressed body length = %d, want %d", len(got), len(want))
	}
}

func TestDynamicZstdConcurrentEncoding(t *testing.T) {
	const workers = 32
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := []byte(strings.Repeat("worker-"+strconv.Itoa(worker)+" ", 300))
			encoded, encodeErr := encodeDynamicZstd(want, 1)
			if encodeErr != nil {
				errs <- encodeErr
				return
			}
			owned := append([]byte(nil), encoded.data...)
			encoded.release()
			decoded, decodeErr := decoder.DecodeAll(owned, nil)
			if decodeErr != nil {
				errs <- decodeErr
				return
			}
			if !bytes.Equal(decoded, want) {
				errs <- fmt.Errorf("worker %d decoded payload mismatch", worker)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestGoxWriterCloseSkipsCompressionForSmallPayload(t *testing.T) {
	writer, rec := newTestGoxWriter(t, gzipCompressionConfig(), "gzip", false)
	writer.WriteRawString("small")
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("content-encoding = %q, want empty", got)
	}
	if got := rec.Body.String(); got != "small" {
		t.Fatalf("body = %q", got)
	}
}

func TestGoxWriterWriteStream(t *testing.T) {
	writer, rec := newTestGoxWriter(t, noCompressionConfig(), "", false)
	writer.WriteRawString("prefix:")
	writer.WriteStream(strings.NewReader("stream-body"))
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if got := rec.Body.String(); got != "prefix:stream-body" {
		t.Fatalf("body = %q", got)
	}
}

func TestGoxWriterConfigureRouteReusesWriter(t *testing.T) {
	writer, _ := newTestGoxWriter(t, noCompressionConfig(), "", false)
	bufferPtr := writer.bufferPtr
	if err := writer.ConfigureRoute(true, false); err != nil {
		t.Fatalf("ConfigureRoute failed: %v", err)
	}
	if writer.bufferPtr != bufferPtr {
		t.Fatal("ConfigureRoute replaced the pooled buffer")
	}
	if !writer.isAction {
		t.Fatal("ConfigureRoute did not set action mode")
	}
	writer.WriteRawString("started")
	if err := writer.ConfigureRoute(false, false); err == nil {
		t.Fatal("ConfigureRoute must reject a writer after writing")
	}
}

func TestGoxWriterPoolClearsRequestState(t *testing.T) {
	writer, _ := newTestGoxWriter(t, noCompressionConfig(), "", false)
	writer.WriteRawString("first")
	if err := writer.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	releaseGoxWriter(writer)

	rec := httptest.NewRecorder()
	res, state := newRuntimeResponse(rec, nil)
	t.Cleanup(func() { unregisterRuntimeResponse(res) })
	req := httptest.NewRequest(http.MethodGet, "/second", nil)
	reused := newGoxWriter(state, req, noCompressionConfig(), false, false)
	t.Cleanup(func() {
		_ = reused.Close()
		releaseGoxWriter(reused)
	})

	if reused.closed || reused.closing || reused.pos != 0 {
		t.Fatalf("pooled writer retained lifecycle state: %#v", reused)
	}
	if reused.res != state || reused.req != req {
		t.Fatal("pooled writer was not rebound to the current request")
	}
	if reused.stats.BytesIn != 0 || reused.stats.BytesOut != 0 || reused.stats.FlushCount != 0 {
		t.Fatalf("pooled writer retained statistics: %#v", reused.stats)
	}
}
