# Runtime benchmarks

The default suite measures current request hot paths:

- client island rendering
- writer raw and escaped writes
- writer buffer lifecycle
- writer core close without `httptest` body retention
- full test HTTP response lifecycle
- static and dynamic import maps
- pre-router matching

## Fast complete pass

```bash
go test ./internal/serverruntime \
  -run '^$' \
  -bench . \
  -benchmem \
  -benchtime=200ms \
  -count=1
```

## Writer core

`BenchmarkGoxWriterLifecycleCoreFallbackContentType` reuses the request and response state and writes to a
minimal sink that only counts bytes. It excludes `httptest.NewRequest`,
`httptest.ResponseRecorder`, and the recorder's second body copy.

```bash
go test ./internal/serverruntime \
  -run '^$' \
  -bench '^BenchmarkGoxWriterLifecycleCoreFallbackContentType$' \
  -benchmem \
  -benchtime=2s \
  -count=1
```

Memory profile:

```bash
go test ./internal/serverruntime \
  -run '^$' \
  -bench '^BenchmarkGoxWriterLifecycleCoreFallbackContentType$' \
  -benchmem \
  -benchtime=2s \
  -memprofile writer-core.mem.prof

go tool pprof -alloc_objects writer-core.mem.prof
# pprof: top
# pprof: top -cum

go tool pprof -alloc_space writer-core.mem.prof
# pprof: top
# pprof: top -cum
```

CPU profile:

```bash
go test ./internal/serverruntime \
  -run '^$' \
  -bench '^BenchmarkGoxWriterLifecycleCoreFallbackContentType$' \
  -benchtime=3s \
  -cpuprofile writer-core.cpu.prof

go tool pprof writer-core.cpu.prof
# pprof: top
# pprof: top -cum
```

## Full test HTTP lifecycle

These benchmarks intentionally include request construction, response recorder,
response registration, writer creation, body writing, and close:

```bash
go test ./internal/serverruntime \
  -run '^$' \
  -bench '^BenchmarkRuntimeResponseLifecycle' \
  -benchmem \
  -benchtime=200ms \
  -count=1
```

Their allocations must not be attributed to `GoxWriter.Close` alone.

## Client islands and writer writes

```bash
go test ./internal/serverruntime \
  -run '^$' \
  -bench '^(BenchmarkWriteClientIsland|BenchmarkGoxWriter(Raw|Escaped|Buffer|CloseCore))' \
  -benchmem \
  -benchtime=200ms \
  -count=1
```

## SDK

The SDK is a separate Go module and must be benchmarked from its own directory:

```bash
cd ../gox-sdk
go test ./... -run '^$' -bench . -benchmem -benchtime=200ms -count=1
```

## Hot request pipeline

Configured writer close vs fallback:

```bash
go test ./internal/serverruntime \
  -run '^$' \
  -bench '^(BenchmarkGoxWriterLifecycleCoreConfigured|BenchmarkGoxWriterLifecycleCoreFallbackContentType)$' \
  -benchmem \
  -benchtime=2s \
  -count=1
```

Hot in-process ServeHTTP:

```bash
go test ./internal/serverruntime \
  -run '^$' \
  -bench '^BenchmarkRuntimeServeHTTP(Plain|ClientIslands)$' \
  -benchmem \
  -benchtime=2s \
  -count=1
```

Latency distribution (fixed 10,000 requests):

```bash
go test ./internal/serverruntime \
  -run '^$' \
  -bench '^BenchmarkRuntimeServeHTTPDistribution$' \
  -benchmem \
  -benchtime=10000x \
  -count=1
```

The hot request benchmarks set Content-Type from the handler and intentionally do
not depend on the generated `GoxHTTPContentTypes` plugin export.
