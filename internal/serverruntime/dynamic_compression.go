package serverruntime

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"runtime"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/gox-lang/gox/server"
	"github.com/klauspost/compress/zstd"
)

const dynamicEncoderPoolLimit = 4

type dynamicCompressedBuffer struct {
	data       []byte
	slice      *[]byte
	byteBuffer *bytes.Buffer
}

func (b *dynamicCompressedBuffer) release() {
	if b == nil {
		return
	}
	if b.slice != nil {
		putGoxWriterBuffer(b.slice)
	}
	if b.byteBuffer != nil {
		putDynamicByteBuffer(b.byteBuffer)
	}
	*b = dynamicCompressedBuffer{}
}

var dynamicByteBufferPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, goxWriterBuffer8K))
	},
}

func takeDynamicByteBuffer() *bytes.Buffer {
	buffer := dynamicByteBufferPool.Get().(*bytes.Buffer)
	buffer.Reset()
	return buffer
}

func putDynamicByteBuffer(buffer *bytes.Buffer) {
	if buffer == nil || buffer.Cap() > 64*1024 {
		return
	}
	buffer.Reset()
	dynamicByteBufferPool.Put(buffer)
}

type dynamicZstdPool struct {
	encoders chan *zstd.Encoder
}

var (
	dynamicZstdPools           sync.Map
	dynamicPoolCreateMu        sync.Mutex
	dynamicCompressionWarmData [goxWriterBuffer4K]byte
	dynamicGzipPools           sync.Map
	dynamicZlibPools           sync.Map
	dynamicBrotliPools         sync.Map
)

func dynamicPoolSize() int {
	size := runtime.GOMAXPROCS(0)
	if size < 1 {
		return 1
	}
	if size > dynamicEncoderPoolLimit {
		return dynamicEncoderPoolLimit
	}
	return size
}

func prepareDynamicCompression(cfg server.CompressionConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Zstd.Enabled {
		if _, err := dynamicZstdPoolForLevel(cfg.Zstd.Level); err != nil {
			return err
		}
	}
	if cfg.Gzip.Enabled {
		if _, err := dynamicGzipPoolForLevel(cfg.Gzip.Level); err != nil {
			return err
		}
	}
	if cfg.Deflate.Enabled {
		if _, err := dynamicZlibPoolForLevel(cfg.Deflate.Level); err != nil {
			return err
		}
	}
	if cfg.Brotli.Enabled {
		dynamicBrotliPoolForLevel(cfg.Brotli.Level)
	}
	return nil
}

func normalizedZstdLevel(level int) zstd.EncoderLevel {
	if level <= 0 {
		return zstd.SpeedDefault
	}
	return zstd.EncoderLevelFromZstd(level)
}

func dynamicZstdPoolForLevel(level int) (*dynamicZstdPool, error) {
	encoderLevel := normalizedZstdLevel(level)
	if value, ok := dynamicZstdPools.Load(encoderLevel); ok {
		return value.(*dynamicZstdPool), nil
	}
	dynamicPoolCreateMu.Lock()
	defer dynamicPoolCreateMu.Unlock()
	if value, ok := dynamicZstdPools.Load(encoderLevel); ok {
		return value.(*dynamicZstdPool), nil
	}
	pool := &dynamicZstdPool{encoders: make(chan *zstd.Encoder, dynamicPoolSize())}
	for i := 0; i < cap(pool.encoders); i++ {
		encoder, err := zstd.NewWriter(nil,
			zstd.WithEncoderLevel(encoderLevel),
			zstd.WithEncoderConcurrency(1),
		)
		if err != nil {
			return nil, err
		}
		warmDst := make([]byte, 0, encoder.MaxEncodedSize(len(dynamicCompressionWarmData)))
		_ = encoder.EncodeAll(dynamicCompressionWarmData[:], warmDst)
		pool.encoders <- encoder
	}
	dynamicZstdPools.Store(encoderLevel, pool)
	return pool, nil
}

func encodeDynamicZstd(data []byte, level int) (dynamicCompressedBuffer, error) {
	pool, err := dynamicZstdPoolForLevel(level)
	if err != nil {
		return dynamicCompressedBuffer{}, err
	}
	encoder := <-pool.encoders
	defer func() { pool.encoders <- encoder }()
	bufferPtr := takeGoxWriterBuffer(goxWriterBuffer8K)
	buffer := *bufferPtr
	if encoder.MaxEncodedSize(len(data)) > cap(buffer) {
		putGoxWriterBuffer(bufferPtr)
		encoded := encoder.EncodeAll(data, make([]byte, 0, encoder.MaxEncodedSize(len(data))))
		return dynamicCompressedBuffer{data: encoded}, nil
	}
	encoded := encoder.EncodeAll(data, buffer[:0])
	return dynamicCompressedBuffer{data: encoded, slice: bufferPtr}, nil
}

type dynamicGzipPool struct{ writers chan *gzip.Writer }

func normalizedGzipLevel(level int) int {
	if level <= 0 {
		return gzip.DefaultCompression
	}
	return level
}

func dynamicGzipPoolForLevel(level int) (*dynamicGzipPool, error) {
	level = normalizedGzipLevel(level)
	if value, ok := dynamicGzipPools.Load(level); ok {
		return value.(*dynamicGzipPool), nil
	}
	dynamicPoolCreateMu.Lock()
	defer dynamicPoolCreateMu.Unlock()
	if value, ok := dynamicGzipPools.Load(level); ok {
		return value.(*dynamicGzipPool), nil
	}
	pool := &dynamicGzipPool{writers: make(chan *gzip.Writer, dynamicPoolSize())}
	for i := 0; i < cap(pool.writers); i++ {
		writer, err := gzip.NewWriterLevel(io.Discard, level)
		if err != nil {
			return nil, err
		}
		_, _ = writer.Write(dynamicCompressionWarmData[:])
		if err := writer.Close(); err != nil {
			return nil, err
		}
		pool.writers <- writer
	}
	dynamicGzipPools.Store(level, pool)
	return pool, nil
}

