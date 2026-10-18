package serverruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
	sdk "github.com/gox-lang/gox/test"
)

// TestExecutor executes github.com/gox-lang/gox/test pipelines against an in-memory Runtime.
// It is used by the gox test runner. The public SDK never imports serverruntime directly.
type TestExecutor struct {
	runtime    *Runtime
	outputRoot string

	baseConfig     *server.Config
	runtimeFactory func(server.Config) (*Runtime, error)
}

// TestExecutorOptions configures the in-memory test executor.
type TestExecutorOptions struct {
	// OutputRoot is the tests directory used by snapshot resolution.
	OutputRoot string

	// BaseConfig is the app/server config used when test.Config.OverrideConfig is set.
	BaseConfig *server.Config

	// RuntimeFactory creates a runtime from a possibly overridden config.
	RuntimeFactory func(server.Config) (*Runtime, error)
}

func NewTestExecutor(runtime *Runtime) *TestExecutor {
	return NewTestExecutorWithOptions(runtime, TestExecutorOptions{})
}

func NewTestExecutorWithOptions(runtime *Runtime, options TestExecutorOptions) *TestExecutor {
	return &TestExecutor{runtime: runtime, outputRoot: options.OutputRoot, baseConfig: options.BaseConfig, runtimeFactory: options.RuntimeFactory}
}

func (e *TestExecutor) Execute(config sdk.Config, pipeline []sdk.ExecutePipeLine) (sdk.ExecuteResult, bool) {
	if sdk.IsBench() {
		return e.ExecuteBenchmark(config, pipeline)
	}

	result := sdk.ExecuteResult{
		Match: true,
		Steps: make([]sdk.StepResult, 0, len(pipeline)),
	}

	rt, err := e.runtimeForConfig(config)
	if err != nil {
		result.Match = false
		result.Steps = append(result.Steps, sdk.StepResult{
			Index: 1,
			Type:  "error",
			Match: false,
			Error: err.Error(),
		})
		return result, false
	}

	active := *e
	active.runtime = rt

	if err := active.preloadPipelineRoutes(pipeline); err != nil {
		result.Match = false
		result.Steps = append(result.Steps, sdk.StepResult{
			Index: 1,
			Type:  "error",
			Match: false,
			Error: err.Error(),
		})
		return result, false
	}

	browser := newTestBrowser()

	if len(config.Session) > 0 {
		if err := active.bootstrapSession(browser, config.Session); err != nil {
			result.Match = false
			result.Steps = append(result.Steps, sdk.StepResult{
				Index: 1,
				Type:  "error",
				Match: false,
				Error: err.Error(),
			})
			return result, false
		}
	}

	for i, pipe := range pipeline {
		step := active.executeStep(browser, i+1, pipe)
		if !step.Match {
			result.Match = false
		}
		result.Steps = append(result.Steps, step)
	}

	return result, result.Match
}

