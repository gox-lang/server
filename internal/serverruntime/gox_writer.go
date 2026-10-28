package serverruntime

import (
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

const (
	goxWriterBuffer1K = 1 * 1024
	goxWriterBuffer4K = 4 * 1024
	goxWriterBuffer8K = 8 * 1024
)

var (
	goxEscAmp   = []byte("&amp;")
	goxEscLT    = []byte("&lt;")
	goxEscGT    = []byte("&gt;")
	goxEscQuote = []byte("&#34;")
	goxEscApos  = []byte("&#39;")

	goxBoolTrue  = []byte("true")
	goxBoolFalse = []byte("false")

	goxAttrSpace  = []byte(" ")
	goxAttrEqOpen = []byte("=\"")
	goxAttrQuote  = []byte("\"")

	goxWriter1KPool = sync.Pool{
		New: func() any {
			buf := make([]byte, goxWriterBuffer1K)
			return &buf
		},
	}
	goxWriter4KPool = sync.Pool{
		New: func() any {
			buf := make([]byte, goxWriterBuffer4K)
			return &buf
		},
	}
	goxWriter8KPool = sync.Pool{
		New: func() any {
			buf := make([]byte, goxWriterBuffer8K)
			return &buf
		},
	}
	goxWriterPool = sync.Pool{
		New: func() any {
			return new(GoxWriter)
		},
	}
)

var _ gox.Writer = (*GoxWriter)(nil)

type GoxWriterStats struct {
	BytesIn      int64
	BytesOut     int64
	FlushCount   int64
	Compressed   bool
	Encoding     string
	StartedAt    time.Time
	FirstWriteAt time.Time
	LastFlushAt  time.Time
	ClosedAt     time.Time
	BufferSize   int

	CloseFlushBufferDuration   time.Duration
	CloseResponseWriteDuration time.Duration
	ClosePoolPutDuration       time.Duration
	CompressionDuration        time.Duration
}

type GoxWriter struct {
	res         *responseRuntimeState
	req         *http.Request
	compression server.CompressionConfig
	isAction    bool
	traceDeep   bool

	buffer    []byte
	bufferPtr *[]byte
	pos       int
	scratch   [256]byte
	closed    bool
	closing   bool
	writeErr  error

	stats    GoxWriterStats
	pageHead *goxPageHead
}

func newGoxWriter(res *responseRuntimeState, req *http.Request, compression server.CompressionConfig, isAction bool, traceDeep bool) *GoxWriter {
	bufferPtr := takeGoxWriterBuffer(selectGoxWriterBufferSize(isAction))
	buffer := *bufferPtr
	w := goxWriterPool.Get().(*GoxWriter)
	*w = GoxWriter{
		res:         res,
		req:         req,
		compression: compression,
		isAction:    isAction,
		traceDeep:   traceDeep,
		buffer:      buffer,
		bufferPtr:   bufferPtr,
		stats:       GoxWriterStats{BufferSize: len(buffer)},
	}
	if traceDeep {
		w.stats.StartedAt = time.Now()
	}
	return w
}

func releaseGoxWriter(w *GoxWriter) {
	if w == nil {
		return
	}
	if w.bufferPtr != nil {
		putGoxWriterBuffer(w.bufferPtr)
	}
	*w = GoxWriter{}
	goxWriterPool.Put(w)
}

func (w *GoxWriter) ConfigureRoute(isAction bool, traceDeep bool) error {
	if w == nil {
		return newResponseRuntimeError("response-writer-nil", "response writer is nil")
	}
	if w.closed || w.pos != 0 || (w.res != nil && w.res.bodyConstructed) {
		return newResponseRuntimeError("response-writer-started", "response writer cannot be reconfigured after writing")
	}
	w.isAction = isAction
	w.traceDeep = traceDeep
	if traceDeep && w.stats.StartedAt.IsZero() {
		w.stats.StartedAt = time.Now()
	}
	return nil
}

func selectGoxWriterBufferSize(isAction bool) int {
	if isAction {
		return goxWriterBuffer1K
	}
	return goxWriterBuffer4K
}

func takeGoxWriterBuffer(size int) *[]byte {
	switch size {
	case goxWriterBuffer1K:
		return goxWriter1KPool.Get().(*[]byte)
	case goxWriterBuffer8K:
		return goxWriter8KPool.Get().(*[]byte)
	default:
		return goxWriter4KPool.Get().(*[]byte)
	}
}

func putGoxWriterBuffer(bufferPtr *[]byte) {
	if bufferPtr == nil {
		return
	}
	buffer := *bufferPtr
	switch len(buffer) {
	case goxWriterBuffer1K:
		goxWriter1KPool.Put(bufferPtr)
	case goxWriterBuffer8K:
		goxWriter8KPool.Put(bufferPtr)
	default:
		goxWriter4KPool.Put(bufferPtr)
	}
}

func (w *GoxWriter) Write(p []byte) (int, error) {
	if w == nil {
		return 0, newResponseRuntimeError("response-writer-nil", "response writer is nil")
	}
	if w.closed {
		return 0, newResponseRuntimeError("response-writer-closed", "response writer is closed")
	}
	if len(p) == 0 {
		return 0, nil
	}
	w.markWrite(len(p))

	if len(p) > len(w.buffer) {
		if err := w.flushBuffer(false); err != nil {
			return 0, err
		}
		n, err := w.writeRaw(p)
		if err != nil {
			return n, err
		}
		return len(p), nil
	}

	if w.pos+len(p) > len(w.buffer) {
		if err := w.flushBuffer(false); err != nil {
			return 0, err
		}
		w.maybePromoteBuffer()
	}

	copy(w.buffer[w.pos:], p)
	w.pos += len(p)

	return len(p), nil
}

func (w *GoxWriter) WriteTextString(value string) { w.writeEscapedString(value) }
func (w *GoxWriter) WriteRawString(value string)  { w.writeRawString(value) }
func (w *GoxWriter) WriteTextBytes(value []byte)  { w.writeEscapedBytes(value) }
func (w *GoxWriter) WriteRawBytes(value []byte)   { _, _ = w.Write(value) }
func (w *GoxWriter) WriteError(value error) {
	if value == nil {
		return
	}
	w.WriteTextString(value.Error())
}
func (w *GoxWriter) WriteBytes(value []byte) {
	if len(value) == 0 {
		return
	}
	_, _ = w.Write(value)
}
func (w *GoxWriter) WriteByte(value byte) error {
	if w == nil || w.closed {
		return io.ErrClosedPipe
	}

	w.markWrite(1)

	if w.pos >= len(w.buffer) {
		if err := w.flushBuffer(false); err != nil {
			return err
		}
		w.maybePromoteBuffer()
	}

	w.buffer[w.pos] = value
	w.pos++
	return nil
}
func (w *GoxWriter) WriteStream(value io.Reader) {
	if value == nil || w == nil || w.closed {
		return
	}
	if err := w.flushBuffer(false); err != nil {
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, readErr := value.Read(buf)
		if n > 0 {
			w.markWrite(n)
			_, writeErr := w.writeRaw(buf[:n])
			if writeErr != nil {
				return
			}
			w.stats.FlushCount++
			if w.traceDeep {
				w.stats.LastFlushAt = time.Now()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func (w *GoxWriter) WriteTextBool(value bool) {
	if value {
		_, _ = w.Write(goxBoolTrue)
		return
	}
	_, _ = w.Write(goxBoolFalse)
}
func (w *GoxWriter) WriteRawBool(value bool) { w.WriteTextBool(value) }

func (w *GoxWriter) WriteTextInt(value int)     { w.writeInt64(int64(value)) }
func (w *GoxWriter) WriteRawInt(value int)      { w.WriteTextInt(value) }
func (w *GoxWriter) WriteTextInt8(value int8)   { w.writeInt64(int64(value)) }
func (w *GoxWriter) WriteRawInt8(value int8)    { w.WriteTextInt8(value) }
func (w *GoxWriter) WriteTextInt16(value int16) { w.writeInt64(int64(value)) }
func (w *GoxWriter) WriteRawInt16(value int16)  { w.WriteTextInt16(value) }
func (w *GoxWriter) WriteTextInt32(value int32) { w.writeInt64(int64(value)) }
func (w *GoxWriter) WriteRawInt32(value int32)  { w.WriteTextInt32(value) }
func (w *GoxWriter) WriteTextInt64(value int64) { w.writeInt64(value) }
func (w *GoxWriter) WriteRawInt64(value int64)  { w.WriteTextInt64(value) }

func (w *GoxWriter) WriteTextUint(value uint)       { w.writeUint64(uint64(value)) }
func (w *GoxWriter) WriteRawUint(value uint)        { w.WriteTextUint(value) }
func (w *GoxWriter) WriteTextUint8(value uint8)     { w.writeUint64(uint64(value)) }
func (w *GoxWriter) WriteRawUint8(value uint8)      { w.WriteTextUint8(value) }
func (w *GoxWriter) WriteTextUint16(value uint16)   { w.writeUint64(uint64(value)) }
func (w *GoxWriter) WriteRawUint16(value uint16)    { w.WriteTextUint16(value) }
func (w *GoxWriter) WriteTextUint32(value uint32)   { w.writeUint64(uint64(value)) }
func (w *GoxWriter) WriteRawUint32(value uint32)    { w.WriteTextUint32(value) }
func (w *GoxWriter) WriteTextUint64(value uint64)   { w.writeUint64(value) }
func (w *GoxWriter) WriteRawUint64(value uint64)    { w.WriteTextUint64(value) }
func (w *GoxWriter) WriteTextUintptr(value uintptr) { w.writeUint64(uint64(value)) }
func (w *GoxWriter) WriteRawUintptr(value uintptr)  { w.WriteTextUintptr(value) }

func (w *GoxWriter) WriteTextFloat32(value float32) { w.writeFloat(float64(value), 32) }
func (w *GoxWriter) WriteRawFloat32(value float32)  { w.WriteTextFloat32(value) }
func (w *GoxWriter) WriteTextFloat64(value float64) { w.writeFloat(value, 64) }
func (w *GoxWriter) WriteRawFloat64(value float64)  { w.WriteTextFloat64(value) }

func (w *GoxWriter) WriteTextBigInt(value *big.Int) {
	if value != nil {
		w.WriteRawString(value.String())
	}
}
func (w *GoxWriter) WriteRawBigInt(value *big.Int)      { w.WriteTextBigInt(value) }
func (w *GoxWriter) WriteTextBigIntValue(value big.Int) { w.WriteRawString(value.String()) }
func (w *GoxWriter) WriteRawBigIntValue(value big.Int)  { w.WriteTextBigIntValue(value) }
func (w *GoxWriter) WriteTextBigFloat(value *big.Float) {
	if value != nil {
		w.WriteRawString(value.Text('g', -1))
	}
}
func (w *GoxWriter) WriteRawBigFloat(value *big.Float)      { w.WriteTextBigFloat(value) }
func (w *GoxWriter) WriteTextBigFloatValue(value big.Float) { w.WriteRawString(value.Text('g', -1)) }
func (w *GoxWriter) WriteRawBigFloatValue(value big.Float)  { w.WriteTextBigFloatValue(value) }
func (w *GoxWriter) WriteTextBigRat(value *big.Rat) {
	if value != nil {
		w.WriteRawString(value.RatString())
	}
}
func (w *GoxWriter) WriteRawBigRat(value *big.Rat)      { w.WriteTextBigRat(value) }
func (w *GoxWriter) WriteTextBigRatValue(value big.Rat) { w.WriteRawString(value.RatString()) }
func (w *GoxWriter) WriteRawBigRatValue(value big.Rat)  { w.WriteTextBigRatValue(value) }

func (w *GoxWriter) WriteTextAny(value any) { w.writeAny(value, true) }
func (w *GoxWriter) WriteRawAny(value any)  { w.writeAny(value, false) }

func (w *GoxWriter) WriteSpreadAttrs(attrs map[string]string) {
	for k, v := range attrs {
		_, _ = w.Write(goxAttrSpace)
		w.WriteTextString(k)
		_, _ = w.Write(goxAttrEqOpen)
		w.WriteTextString(v)
		_, _ = w.Write(goxAttrQuote)
	}
}

func (w *GoxWriter) Flush() error {
	return w.flushBuffer(true)
}

func (w *GoxWriter) DiscardBuffered() {
	if w == nil || w.closed {
		return
	}
	w.pos = 0
	if w.res != nil && !w.res.bodyWritten {
		w.res.bodyConstructed = false
	}
}

func (w *GoxWriter) Close() error {
	if w == nil || w.closed {
		return nil
	}

	defer func() {
		w.closing = false

		w.closed = true
		if w.traceDeep {
			w.stats.ClosedAt = time.Now()
		}

		if w.traceDeep {
			stageStart := time.Now()
			putGoxWriterBuffer(w.bufferPtr)
			w.stats.ClosePoolPutDuration += time.Since(stageStart)
		} else {
			putGoxWriterBuffer(w.bufferPtr)
		}

		w.bufferPtr = nil
		w.buffer = nil
		w.pos = 0
	}()
	w.closing = true
	var err error
	if w.traceDeep {
		stageStart := time.Now()
		err = w.closeBuffer()
		w.stats.CloseFlushBufferDuration += time.Since(stageStart)
	} else {
		err = w.closeBuffer()
	}

	return err
}

func (w *GoxWriter) closeBuffer() error {
	if w != nil && w.writeErr != nil {
		return w.writeErr
	}
	if w == nil {
		return nil
	}
	if w.pos == 0 {
		// An Action may intentionally return an empty body, for example a
		// zero-byte file. In that case no Write call reaches the runtime state,
		// so its response headers would otherwise never be committed.
		if w.isAction && w.res != nil && !w.res.WroteHeader() {
			actionResponse, _ := w.res.response.Headers.Get("Action-Response")
			if actionResponse != "" {
				w.res.WriteHeader(w.res.Status())
			}
		}
		return nil
	}

	// Hot path for small dynamic responses. Avoid compression negotiation and
	// extra branching for the common case: a small HTML/JSON payload that fits
	// in the current buffer.
	if w.stats.BytesIn < int64(goxWriterBuffer1K) || !w.compression.Enabled {
		return w.closeRawFast()
	}

	if encoding := w.selectCloseCompressionEncoding(); encoding != staticIdentityEncoding {
		return w.closeCompressedBuffer(encoding)
	}
	return w.closeRawFast()
}

func (w *GoxWriter) closeRawFast() error {
	if w == nil || w.pos == 0 {
		return nil
	}
	data := w.buffer[:w.pos]
	w.pos = 0
	w.ensureDefaultContentType()
	_, err := w.writeRaw(data)
	if err != nil {
		return err
	}
	w.stats.FlushCount++
	if w.traceDeep {
		w.stats.LastFlushAt = time.Now()
	}
	return nil
}

func (w *GoxWriter) selectCloseCompressionEncoding() string {
	if w == nil || w.req == nil || w.res == nil {
		return staticIdentityEncoding
	}
	if w.stats.FlushCount > 0 {
		return staticIdentityEncoding
	}
	if w.stats.BytesIn < int64(goxWriterBuffer1K) {
		return staticIdentityEncoding
	}
	if w.res.WroteHeader() {
		return staticIdentityEncoding
	}
	if w.req.Method == http.MethodHead {
		return staticIdentityEncoding
	}
	status := w.res.Status()
	if status < 200 || status == http.StatusNoContent || status == http.StatusNotModified {
		return staticIdentityEncoding
	}
	contentEncoding, _ := w.res.response.Headers.Get("Content-Encoding")
	contentLength, _ := w.res.response.Headers.Get("Content-Length")
	if contentEncoding != "" || contentLength != "" {
		return staticIdentityEncoding
	}
	return selectStaticEncoding(w.compression, w.req.Header.Get("Accept-Encoding"))
}

func (w *GoxWriter) closeCompressedBuffer(encoding string) error {
	data := w.buffer[:w.pos]
	w.pos = 0

	level := staticCompressionLevel(w.compression, encoding)
	var stageStart time.Time
	if w.traceDeep {
		stageStart = time.Now()
	}

	compressed, err := encodeDynamicCompression(encoding, data, level)
	if !stageStart.IsZero() {
		w.stats.CompressionDuration += time.Since(stageStart)
	}
	defer compressed.release()
	if err != nil {
		w.ensureDefaultContentType()
		_, rawErr := w.writeRaw(data)
		if rawErr != nil {
			return rawErr
		}
		w.stats.FlushCount++
		w.stats.LastFlushAt = time.Now()
		return err
	}
	if len(compressed.data) >= len(data) {
		w.ensureDefaultContentType()
		_, err = w.writeRaw(data)
		if err != nil {
			return err
		}
		w.stats.FlushCount++
		if w.traceDeep {
			w.stats.LastFlushAt = time.Now()
		}
		return nil
	}

	w.ensureDefaultContentType()
	w.setHTTPHeader("Content-Encoding", encoding)
	w.addHTTPVaryAcceptEncoding()

	_, err = w.writeRaw(compressed.data)
	if err != nil {
		return err
	}

	w.stats.FlushCount++
	if w.traceDeep {
		w.stats.LastFlushAt = time.Now()
	}
	w.stats.Compressed = true
	w.stats.Encoding = encoding
	return nil
}

func (w *GoxWriter) ensureDefaultContentType() {
	if w == nil || w.res == nil {
		return
	}
	contentType, _ := w.res.response.Headers.Get("Content-Type")
	if contentType != "" {
		return
	}
	if w.res.writer != nil && w.res.writer.Header().Get("Content-Type") != "" {
		return
	}
	w.setHTTPHeader("Content-Type", "text/plain; charset=utf-8")
}

func (w *GoxWriter) setHTTPHeader(key string, value string) {
	if w == nil || w.res == nil || w.res.writer == nil || key == "" {
		return
	}
	setHTTPHeaderValue(w.res.writer.Header(), key, value)
}

func setHTTPHeaderValue(header http.Header, key string, value string) {
	values := header[key]
	header[key] = append(values[:0], value)
}

func (w *GoxWriter) addHTTPVaryAcceptEncoding() {
	if w == nil || w.res == nil || w.res.writer == nil {
		return
	}
	current := w.res.writer.Header().Get("Vary")
	if current == "" {
		current, _ = w.res.response.Headers.Get("Vary")
	}
	if current == "" {
		setHTTPHeaderValue(w.res.writer.Header(), "Vary", "Accept-Encoding")
		return
	}
	if headerListContains(current, "Accept-Encoding") {
		return
	}
	setHTTPHeaderValue(w.res.writer.Header(), "Vary", current+", Accept-Encoding")
}

func headerListContains(value string, target string) bool {
	for len(value) > 0 {
		comma := strings.IndexByte(value, ',')
		part := value
		if comma >= 0 {
			part = value[:comma]
			value = value[comma+1:]
		} else {
			value = ""
		}
		if strings.EqualFold(strings.TrimSpace(part), target) {
			return true
		}
	}
	return false
}

func addVaryAcceptEncoding(res *responseRuntimeState) {
	if res == nil {
		return
	}
	current, _ := res.response.Headers.Get("Vary")
	if current == "" {
		res.response.Headers.Set("Vary", "Accept-Encoding")
		return
	}
	for _, part := range strings.Split(current, ",") {
		if strings.EqualFold(strings.TrimSpace(part), "Accept-Encoding") {
			return
		}
	}
	res.response.Headers.Set("Vary", current+", Accept-Encoding")
}

func (w *GoxWriter) flushBuffer(flushHTTP bool) error {
	if w == nil || w.pos == 0 {
		return nil
	}
	if w.closed {
		return newResponseRuntimeError("response-writer-closed", "response writer is closed")
	}

	data := w.buffer[:w.pos]
	w.pos = 0

	_, err := w.writeRaw(data)
	if err != nil {
		return err
	}

	w.stats.FlushCount++
	if w.traceDeep {
		w.stats.LastFlushAt = time.Now()
	}

	if flushHTTP {
		if flusher, ok := w.res.writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	return nil
}

func (w *GoxWriter) maybePromoteBuffer() {
	if w == nil || w.bufferPtr == nil || w.pos != 0 {
		return
	}

	current := len(w.buffer)
	next := 0
	switch {
	case current == goxWriterBuffer1K && w.stats.FlushCount >= 2:
		next = goxWriterBuffer4K
	case current == goxWriterBuffer4K && w.stats.FlushCount >= 4:
		next = goxWriterBuffer8K
	}
	if next == 0 {
		return
	}

	oldPtr := w.bufferPtr
	newPtr := takeGoxWriterBuffer(next)
	w.bufferPtr = newPtr
	w.buffer = *newPtr
	w.stats.BufferSize = len(w.buffer)
	putGoxWriterBuffer(oldPtr)
}

func (w *GoxWriter) BytesIn() int64 {
	if w == nil {
		return 0
	}
	return w.stats.BytesIn
}

func (w *GoxWriter) BytesOut() int64 {
	if w == nil {
		return 0
	}
	return w.stats.BytesOut
}

func (w *GoxWriter) BufferedBytes() int {
	if w == nil {
		return 0
	}
	return w.pos
}

func (w *GoxWriter) Stats() GoxWriterStats {
	if w == nil {
		return GoxWriterStats{}
	}
	stats := w.stats
	stats.BufferSize = len(w.buffer)
	return stats
}

func (w *GoxWriter) writeRaw(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if w.res == nil || w.res.writer == nil {
		return 0, newResponseRuntimeError("response-runtime-missing", "response runtime is missing")
	}
	if !w.res.WroteHeader() {
		w.res.WriteHeader(w.res.Status())
	}

	var stageStart time.Time
	if w.traceDeep && w.closing {
		stageStart = time.Now()
	}
	n, err := w.res.writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.writeErr = err
	}
	w.res.MarkBodyWritten(n)
	if !stageStart.IsZero() {
		w.stats.CloseResponseWriteDuration += time.Since(stageStart)
	}
	w.stats.BytesOut += int64(n)
	return n, err
}

func (w *GoxWriter) markWrite(n int) {
	if w.traceDeep && w.stats.FirstWriteAt.IsZero() {
		w.stats.FirstWriteAt = time.Now()
	}
	if w.res != nil {
		w.res.MarkBodyConstructed(n)
	}
	w.stats.BytesIn += int64(n)
}

func (w *GoxWriter) writeBufferedBytes(p []byte) {
	_, _ = w.Write(p)
}

func (w *GoxWriter) writeBufferedString(value string) {
	if w == nil || value == "" || w.closed {
		return
	}
	w.markWrite(len(value))

	for len(value) > 0 {
		available := len(w.buffer) - w.pos
		if available == 0 {
			if err := w.flushBuffer(false); err != nil {
				return
			}
			w.maybePromoteBuffer()
			available = len(w.buffer) - w.pos
		}

		if len(value) <= available {
			w.pos += copy(w.buffer[w.pos:], value)
			return
		}

		w.pos += copy(w.buffer[w.pos:], value[:available])
		value = value[available:]
		if err := w.flushBuffer(false); err != nil {
			return
		}
		w.maybePromoteBuffer()
	}
}

func (w *GoxWriter) writeInt64(value int64) {
	buf := strconv.AppendInt(w.scratch[:0], value, 10)
	_, _ = w.Write(buf)
}

func (w *GoxWriter) writeUint64(value uint64) {
	buf := strconv.AppendUint(w.scratch[:0], value, 10)
	_, _ = w.Write(buf)
}

func (w *GoxWriter) writeFloat(value float64, bitSize int) {
	buf := strconv.AppendFloat(w.scratch[:0], value, 'f', -1, bitSize)
	_, _ = w.Write(buf)
}

func (w *GoxWriter) writeRawString(value string) {
	w.writeBufferedString(value)
}

func (w *GoxWriter) writeEscapedString(value string) {
	if value == "" {
		return
	}

	start := 0
	for i := 0; i < len(value); i++ {
		var esc []byte
		switch value[i] {
		case '&':
			esc = goxEscAmp
		case '<':
			esc = goxEscLT
		case '>':
			esc = goxEscGT
		case '"':
			esc = goxEscQuote
		case '\'':
			esc = goxEscApos
		default:
			continue
		}

		if start < i {
			w.writeBufferedString(value[start:i])
		}
		_, _ = w.Write(esc)
		start = i + 1
	}

	if start == 0 {
		w.writeBufferedString(value)
		return
	}
	if start < len(value) {
		w.writeBufferedString(value[start:])
	}
}

func (w *GoxWriter) writeEscapedBytes(value []byte) {
	if len(value) == 0 {
		return
	}

	start := 0
	for i, b := range value {
		var esc []byte
		switch b {
		case '&':
			esc = goxEscAmp
		case '<':
			esc = goxEscLT
		case '>':
			esc = goxEscGT
		case '"':
			esc = goxEscQuote
		case '\'':
			esc = goxEscApos
		default:
			continue
		}

		if start < i {
			_, _ = w.Write(value[start:i])
		}
		_, _ = w.Write(esc)
		start = i + 1
	}

	if start == 0 {
		_, _ = w.Write(value)
		return
	}
	if start < len(value) {
		_, _ = w.Write(value[start:])
	}
}

func (w *GoxWriter) writeAny(value any, escape bool) {
	switch v := value.(type) {
	case string:
		if escape {
			w.WriteTextString(v)
		} else {
			w.WriteRawString(v)
		}
	case []byte:
		if escape {
			w.WriteTextBytes(v)
		} else {
			w.WriteRawBytes(v)
		}
	case bool:
		w.WriteTextBool(v)
	case int:
		w.WriteTextInt(v)
	case int8:
		w.WriteTextInt8(v)
	case int16:
		w.WriteTextInt16(v)
	case int32:
		w.WriteTextInt32(v)
	case int64:
		w.WriteTextInt64(v)
	case uint:
		w.WriteTextUint(v)
	case uint8:
		w.WriteTextUint8(v)
	case uint16:
		w.WriteTextUint16(v)
	case uint32:
		w.WriteTextUint32(v)
	case uint64:
		w.WriteTextUint64(v)
	case uintptr:
		w.WriteTextUintptr(v)
	case float32:
		w.WriteTextFloat32(v)
	case float64:
		w.WriteTextFloat64(v)
	case *big.Int:
		w.WriteTextBigInt(v)
	case big.Int:
		w.WriteTextBigIntValue(v)
	case *big.Float:
		w.WriteTextBigFloat(v)
	case big.Float:
		w.WriteTextBigFloatValue(v)
	case *big.Rat:
		w.WriteTextBigRat(v)
	case big.Rat:
		w.WriteTextBigRatValue(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			if escape {
				w.WriteTextString("[Object]")
			} else {
				w.WriteRawString("[Object]")
			}
			return
		}
		if escape {
			w.WriteTextBytes(b)
		} else {
			w.WriteRawBytes(b)
		}
	}
}

// goxPageHead carries request-local metadata from a Page into shared components.
// It is not part of the SDK Writer contract or the cluster protocol.
type goxPageHead struct {
	actions []byte
	entries [][2]string
	script  []byte
	hash    string
}

func (w *GoxWriter) GoxPreparePageHead(actions []byte, entries [][2]string, script []byte, hash string) {
	if w.isAction || w.res == nil || !w.res.response.Headers.Contains("Content-Type", "text/html") {
		return
	}
	w.pageHead = &goxPageHead{actions: actions, entries: entries, script: script, hash: hash}
}

func (w *GoxWriter) GoxWritePageHead() {
	head := w.pageHead
	if head == nil {
		return
	}
	w.pageHead = nil
	if len(head.actions) > 0 {
		_, _ = w.Write(head.actions)
	}
	w.res.response.ImportMap.Runtime().WriteCompiled(w, head.entries, head.script, head.hash)
}