func encodeDynamicGzip(data []byte, level int) (dynamicCompressedBuffer, error) {
	pool, err := dynamicGzipPoolForLevel(level)
	if err != nil {
		return dynamicCompressedBuffer{}, err
	}
	buffer := takeDynamicByteBuffer()
	writer := <-pool.writers
	writer.Reset(buffer)
	_, writeErr := writer.Write(data)
	closeErr := writer.Close()
	pool.writers <- writer
	if writeErr != nil {
		putDynamicByteBuffer(buffer)
		return dynamicCompressedBuffer{}, writeErr
	}
	if closeErr != nil {
		putDynamicByteBuffer(buffer)
		return dynamicCompressedBuffer{}, closeErr
	}
	return dynamicCompressedBuffer{data: buffer.Bytes(), byteBuffer: buffer}, nil
}

type dynamicZlibPool struct{ writers chan *zlib.Writer }

func normalizedZlibLevel(level int) int {
	if level <= 0 {
		return zlib.DefaultCompression
	}
	return level
}

func dynamicZlibPoolForLevel(level int) (*dynamicZlibPool, error) {
	level = normalizedZlibLevel(level)
	if value, ok := dynamicZlibPools.Load(level); ok {
		return value.(*dynamicZlibPool), nil
	}
	dynamicPoolCreateMu.Lock()
	defer dynamicPoolCreateMu.Unlock()
	if value, ok := dynamicZlibPools.Load(level); ok {
		return value.(*dynamicZlibPool), nil
	}
	pool := &dynamicZlibPool{writers: make(chan *zlib.Writer, dynamicPoolSize())}
	for i := 0; i < cap(pool.writers); i++ {
		writer, err := zlib.NewWriterLevel(io.Discard, level)
		if err != nil {
			return nil, err
		}
		_, _ = writer.Write(dynamicCompressionWarmData[:])
		if err := writer.Close(); err != nil {
			return nil, err
		}
		pool.writers <- writer
	}
	dynamicZlibPools.Store(level, pool)
	return pool, nil
}

func encodeDynamicDeflate(data []byte, level int) (dynamicCompressedBuffer, error) {
	pool, err := dynamicZlibPoolForLevel(level)
	if err != nil {
		return dynamicCompressedBuffer{}, err
	}
	buffer := takeDynamicByteBuffer()
	writer := <-pool.writers
	writer.Reset(buffer)
	_, writeErr := writer.Write(data)
	closeErr := writer.Close()
	pool.writers <- writer
	if writeErr != nil {
		putDynamicByteBuffer(buffer)
		return dynamicCompressedBuffer{}, writeErr
	}
	if closeErr != nil {
		putDynamicByteBuffer(buffer)
		return dynamicCompressedBuffer{}, closeErr
	}
	return dynamicCompressedBuffer{data: buffer.Bytes(), byteBuffer: buffer}, nil
}

type dynamicBrotliPool struct{ writers chan *brotli.Writer }

func normalizedBrotliLevel(level int) int {
	if level <= 0 {
		return brotli.DefaultCompression
	}
	return level
}

func dynamicBrotliPoolForLevel(level int) *dynamicBrotliPool {
	level = normalizedBrotliLevel(level)
	if value, ok := dynamicBrotliPools.Load(level); ok {
		return value.(*dynamicBrotliPool)
	}
	dynamicPoolCreateMu.Lock()
	defer dynamicPoolCreateMu.Unlock()
	if value, ok := dynamicBrotliPools.Load(level); ok {
		return value.(*dynamicBrotliPool)
	}
	pool := &dynamicBrotliPool{writers: make(chan *brotli.Writer, dynamicPoolSize())}
	for i := 0; i < cap(pool.writers); i++ {
		writer := brotli.NewWriterLevel(io.Discard, level)
		_, _ = writer.Write(dynamicCompressionWarmData[:])
		_ = writer.Close()
		pool.writers <- writer
	}
	dynamicBrotliPools.Store(level, pool)
	return pool
}

func encodeDynamicBrotli(data []byte, level int) (dynamicCompressedBuffer, error) {
	pool := dynamicBrotliPoolForLevel(level)
	buffer := takeDynamicByteBuffer()
	writer := <-pool.writers
	writer.Reset(buffer)
	_, writeErr := writer.Write(data)
	closeErr := writer.Close()
	pool.writers <- writer
	if writeErr != nil {
		putDynamicByteBuffer(buffer)
		return dynamicCompressedBuffer{}, writeErr
	}
	if closeErr != nil {
		putDynamicByteBuffer(buffer)
		return dynamicCompressedBuffer{}, closeErr
	}
	return dynamicCompressedBuffer{data: buffer.Bytes(), byteBuffer: buffer}, nil
}

func encodeDynamicCompression(encoding string, data []byte, level int) (dynamicCompressedBuffer, error) {
	switch encoding {
	case "zstd":
		return encodeDynamicZstd(data, level)
	case "gzip":
		return encodeDynamicGzip(data, level)
	case "br":
		return encodeDynamicBrotli(data, level)
	case "deflate":
		return encodeDynamicDeflate(data, level)
	default:
		return dynamicCompressedBuffer{}, newRuntimeConfigError("unsupported-dynamic-encoding", "unsupported dynamic encoding: "+encoding, nil)
	}
}