func (e *TestExecutor) ExecuteBenchmark(config sdk.Config, pipeline []sdk.ExecutePipeLine) (sdk.ExecuteResult, bool) {
	warmup := config.Warmup
	if warmup <= 0 {
		warmup = 100
	}
	iterations := config.Iterations
	if iterations <= 0 {
		iterations = 1000
	}
	sampleRate := config.BenchSampleRate
	if sampleRate <= 0 {
		sampleRate = 0.01
	}
	if sampleRate > 1 {
		sampleRate = 1
	}

	bench := sdk.BenchmarkResult{
		Warmup:     warmup,
		Iterations: iterations,
		SampleRate: sampleRate,
		Status:     make(map[int]int),
		Steps:      make([]sdk.BenchmarkStepResult, 0, len(pipeline)),
	}
	result := sdk.ExecuteResult{
		Mode:      sdk.ModeBench,
		Match:     true,
		Steps:     make([]sdk.StepResult, 0, len(pipeline)),
		Benchmark: &bench,
	}

	rt, err := e.runtimeForConfig(config)
	if err != nil {
		result.Match = false
		result.Steps = append(result.Steps, sdk.StepResult{Index: 1, Type: "error", Match: false, Error: err.Error()})
		result.Benchmark.Errors++
		return result, false
	}

	active := *e
	active.runtime = rt

	if err := active.preloadPipelineRoutes(pipeline); err != nil {
		result.Match = false
		result.Steps = append(result.Steps, sdk.StepResult{Index: 1, Type: "error", Match: false, Error: err.Error()})
		result.Benchmark.Errors++
		return result, false
	}

	browser := newTestBrowser()
	if len(config.Session) > 0 {
		if err := active.bootstrapSession(browser, config.Session); err != nil {
			result.Match = false
			result.Steps = append(result.Steps, sdk.StepResult{Index: 1, Type: "error", Match: false, Error: err.Error()})
			result.Benchmark.Errors++
			return result, false
		}
	}

	steps := make([]sdk.BenchmarkStepResult, len(pipeline))
	for i, pipe := range pipeline {
		steps[i] = benchmarkStepBase(i+1, pipe.Request)
		if steps[i].Status == nil {
			steps[i].Status = make(map[int]int)
		}
		if steps[i].Error != "" {
			steps[i].Errors++
			result.Match = false
		}
	}
	if !result.Match {
		finalizeBenchmarkResultSteps(&result, steps)
		return result, false
	}

	// Warmup repeats the complete pipeline in order. This preserves the same
	// dependency semantics as gox test: step N can depend on state produced by
	// step N-1 on every pipeline iteration.
	for i := 0; i < warmup; i++ {
		for stepIndex, pipe := range pipeline {
			if _, err := active.runBenchmarkRequest(browser, pipe.Request, false); err != nil {
				steps[stepIndex].Error = err.Error()
				steps[stepIndex].Errors++
				result.Match = false
				result.Benchmark.Errors++
				finalizeBenchmarkResultSteps(&result, steps)
				return result, false
			}
		}
	}

	sampledIterations := benchmarkSampledIterations(iterations, sampleRate)
	rawSampledHandlerAllocs := make([]float64, len(pipeline))
	rawSampledHandlerBytes := make([]float64, len(pipeline))
	for i := range steps {
		steps[i].Iterations = iterations
		steps[i].AllocationSamples = sampledIterations
		if steps[i].Status == nil {
			steps[i].Status = make(map[int]int)
		}
	}

	started := time.Now()
	for iteration := 0; iteration < iterations; iteration++ {
		sampleHandler := benchmarkShouldSampleHandlerAllocs(iteration, sampleRate)
		for stepIndex, pipe := range pipeline {
			stepStarted := time.Now()
			run, err := active.runBenchmarkRequest(browser, pipe.Request, sampleHandler)
			steps[stepIndex].DurationNS += time.Since(stepStarted).Nanoseconds()
			if err != nil {
				steps[stepIndex].Errors++
				if steps[stepIndex].Error == "" {
					steps[stepIndex].Error = err.Error()
				}
				result.Match = false
				continue
			}
			steps[stepIndex].Status[run.Status]++
			steps[stepIndex].BytesSentPerOp += float64(run.BytesSent)
			steps[stepIndex].BytesRecvPerOp += float64(run.BytesReceived)
			if run.SampledHandler {
				rawSampledHandlerAllocs[stepIndex] += float64(run.HandlerAllocs)
				rawSampledHandlerBytes[stepIndex] += float64(run.HandlerBytes)
				steps[stepIndex].HandlerAllocsOp += float64(run.HandlerAllocs)
				steps[stepIndex].HandlerBytesOp += float64(run.HandlerBytes)
			}
		}
	}
	measuredDurationNS := time.Since(started).Nanoseconds()

	for i := range steps {
		active.finalizeBenchmarkStep(&steps[i], pipeline[i], iterations, sampleRate, sampledIterations, rawSampledHandlerAllocs[i], rawSampledHandlerBytes[i])
		result.Benchmark.Steps = append(result.Benchmark.Steps, steps[i])
		mergeBenchmarkStep(result.Benchmark, steps[i])
		result.Steps = append(result.Steps, sdk.StepResult{
			Index:     i + 1,
			Type:      steps[i].Type,
			Method:    steps[i].Method,
			URL:       steps[i].URL,
			Match:     steps[i].Error == "" && steps[i].Errors == 0,
			Error:     steps[i].Error,
			Benchmark: steps[i],
		})
		if steps[i].Error != "" || steps[i].Errors > 0 {
			result.Match = false
		}
	}

	result.Benchmark.DurationNS = measuredDurationNS
	if len(pipeline) > 0 {
		ops := float64(iterations * len(pipeline))
		if ops > 0 {
			result.Benchmark.NsPerOp = float64(result.Benchmark.DurationNS) / ops
			if result.Benchmark.NsPerOp > 0 {
				result.Benchmark.OpsPerSec = float64(time.Second) / result.Benchmark.NsPerOp
			}
			result.Benchmark.BytesSentPerOp /= ops
			result.Benchmark.BytesRecvPerOp /= ops
			result.Benchmark.HandlerAllocsOp /= ops
			result.Benchmark.HandlerBytesOp /= ops
		}
	}

	return result, result.Match
}

func finalizeBenchmarkResultSteps(result *sdk.ExecuteResult, steps []sdk.BenchmarkStepResult) {
	if result == nil || result.Benchmark == nil {
		return
	}
	for i, step := range steps {
		result.Benchmark.Steps = append(result.Benchmark.Steps, step)
		mergeBenchmarkStep(result.Benchmark, step)
		result.Steps = append(result.Steps, sdk.StepResult{
			Index:     i + 1,
			Type:      step.Type,
			Method:    step.Method,
			URL:       step.URL,
			Match:     step.Error == "" && step.Errors == 0,
			Error:     step.Error,
			Benchmark: step,
		})
	}
}

func (e *TestExecutor) finalizeBenchmarkStep(step *sdk.BenchmarkStepResult, pipe sdk.ExecutePipeLine, iterations int, sampleRate float64, sampledIterations int, rawSampledHandlerAllocs float64, rawSampledHandlerBytes float64) {
	if step == nil || iterations <= 0 {
		return
	}
	step.NsPerOp = float64(step.DurationNS) / float64(iterations)
	if step.NsPerOp > 0 {
		step.OpsPerSec = float64(time.Second) / step.NsPerOp
	}
	step.BytesSentPerOp /= float64(iterations)
	step.BytesRecvPerOp /= float64(iterations)
	step.AllocationSamples = sampledIterations
	if sampledIterations <= 0 {
		return
	}
	step.HandlerAllocsOp /= float64(sampledIterations)
	step.HandlerBytesOp /= float64(sampledIterations)
	if rawSampledHandlerAllocs == 0 && rawSampledHandlerBytes == 0 {
		return
	}
	sites, err := e.collectBenchmarkRequestAllocationSites(newTestBrowser(), pipe.Request, sampledIterations)
	if err != nil {
		// The profile pass is best-effort. It must not make a successful benchmark
		// fail, because handler alloc totals already came from the measured section.
		sites = nil
	}
	// Memprofile sites are reported only when the runtime provides an exact
	// stack location. Never manufacture a source line to reconcile profile and
	// MemStats totals: that made unrelated allocations appear on the first
	// allocation-looking line of a handler.
	step.AllocationSites = sdkBenchmarkAllocationSites(sites)
}

type benchmarkRunResult struct {
	Status          int
	BytesSent       int64
	BytesReceived   int64
	HandlerAllocs   uint64
	HandlerBytes    uint64
	AllocationSites []sdk.BenchmarkAllocationSite
	SampledHandler  bool
}

func benchmarkShouldSampleHandlerAllocs(iteration int, sampleRate float64) bool {
	if sampleRate <= 0 {
		return false
	}
	if sampleRate >= 1 {
		return true
	}
	every := int(1 / sampleRate)
	if every < 1 {
		every = 1
	}
	return iteration%every == 0
}

func benchmarkSampledIterations(iterations int, sampleRate float64) int {
	count := 0
	for i := 0; i < iterations; i++ {
		if benchmarkShouldSampleHandlerAllocs(i, sampleRate) {
			count++
		}
	}
	return count
}

func (e *TestExecutor) executeBenchmarkStep(browser *testBrowser, index int, pipe sdk.ExecutePipeLine, warmup int, iterations int, sampleRate float64) sdk.BenchmarkStepResult {
	step := benchmarkStepBase(index, pipe.Request)
	if step.Error != "" {
		return step
	}
	for i := 0; i < warmup; i++ {
		if _, err := e.runBenchmarkRequest(browser, pipe.Request, false); err != nil {
			step.Error = err.Error()
			step.Errors++
			return step
		}
	}

	step.Iterations = iterations
	step.Status = make(map[int]int)
	started := time.Now()
	rawSampledHandlerAllocs := float64(0)
	rawSampledHandlerBytes := float64(0)
	for i := 0; i < iterations; i++ {
		run, err := e.runBenchmarkRequest(browser, pipe.Request, benchmarkShouldSampleHandlerAllocs(i, sampleRate))
		if err != nil {
			step.Errors++
			if step.Error == "" {
				step.Error = err.Error()
			}
			continue
		}
		step.Status[run.Status]++
		step.BytesSentPerOp += float64(run.BytesSent)
		step.BytesRecvPerOp += float64(run.BytesReceived)
		if run.SampledHandler {
			rawSampledHandlerAllocs += float64(run.HandlerAllocs)
			rawSampledHandlerBytes += float64(run.HandlerBytes)
			step.HandlerAllocsOp += float64(run.HandlerAllocs)
			step.HandlerBytesOp += float64(run.HandlerBytes)
		}
	}
	step.DurationNS = time.Since(started).Nanoseconds()
	if iterations > 0 {
		step.NsPerOp = float64(step.DurationNS) / float64(iterations)
		if step.NsPerOp > 0 {
			step.OpsPerSec = float64(time.Second) / step.NsPerOp
		}
		step.BytesSentPerOp /= float64(iterations)
		step.BytesRecvPerOp /= float64(iterations)
		sampledIterations := benchmarkSampledIterations(iterations, sampleRate)
		step.AllocationSamples = sampledIterations
		if sampledIterations > 0 {
			step.HandlerAllocsOp /= float64(sampledIterations)
			step.HandlerBytesOp /= float64(sampledIterations)
			if rawSampledHandlerAllocs > 0 || rawSampledHandlerBytes > 0 {
				sites, err := e.collectBenchmarkRequestAllocationSites(browser.clone(), pipe.Request, sampledIterations)
				if err != nil {
					step.Errors++
					if step.Error == "" {
						step.Error = err.Error()
					}
				} else {
					step.AllocationSites = sdkBenchmarkAllocationSites(sites)
				}
			}
		}
	}
	return step
}

func benchmarkAllocationSitesMatchTotals(sites []benchmarkAllocationSite, rawAllocs uint64, rawBytes uint64) bool {
	if len(sites) == 0 {
		return rawAllocs == 0 && rawBytes == 0
	}
	var allocs uint64
	var bytes uint64
	for _, site := range sites {
		allocs += site.Allocs
		bytes += site.Bytes
	}
	return allocs == rawAllocs && bytes == rawBytes
}

func benchmarkStepBase(index int, request sdk.PipeRequest) sdk.BenchmarkStepResult {
	switch req := request.(type) {
	case sdk.NewRequest:
		method := strings.TrimSpace(req.Method)
		if method == "" {
			method = http.MethodGet
		}
		return sdk.BenchmarkStepResult{Index: index, Type: "request", Method: method, URL: req.URL, Status: make(map[int]int)}
	case *sdk.NewRequest:
		if req == nil {
			return sdk.BenchmarkStepResult{Index: index, Type: "error", Error: "request is nil", Status: make(map[int]int)}
		}
		return benchmarkStepBase(index, *req)
	case sdk.NewActionRequest:
		return sdk.BenchmarkStepResult{Index: index, Type: "action", Method: req.Method, URL: req.URL, Status: make(map[int]int)}
	case *sdk.NewActionRequest:
		if req == nil {
			return sdk.BenchmarkStepResult{Index: index, Type: "error", Error: "action request is nil", Status: make(map[int]int)}
		}
		return benchmarkStepBase(index, *req)
	default:
		return sdk.BenchmarkStepResult{Index: index, Type: "error", Error: "unsupported request type", Status: make(map[int]int)}
	}
}

func (e *TestExecutor) runBenchmarkRequest(browser *testBrowser, request sdk.PipeRequest, sampleHandlerAllocs bool) (benchmarkRunResult, error) {
	var zero benchmarkRunResult
	var input sdk.NewRequest
	switch req := request.(type) {
	case sdk.NewRequest:
		input = req
	case *sdk.NewRequest:
		if req == nil {
			return zero, fmt.Errorf("request is nil")
		}
		input = *req
	case sdk.NewActionRequest:
		var buildErr error
		input, buildErr = buildActionHTTPRequestInput(req)
		if buildErr != nil {
			return zero, buildErr
		}
	case *sdk.NewActionRequest:
		if req == nil {
			return zero, fmt.Errorf("action request is nil")
		}
		return e.runBenchmarkRequest(browser, *req, sampleHandlerAllocs)
	default:
		return zero, fmt.Errorf("unsupported request type")
	}

	req, err := buildTestHTTPRequest(input)
	if err != nil {
		return zero, err
	}
	req = withBenchmarkRequestContext(req)
	browser.apply(req)
	run := benchmarkRunResult{BytesSent: estimateHTTPRequestBytes(req)}

	var sample *benchmarkHandlerAllocSample
	var restoreBenchmarkHook func()
	if sampleHandlerAllocs {
		sample = beginBenchmarkHandlerAllocSample()
		restoreBenchmarkHook = gox.SetBenchmarkHandlerSectionHook(func(fn func()) {
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			fn()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			if after.Mallocs >= before.Mallocs && after.TotalAlloc >= before.TotalAlloc {
				sample.add(after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc)
			}
		})
	}
	rec := httptest.NewRecorder()
	e.runtime.ServeHTTP(rec, req)
	if restoreBenchmarkHook != nil {
		restoreBenchmarkHook()
	}
	if sample != nil {
		allocs, bytes, sites := endBenchmarkHandlerAllocSampleWithSites(sample)
		run.HandlerAllocs = allocs
		run.HandlerBytes = bytes
		run.AllocationSites = sdkBenchmarkAllocationSites(sites)
		run.SampledHandler = true
	}
	res := rec.Result()
	defer res.Body.Close()
	browser.capture(res)

	bodyBytes := int64(0)
	if rec.Body != nil {
		bodyBytes = int64(rec.Body.Len())
	} else if res.ContentLength > 0 {
		bodyBytes = res.ContentLength
	}
	run.Status = res.StatusCode
	run.BytesReceived = estimateHTTPResponseBytes(res, bodyBytes)
	return run, nil
}

func (e *TestExecutor) collectBenchmarkRequestAllocationSites(browser *testBrowser, request sdk.PipeRequest, count int) ([]benchmarkAllocationSite, error) {
	allowedFiles := e.benchmarkRequestSourceFiles(request)
	return collectBenchmarkAllocationSites(count, allowedFiles, func() error {
		_, err := e.runBenchmarkRequest(browser, request, false)
		return err
	})
}

func (e *TestExecutor) inferBenchmarkRequestAllocationSites(request sdk.PipeRequest, allocs uint64, bytes uint64) ([]benchmarkAllocationSite, error) {
	if allocs == 0 && bytes == 0 {
		return nil, nil
	}
	route := e.benchmarkRequestRoute(request)
	if route == nil {
		return nil, nil
	}
	sourceFile := strings.TrimSpace(route.RelPath)
	if sourceFile == "" {
		sourceFile = strings.TrimSpace(route.AbsPath)
	}
	if sourceFile == "" || strings.TrimSpace(route.GeneratedPath) == "" {
		return nil, nil
	}
	name := ""
	switch req := request.(type) {
	case sdk.NewRequest:
		name = strings.TrimSpace(req.Method)
	case *sdk.NewRequest:
		if req != nil {
			name = strings.TrimSpace(req.Method)
		}
	case sdk.NewActionRequest:
		name = strings.TrimSpace(req.Method)
	case *sdk.NewActionRequest:
		if req != nil {
			name = strings.TrimSpace(req.Method)
		}
	}
	if name == "" {
		name = strings.TrimSpace(route.RoutePath)
	}
	return inferBenchmarkAllocationSitesFromGenerated(route.GeneratedPath, sourceFile, name, allocs, bytes)
}

func (e *TestExecutor) benchmarkRequestRoute(request sdk.PipeRequest) *GoxRouteRuntime {
	if e == nil || e.runtime == nil {
		return nil
	}
	req, err := e.preloadHTTPRequest(request)
	if err != nil || req == nil || req.URL == nil {
		return nil
	}
	target := normalizePreRouteTarget(req.URL.Path)
	if preRouter := e.runtime.state().PreRouter; preRouter != nil {
		if _, matchedTarget, _, ok := preRouter.Match(req.URL.Path); ok {
			target = matchedTarget
		}
	}
	route, ok, err := e.runtime.getOrCreateGoxRoute(target, nil)
	if err != nil || !ok || route == nil {
		return nil
	}
	return route
}

func (e *TestExecutor) benchmarkRequestSourceFiles(request sdk.PipeRequest) map[string]struct{} {
	route := e.benchmarkRequestRoute(request)
	if route == nil {
		return nil
	}
	files := make(map[string]struct{}, 2)
	if rel := strings.TrimSpace(route.RelPath); rel != "" {
		files[filepath.ToSlash(rel)] = struct{}{}
	}
	if abs := strings.TrimSpace(route.AbsPath); abs != "" {
		files[filepath.ToSlash(abs)] = struct{}{}
	}
	if len(files) == 0 {
		return nil
	}
	return files
}

func estimateHTTPRequestBytes(req *http.Request) int64 {
	if req == nil {
		return 0
	}
	uri := req.URL.RequestURI()
	if uri == "" {
		uri = "/"
	}
	total := int64(len(req.Method) + 1 + len(uri) + len(" HTTP/1.1\r\n"))
	if req.Host != "" {
		total += int64(len("Host: ") + len(req.Host) + len("\r\n"))
	}
	for key, values := range req.Header {
		for _, value := range values {
			total += int64(len(key) + len(": ") + len(value) + len("\r\n"))
		}
	}
	total += int64(len("\r\n"))
	if req.ContentLength > 0 {
		total += req.ContentLength
	}
	return total
}

func estimateHTTPResponseBytes(res *http.Response, bodyBytes int64) int64 {
	if res == nil {
		return bodyBytes
	}
	proto := res.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	status := res.Status
	if status == "" {
		status = fmt.Sprintf("%d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}
	total := int64(len(proto) + 1 + len(status) + len("\r\n"))
	for key, values := range res.Header {
		for _, value := range values {
			total += int64(len(key) + len(": ") + len(value) + len("\r\n"))
		}
	}
	total += int64(len("\r\n"))
	if bodyBytes > 0 {
		total += bodyBytes
	}
	return total
}

func mergeBenchmarkStep(total *sdk.BenchmarkResult, step sdk.BenchmarkStepResult) {
	if total == nil {
		return
	}
	total.DurationNS += step.DurationNS
	total.BytesSentPerOp += step.BytesSentPerOp * float64(step.Iterations)
	total.BytesRecvPerOp += step.BytesRecvPerOp * float64(step.Iterations)
	total.HandlerAllocsOp += step.HandlerAllocsOp * float64(step.Iterations)
	total.HandlerBytesOp += step.HandlerBytesOp * float64(step.Iterations)
	total.AllocationSamples += step.AllocationSamples
	total.Errors += step.Errors
	if total.Status == nil {
		total.Status = make(map[int]int)
	}
	for status, count := range step.Status {
		total.Status[status] += count
	}
	total.AllocationSites = mergeBenchmarkAllocationSites(total.AllocationSites, step.AllocationSites)
}

func sdkBenchmarkAllocationSites(sites []benchmarkAllocationSite) []sdk.BenchmarkAllocationSite {
	if len(sites) == 0 {
		return nil
	}
	out := make([]sdk.BenchmarkAllocationSite, 0, len(sites))
	for _, site := range sites {
		out = append(out, sdk.BenchmarkAllocationSite{
			File:   site.File,
			Line:   site.Line,
			Name:   cleanBenchmarkAllocationSiteName(site.Name),
			Allocs: site.Allocs,
			Bytes:  site.Bytes,
		})
	}
	return out
}

func cleanBenchmarkAllocationSiteName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if idx := strings.Index(name, ")."); idx >= 0 && idx+2 < len(name) {
		name = name[idx+2:]
	}
	name = strings.ReplaceAll(name, "-fm", "")
	name = strings.TrimSpace(name)
	return name
}

func mergeBenchmarkAllocationSites(dst []sdk.BenchmarkAllocationSite, src []sdk.BenchmarkAllocationSite) []sdk.BenchmarkAllocationSite {
	if len(src) == 0 {
		return dst
	}
	merged := make(map[string]sdk.BenchmarkAllocationSite, len(dst)+len(src))
	for _, site := range dst {
		key := benchmarkAllocationSiteKey(site)
		merged[key] = site
	}
	for _, site := range src {
		key := benchmarkAllocationSiteKey(site)
		current := merged[key]
		if current.File == "" && current.Name == "" {
			current = site
		} else {
			current.Allocs += site.Allocs
			current.Bytes += site.Bytes
		}
		merged[key] = current
	}
	out := make([]sdk.BenchmarkAllocationSite, 0, len(merged))
	for _, site := range merged {
		out = append(out, site)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		if out[i].Allocs != out[j].Allocs {
			return out[i].Allocs > out[j].Allocs
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func benchmarkAllocationSiteKey(site sdk.BenchmarkAllocationSite) string {
	return site.File + "|" + strconv.Itoa(site.Line) + "|" + site.Name
}

func (e *TestExecutor) preloadPipelineRoutes(pipeline []sdk.ExecutePipeLine) error {
	if e == nil || e.runtime == nil {
		return fmt.Errorf("gox test executor: runtime is nil")
	}

	type preloadEntry struct {
		method string
		url    string
		target string
	}

	seen := make(map[string]struct{}, len(pipeline))
	entries := make([]preloadEntry, 0, len(pipeline))
	for _, pipe := range pipeline {
		req, err := e.preloadHTTPRequest(pipe.Request)
		if err != nil {
			return err
		}
		if req == nil {
			continue
		}

		ctx := &gox.Context{Request: buildRequest(req)}
		e.runtime.preRoute(ctx)

		route := ctx.Request.Route
		if route.IsStatic() {
			continue
		}

		target := strings.TrimSpace(route.Target())
		if target == "" {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		entries = append(entries, preloadEntry{method: req.Method, url: req.URL.Path, target: target})
	}

	if len(entries) == 0 {
		return nil
	}

	logs.Info("test preload started: routes=%d", len(entries))

	loaded := 0
	reused := 0
	for _, entry := range entries {
		runtimeRoute, ok, err := e.runtime.getOrCreateGoxRoute(entry.target, nil)
		if err != nil {
			return fmt.Errorf("gox test executor: preload route %q failed: %w", entry.target, err)
		}
		if !ok || runtimeRoute == nil {
			continue
		}

		wasLoaded := isGoxRouteLoaded(runtimeRoute)
		if err := e.runtime.ensureGoxRouteReady(runtimeRoute); err != nil {
			return fmt.Errorf("gox test executor: preload route %q failed: %w", entry.target, err)
		}

		status := "loaded"
		if wasLoaded {
			status = "reused"
			reused++
		} else {
			loaded++
		}

		rel := strings.TrimSpace(runtimeRoute.RelPath)
		if rel == "" {
			rel = strings.TrimSpace(entry.target)
		}
		logs.Info("test preload route: %s %s -> %s (%s)", entry.method, entry.url, rel, status)
	}

	logs.Info("test preload completed: loaded=%d reused=%d", loaded, reused)
	return nil
}

func isGoxRouteLoaded(route *GoxRouteRuntime) bool {
	if route == nil {
		return false
	}
	route.mu.Lock()
	defer route.mu.Unlock()
	return route.State == GoxRouteLoaded && (route.Plugin != nil || len(route.HTTPHandlers) > 0 || len(route.Actions) > 0)
}

func (e *TestExecutor) preloadHTTPRequest(request sdk.PipeRequest) (*http.Request, error) {
	switch req := request.(type) {
	case sdk.NewRequest:
		return buildTestHTTPRequest(req)
	case *sdk.NewRequest:
		if req == nil {
			return nil, fmt.Errorf("gox test executor: preload request is nil")
		}
		return buildTestHTTPRequest(*req)
	case sdk.NewActionRequest:
		input, err := buildActionHTTPRequestInput(req)
		if err != nil {
			return nil, err
		}
		return buildTestHTTPRequest(input)
	case *sdk.NewActionRequest:
		if req == nil {
			return nil, fmt.Errorf("gox test executor: preload action request is nil")
		}
		input, err := buildActionHTTPRequestInput(*req)
		if err != nil {
			return nil, err
		}
		return buildTestHTTPRequest(input)
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("gox test executor: unsupported request type")
	}
}

func (e *TestExecutor) runtimeForConfig(config sdk.Config) (*Runtime, error) {
	if e == nil {
		return nil, fmt.Errorf("gox test executor: executor is nil")
	}
	if config.OverrideConfig == nil {
		if e.runtime == nil {
			return nil, fmt.Errorf("gox test executor: runtime is nil")
		}
		return e.runtime, nil
	}
	if e.baseConfig == nil || e.runtimeFactory == nil {
		return nil, fmt.Errorf("gox test executor: OverrideConfig requires a runtime factory")
	}
	cfg := *e.baseConfig
	config.OverrideConfig(&cfg)
	rt, err := e.runtimeFactory(cfg)
	if err != nil {
		return nil, fmt.Errorf("gox test executor: override config runtime failed: %w", err)
	}
	if rt == nil {
		return nil, fmt.Errorf("gox test executor: override config runtime is nil")
	}
	return rt, nil
}

func (e *TestExecutor) bootstrapSession(browser *testBrowser, values sdk.Session) error {
	if e == nil || e.runtime == nil {
		return fmt.Errorf("gox test executor: runtime is nil")
	}
	if browser == nil {
		return fmt.Errorf("gox test executor: browser is nil")
	}

	logs.Info("test session bootstrap started: values=%d", len(values))

	state := e.runtime.state()
	if state == nil || state.Session == nil {
		return fmt.Errorf("gox test executor: session policy is not configured")
	}

	storage := e.runtime.getSessionStorage()
	if storage == nil {
		return fmt.Errorf("gox test executor: session storage is not initialized")
	}

	meta := newSessionMeta(state.Session, time.Now())
	instance, err := storage.CreateSession(context.Background(), meta)
	if err != nil {
		return fmt.Errorf("gox test executor: create session failed: %w", err)
	}

	for key, value := range values {
		if err := instance.Set(key, value); err != nil {
			return fmt.Errorf("gox test executor: set session value %q failed: %w", key, err)
		}
	}

	if err := storage.SaveSession(context.Background(), instance.ID()); err != nil {
		return fmt.Errorf("gox test executor: save session failed: %w", err)
	}

	browser.setCookie(&http.Cookie{
		Name:     state.Session.idCookie,
		Value:    instance.ID(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	browser.setCookie(&http.Cookie{
		Name:     state.Session.tokenCookie,
		Value:    instance.Token(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	logs.Info("test session bootstrap completed: values=%d idCookie=%s tokenCookie=%s", len(values), state.Session.idCookie, state.Session.tokenCookie)
	return nil
}

func (e *TestExecutor) executeStep(browser *testBrowser, index int, pipe sdk.ExecutePipeLine) sdk.StepResult {
	switch req := pipe.Request.(type) {
	case sdk.NewRequest:
		return e.executeHTTP(browser, index, req, pipe.Expected)
	case *sdk.NewRequest:
		if req == nil {
			return invalidTestStep(index, "request is nil")
		}
		return e.executeHTTP(browser, index, *req, pipe.Expected)
	case sdk.NewActionRequest:
		return e.executeAction(browser, index, req, pipe.Expected)
	case *sdk.NewActionRequest:
		if req == nil {
			return invalidTestStep(index, "action request is nil")
		}
		return e.executeAction(browser, index, *req, pipe.Expected)
	default:
		return invalidTestStep(index, "unsupported request type")
	}
}

func (e *TestExecutor) executeHTTP(browser *testBrowser, index int, input sdk.NewRequest, expected sdk.PipeExpected) sdk.StepResult {
	step := sdk.StepResult{
		Index:  index,
		Type:   "request",
		Method: input.Method,
		URL:    input.URL,
		Match:  true,
	}

	req, err := buildTestHTTPRequest(input)
	if err != nil {
		step.Match = false
		step.Error = err.Error()
		return step
	}

	browser.apply(req)

	rec := httptest.NewRecorder()
	e.runtime.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()

	browser.capture(res)

	body, err := io.ReadAll(res.Body)
	if err != nil {
		step.Match = false
		step.Error = err.Error()
		return step
	}
	step.Response = snapshotHTTPResponse(res, body)

	if expected == nil {
		return step
	}

	exp, ok := expected.(sdk.NewResponseExpected)
	if !ok {
		if expPtr, ok := expected.(*sdk.NewResponseExpected); ok && expPtr != nil {
			exp = *expPtr
		} else {
			step.Match = false
			step.Error = "expected contract is not NewResponseExpected for HTTP request"
			return step
		}
	}

	step.Status = matchStatus(exp.Status, res.StatusCode)
	if !step.Status.Match {
		step.Match = false
	}

	if exp.Headers != nil {
		step.Headers = exp.Headers.MatchHeaders(res.Header)
		if !step.Headers.Match {
			step.Match = false
		}
	}

	if exp.Cookies != nil {
		step.Cookies = exp.Cookies.MatchCookies(res.Cookies())
		if !step.Cookies.Match {
			step.Match = false
		}
	}

	if exp.Body != nil {
		bodyRule := e.prepareBodyRule(exp.Body)
		if metadataRule, ok := bodyRule.(sdk.ExpectedHTTPBodyRule); ok {
			step.Body = metadataRule.MatchHTTPBody(res.Header.Get("Content-Type"), res.Header.Get("Content-Disposition"), body)
		} else {
			step.Body = bodyRule.MatchBody(body)
		}
		if !step.Body.Match {
			step.Match = false
		}
	}

	return step
}

func (e *TestExecutor) executeAction(browser *testBrowser, index int, input sdk.NewActionRequest, expected sdk.PipeExpected) sdk.StepResult {
	step := sdk.StepResult{
		Index:  index,
		Type:   "action",
		Method: input.Method,
		URL:    input.URL,
		Match:  true,
	}

	reqInput, err := buildActionHTTPRequestInput(input)
	if err != nil {
		step.Match = false
		step.Error = err.Error()
		return step
	}

	req, err := buildTestHTTPRequest(reqInput)
	if err != nil {
		step.Match = false
		step.Error = err.Error()
		return step
	}

	browser.apply(req)

	rec := httptest.NewRecorder()
	e.runtime.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()

	browser.capture(res)

	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		step.Match = false
		step.Error = err.Error()
		return step
	}
	step.Response = snapshotHTTPResponse(res, responseBody)

	actionState := strings.ToLower(strings.TrimSpace(res.Header.Get("Action-Response")))
	step.Status = matchStatus(http.StatusOK, res.StatusCode)
	if !step.Status.Match {
		step.Match = false
	}

	if actionState == "" {
		step.Match = false
		step.Body = sdk.BodyMatchResult{
			Rule:     "action-response-header",
			Response: string(responseBody),
			Match:    false,
			Message:  "missing Action-Response header",
		}
		return step
	}

	var actionError any
	var actionResult any
	if actionState == "error" {
		actionError = string(responseBody)
	} else {
		actionResult, err = decodeActionTestResult(res.Header, responseBody)
		if err != nil {
			step.Match = false
			step.Error = err.Error()
			return step
		}
	}

	if expected == nil {
		step.Action = sdk.ActionMatchResult{Match: true}
		return step
	}

	exp, ok := expected.(sdk.NewResponseActionExpected)
	if !ok {
		if expPtr, ok := expected.(*sdk.NewResponseActionExpected); ok && expPtr != nil {
			exp = *expPtr
		} else {
			step.Match = false
			step.Error = "expected contract is not NewResponseActionExpected for action request"
			return step
		}
	}

	actionMatch := sdk.ActionMatchResult{Match: true}
	if exp.Error != nil {
		actionMatch.Error = exp.Error.MatchActionError(actionError)
		if !actionMatch.Error.Match {
			actionMatch.Match = false
			step.Match = false
		}
	}
	if exp.Result != nil {
		actionMatch.Result = exp.Result.MatchActionResult(actionResult)
		if !actionMatch.Result.Match {
			actionMatch.Match = false
			step.Match = false
		}
	}
	step.Action = actionMatch

	return step
}

func decodeActionTestResult(headers http.Header, body []byte) (any, error) {
	actionType := strings.ToLower(strings.TrimSpace(headers.Get("Action-Type")))
	if actionType == "file" {
		return decodeSingleResponseFile(headers.Get("Content-Type"), headers.Get("Content-Disposition"), body)
	}
	if actionType == "files" {
		return decodeMultipartResponseFiles(headers.Get("Content-Type"), body)
	}
	if len(body) == 0 {
		return nil, nil
	}
	contentType := headers.Get("Content-Type")
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if mediaType == "application/json" {
		var value any
		if err := json.Unmarshal(body, &value); err == nil {
			return value, nil
		}
	}
	return string(body), nil
}

func decodeSingleResponseFile(contentType, disposition string, body []byte) (gox.File, error) {
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil || strings.TrimSpace(params["filename"]) == "" {
		return gox.File{}, fmt.Errorf("gox/test: file response has invalid Content-Disposition")
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return gox.File{}, fmt.Errorf("gox/test: file response has invalid Content-Type: %w", err)
	}
	return gox.NewFile(gox.FileConfig{Name: params["filename"], Type: mediaType, Content: bytes.NewReader(body)})
}

func decodeMultipartResponseFiles(contentType string, body []byte) ([]gox.File, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || params["boundary"] == "" {
		return nil, fmt.Errorf("gox/test: files response has invalid Content-Type")
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var files []gox.File
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if part.FileName() == "" {
			part.Close()
			return nil, fmt.Errorf("gox/test: files response contains a non-file part")
		}
		data, readErr := io.ReadAll(part)
		part.Close()
		if readErr != nil {
			return nil, readErr
		}
		file, createErr := gox.NewFile(gox.FileConfig{Name: part.FileName(), Type: part.Header.Get("Content-Type"), Content: bytes.NewReader(data)})
		if createErr != nil {
			return nil, createErr
		}
		files = append(files, file)
	}
	return files, nil
}

func (e *TestExecutor) prepareBodyRule(rule sdk.ExpectedBodyRule) sdk.ExpectedBodyRule {
	snapshot, ok := rule.(sdk.ExpectedBodySnapshot)
	if !ok {
		if snapshotPtr, ok := rule.(*sdk.ExpectedBodySnapshot); ok && snapshotPtr != nil {
			snapshot = *snapshotPtr
		} else {
			return rule
		}
	}

	path := strings.TrimSpace(snapshot.File)
	if path == "" {
		return snapshot
	}
	if filepath.IsAbs(path) || filepath.Clean(path) == ".." || strings.HasPrefix(filepath.ToSlash(filepath.Clean(path)), "../") {
		return sdk.ExpectedBodySnapshot{File: snapshot.File, Error: "snapshot path must remain inside tests/snapshots"}
	}
	if !filepath.IsAbs(path) {
		root := "tests"
		if e != nil && e.outputRoot != "" {
			root = e.outputRoot
		}
		parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
		if len(parts) > 0 && parts[0] == "tests" {
			parts = parts[1:]
		}
		if len(parts) > 0 && parts[0] == "snapshots" {
			parts = parts[1:]
		}
		path = filepath.Join(root, "snapshots", filepath.Join(parts...))
	}
	root := "tests"
	if e != nil && e.outputRoot != "" {
		root = e.outputRoot
	}
	snapshotRoot, _ := filepath.Abs(filepath.Join(root, "snapshots"))
	target, _ := filepath.Abs(path)
	rel, relErr := filepath.Rel(snapshotRoot, target)
	if relErr != nil || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return sdk.ExpectedBodySnapshot{File: snapshot.File, Error: "snapshot path must remain inside tests/snapshots"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return sdk.ExpectedBodySnapshot{File: snapshot.File, Error: "snapshot not found: " + path}
	}
	snapshot.Content = string(data)
	return snapshot
}

func buildActionHTTPRequestInput(input sdk.NewActionRequest) (sdk.NewRequest, error) {
	fields := make(sdk.FormData, len(input.Params))
	hasFiles := false
	query := make(map[string]string, len(input.Params))
	for index, value := range input.Params {
		key := strconv.Itoa(index)
		encoded, binary, err := encodeActionArgument(value)
		if err != nil {
			return sdk.NewRequest{}, fmt.Errorf("gox/test: Action argument %d: %w", index, err)
		}
		if binary {
			hasFiles = true
		}
		fieldKey := key
		if _, ok := encoded.([]gox.File); ok {
			fieldKey += "[]"
		}
		fields[fieldKey] = encoded
		if text, ok := encoded.(string); ok {
			query[key] = text
		}
	}
	body := sdk.RequestBodyValue(sdk.RequestBody.Query(query))
	if hasFiles {
		body = sdk.RequestBody.FormData(fields)
	}
	return sdk.NewRequest{
		Method: http.MethodPost,
		URL:    input.URL,
		Headers: sdk.RequestHeaders{
			"Invoke-Action": input.Method,
		},
		Body: body,
	}, nil
}

func encodeActionArgument(value any) (any, bool, error) {
	switch typed := value.(type) {
	case gox.File:
		return typed, true, nil
	case []gox.File:
		return typed, true, nil
	case string:
		return typed, false, nil
	case bool:
		return strconv.FormatBool(typed), false, nil
	case time.Time:
		return typed.Format(time.RFC3339Nano), false, nil
	case int:
		return strconv.Itoa(typed), false, nil
	case int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(typed), false, nil
	case nil:
		return "null", false, nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, false, err
		}
		return string(data), false, nil
	}
}

func buildTestHTTPRequest(input sdk.NewRequest) (*http.Request, error) {
	method := strings.TrimSpace(input.Method)
	if method == "" {
		method = http.MethodGet
	}

	rawURL := strings.TrimSpace(input.URL)
	if rawURL == "" {
		rawURL = "/"
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" && parsed.Host == "" {
		parsed.Scheme = "http"
		parsed.Host = "gox.test"
	}

	if len(input.URLParams) > 0 {
		q := parsed.Query()
		for key, value := range input.URLParams {
			q.Set(key, value)
		}
		parsed.RawQuery = q.Encode()
	}

	var bodyReader io.Reader
	if input.Body != nil {
		data, contentType, err := input.Body.Encode()
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
		if input.Headers == nil {
			input.Headers = sdk.RequestHeaders{}
		}
		if input.Headers["Content-Type"] == "" {
			input.Headers["Content-Type"] = contentType
		}
	}

	req, err := http.NewRequest(method, parsed.String(), bodyReader)
	if err != nil {
		return nil, err
	}

	for key, value := range input.Headers {
		req.Header.Set(key, value)
	}

	for key, value := range input.Cookies {
		req.AddCookie(&http.Cookie{Name: key, Value: value})
	}

	return req, nil
}

func snapshotHTTPResponse(res *http.Response, body []byte) sdk.ResponseSnapshot {
	if res == nil {
		return sdk.ResponseSnapshot{}
	}
	headers := make(map[string][]string, len(res.Header))
	for key, values := range res.Header {
		clone := make([]string, len(values))
		copy(clone, values)
		headers[key] = clone
	}
	cookies := make(map[string]string)
	for _, cookie := range res.Cookies() {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		cookies[cookie.Name] = cookie.Value
	}
	return sdk.ResponseSnapshot{
		StatusCode: res.StatusCode,
		Status:     res.Status,
		Headers:    headers,
		Cookies:    cookies,
		Body:       string(body),
	}
}

func matchStatus(expected int, actual int) sdk.MatchValue {
	if expected == 0 {
		return sdk.MatchValue{Expected: nil, Response: actual, Match: true, Rule: "status"}
	}
	return sdk.MatchValue{Expected: expected, Response: actual, Match: expected == actual, Rule: "status"}
}

func invalidTestStep(index int, message string) sdk.StepResult {
	return sdk.StepResult{
		Index: index,
		Type:  "error",
		Match: false,
		Error: message,
	}
}

type testBrowser struct {
	cookies map[string]*http.Cookie
}

func newTestBrowser() *testBrowser {
	return &testBrowser{cookies: make(map[string]*http.Cookie)}
}

func (b *testBrowser) clone() *testBrowser {
	clone := newTestBrowser()
	if b == nil {
		return clone
	}
	for name, cookie := range b.cookies {
		if cookie == nil {
			continue
		}
		copied := *cookie
		clone.cookies[name] = &copied
	}
	return clone
}

func (b *testBrowser) apply(req *http.Request) {
	if b == nil || req == nil || len(b.cookies) == 0 {
		return
	}
	for _, cookie := range b.cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}
}

func (b *testBrowser) setCookie(cookie *http.Cookie) {
	if b == nil || cookie == nil || cookie.Name == "" {
		return
	}
	clone := *cookie
	b.cookies[cookie.Name] = &clone
}

func (b *testBrowser) capture(res *http.Response) {
	if b == nil || res == nil {
		return
	}
	for _, cookie := range res.Cookies() {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		if cookie.MaxAge < 0 || cookie.Value == "" {
			delete(b.cookies, cookie.Name)
			continue
		}
		clone := *cookie
		b.cookies[cookie.Name] = &clone
	}
}
