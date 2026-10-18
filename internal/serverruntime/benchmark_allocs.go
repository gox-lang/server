package serverruntime

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/gox-lang/gox"
)

type benchmarkHandlerAllocSample struct {
	mu     sync.Mutex
	allocs uint64
	bytes  uint64
	sites  map[string]*benchmarkAllocationSite
}

type benchmarkAllocationSite struct {
	File   string
	Line   int
	Name   string
	Allocs uint64
	Bytes  uint64
	Source string
}

func (s *benchmarkHandlerAllocSample) add(allocs uint64, bytes uint64) {
	s.addAt(allocs, bytes, benchmarkAllocationSite{})
}

func (s *benchmarkHandlerAllocSample) addAt(allocs uint64, bytes uint64, site benchmarkAllocationSite) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allocs += allocs
	s.bytes += bytes
	if allocs == 0 && bytes == 0 {
		return
	}
	if site.File == "" && site.Name == "" {
		return
	}
	if site.File != "" {
		site.File = filepath.ToSlash(site.File)
	}
	key := site.File + ":" + site.Name
	if site.Line > 0 {
		key += ":" + strconvItoa(site.Line)
	}
	if s.sites == nil {
		s.sites = make(map[string]*benchmarkAllocationSite)
	}
	current := s.sites[key]
	if current == nil {
		copy := site
		current = &copy
		s.sites[key] = current
	}
	current.Allocs += allocs
	current.Bytes += bytes
}

func (s *benchmarkHandlerAllocSample) totals() (uint64, uint64) {
	if s == nil {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allocs, s.bytes
}

func (s *benchmarkHandlerAllocSample) snapshotSites() []benchmarkAllocationSite {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sites) == 0 {
		return nil
	}
	out := make([]benchmarkAllocationSite, 0, len(s.sites))
	for _, site := range s.sites {
		out = append(out, *site)
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

var benchmarkHandlerAllocState struct {
	mu      sync.Mutex
	current *benchmarkHandlerAllocSample
}

func beginBenchmarkHandlerAllocSample() *benchmarkHandlerAllocSample {
	sample := &benchmarkHandlerAllocSample{}
	benchmarkHandlerAllocState.mu.Lock()
	benchmarkHandlerAllocState.current = sample
	benchmarkHandlerAllocState.mu.Unlock()
	return sample
}

func endBenchmarkHandlerAllocSample(sample *benchmarkHandlerAllocSample) (uint64, uint64) {
	allocs, bytes, _ := endBenchmarkHandlerAllocSampleWithSites(sample)
	return allocs, bytes
}

func endBenchmarkHandlerAllocSampleWithSites(sample *benchmarkHandlerAllocSample) (uint64, uint64, []benchmarkAllocationSite) {
	benchmarkHandlerAllocState.mu.Lock()
	if benchmarkHandlerAllocState.current == sample {
		benchmarkHandlerAllocState.current = nil
	}
	benchmarkHandlerAllocState.mu.Unlock()
	allocs, bytes := sample.totals()
	return allocs, bytes, sample.snapshotSites()
}

func activeBenchmarkHandlerAllocSample() *benchmarkHandlerAllocSample {
	benchmarkHandlerAllocState.mu.Lock()
	defer benchmarkHandlerAllocState.mu.Unlock()
	return benchmarkHandlerAllocState.current
}

type benchmarkMemProfileSnapshot map[string]benchmarkMemProfileRecord

type benchmarkMemProfileRecord struct {
	Site         benchmarkAllocationSite
	AllocObjects int64
	AllocBytes   int64
}

var benchmarkMemProfileMu sync.Mutex
var benchmarkMemProfilePrimeSink [][]byte

func collectBenchmarkAllocationSites(count int, allowedFiles map[string]struct{}, run func() error) ([]benchmarkAllocationSite, error) {
	if count <= 0 || run == nil {
		return nil, nil
	}
	benchmarkMemProfileMu.Lock()
	defer benchmarkMemProfileMu.Unlock()

	previousRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = previousRate }()
	benchmarkPrimeMemProfileRate()

	runtime.GC()
	runtime.GC()
	before := benchmarkMemProfileSnapshotCurrent()
	for i := 0; i < count; i++ {
		if err := run(); err != nil {
			return nil, err
		}
	}
	runtime.GC()
	runtime.GC()
	after := benchmarkMemProfileSnapshotCurrent()

	sites := make([]benchmarkAllocationSite, 0)
	for key, afterRecord := range after {
		beforeRecord := before[key]
		allocs := afterRecord.AllocObjects - beforeRecord.AllocObjects
		bytes := afterRecord.AllocBytes - beforeRecord.AllocBytes
		if allocs <= 0 && bytes <= 0 {
			continue
		}
		site := afterRecord.Site
		if site.File == "" || site.Line <= 0 {
			continue
		}
		if !benchmarkAllocationSiteAllowed(site, allowedFiles) {
			continue
		}
		if allocs > 0 {
			site.Allocs = uint64(allocs)
		}
		if bytes > 0 {
			site.Bytes = uint64(bytes)
		}
		sites = append(sites, site)
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Bytes != sites[j].Bytes {
			return sites[i].Bytes > sites[j].Bytes
		}
		if sites[i].Allocs != sites[j].Allocs {
			return sites[i].Allocs > sites[j].Allocs
		}
		if sites[i].File != sites[j].File {
			return sites[i].File < sites[j].File
		}
		return sites[i].Line < sites[j].Line
	})
	return sites, nil
}

func benchmarkPrimeMemProfileRate() {
	// runtime.MemProfileRate is global and the runtime keeps per-P sampling
	// thresholds. After lowering the rate for a benchmark collection, force enough
	// fresh allocations to cross any threshold that was computed with the previous
	// rate. These allocations happen before the baseline snapshot, so they are not
	// reported as benchmark sites.
	const blocks = 64
	const blockSize = 16 << 10
	bufs := make([][]byte, blocks)
	for i := range bufs {
		bufs[i] = make([]byte, blockSize)
	}
	benchmarkMemProfilePrimeSink = bufs
}

func benchmarkMemProfileSnapshotCurrent() benchmarkMemProfileSnapshot {
	records := make([]runtime.MemProfileRecord, 1024)
	for {
		n, ok := runtime.MemProfile(records, true)
		if ok {
			records = records[:n]
			break
		}
		if n <= len(records) {
			n = len(records) * 2
		}
		records = make([]runtime.MemProfileRecord, n)
	}

	snapshot := make(benchmarkMemProfileSnapshot)
	for i := range records {
		record := records[i]
		site, ok := benchmarkAllocationSiteFromStack(record.Stack())
		if !ok {
			continue
		}
		key := benchmarkRuntimeAllocationSiteKey(site)
		existing := snapshot[key]
		existing.Site = site
		existing.AllocObjects += record.AllocObjects
		existing.AllocBytes += record.AllocBytes
		snapshot[key] = existing
	}
	return snapshot
}

func benchmarkAllocationSiteFromStack(pcs []uintptr) (benchmarkAllocationSite, bool) {
	for _, pc := range pcs {
		lookupPC := pc
		if lookupPC > 0 {
			lookupPC--
		}
		fn := runtime.FuncForPC(lookupPC)
		if fn == nil {
			fn = runtime.FuncForPC(pc)
		}
		if fn == nil {
			continue
		}
		file, line := fn.FileLine(lookupPC)
		if line <= 0 {
			file, line = fn.FileLine(pc)
		}
		file = filepath.ToSlash(file)
		if !isBenchmarkGoxSourceFile(file) {
			continue
		}
		name := shortBenchmarkFuncName(fn.Name())
		return benchmarkAllocationSite{File: file, Line: line, Name: name}, true
	}
	return benchmarkAllocationSite{}, false
}

func isBenchmarkGoxSourceFile(file string) bool {
	file = filepath.ToSlash(strings.TrimSpace(file))
	return strings.HasSuffix(file, ".gox") || strings.Contains(file, ".gox:")
}

func benchmarkAllocationSiteAllowed(site benchmarkAllocationSite, allowedFiles map[string]struct{}) bool {
	if len(allowedFiles) == 0 {
		return false
	}
	file := filepath.ToSlash(strings.TrimSpace(site.File))
	if file == "" {
		return false
	}
	for allowed := range allowedFiles {
		allowed = filepath.ToSlash(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		if file == allowed || strings.HasSuffix(file, "/"+allowed) {
			return true
		}
	}
	return false
}

func benchmarkRuntimeAllocationSiteKey(site benchmarkAllocationSite) string {
	return filepath.ToSlash(site.File) + ":" + strconvItoa(site.Line) + ":" + site.Name + ":" + site.Source
}

func inferBenchmarkAllocationSitesFromGenerated(generatedPath string, sourceFile string, handlerName string, allocs uint64, bytes uint64) ([]benchmarkAllocationSite, error) {
	generatedPath = strings.TrimSpace(generatedPath)
	sourceFile = filepath.ToSlash(strings.TrimSpace(sourceFile))
	handlerName = cleanBenchmarkAllocationSiteName(handlerName)
	if handlerName == "" {
		handlerName = "handler"
	}
	if generatedPath == "" || sourceFile == "" || (allocs == 0 && bytes == 0) {
		return nil, nil
	}
	file, err := os.Open(generatedPath)
	if err != nil {
		return nil, nil
	}
	defer file.Close()

	currentSource := ""
	currentLine := 0
	bestLine := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if src, ln, ok := parseBenchmarkLineDirective(line); ok {
			currentSource = filepath.ToSlash(src)
			currentLine = ln
			continue
		}
		if currentLine <= 0 || currentSource == "" || !benchmarkGeneratedSourceMatches(currentSource, sourceFile) {
			continue
		}
		if benchmarkGeneratedLineMayAllocate(line) {
			bestLine = currentLine
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if bestLine <= 0 {
		return nil, nil
	}
	return []benchmarkAllocationSite{{
		File:   sourceFile,
		Line:   bestLine,
		Name:   handlerName,
		Allocs: allocs,
		Bytes:  bytes,
		Source: "inferred",
	}}, nil
}

func parseBenchmarkLineDirective(line string) (string, int, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "//line ") {
		return "", 0, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "//line "))
	last := strings.LastIndex(payload, ":")
	if last < 0 {
		return "", 0, false
	}
	payload = payload[:last]
	second := strings.LastIndex(payload, ":")
	if second < 0 {
		return "", 0, false
	}
	file := strings.TrimSpace(payload[:second])
	lineNo, ok := benchmarkAtoi(strings.TrimSpace(payload[second+1:]))
	if !ok || file == "" || lineNo <= 0 {
		return "", 0, false
	}
	return file, lineNo, true
}

func benchmarkGeneratedSourceMatches(generatedSource string, sourceFile string) bool {
	generatedSource = filepath.ToSlash(strings.TrimSpace(generatedSource))
	sourceFile = filepath.ToSlash(strings.TrimSpace(sourceFile))
	return generatedSource == sourceFile || strings.HasSuffix(generatedSource, "/"+sourceFile) || strings.HasSuffix(sourceFile, "/"+generatedSource)
}

func benchmarkGeneratedLineMayAllocate(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "//") {
		return false
	}
	if strings.Contains(line, ":=") && strings.Contains(line, " + ") && (strings.Contains(line, "\"") || strings.Contains(line, "`")) {
		return true
	}
	patterns := []string{
		"make(", "new(", "append(", "fmt.Sprintf(", "strings.Builder", "bytes.Buffer",
		"[]byte(", "string(", "map[", "&", "{",
	}
	for _, pattern := range patterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}
	return false
}

func benchmarkAtoi(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	out := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
		out = out*10 + int(r-'0')
	}
	return out, true
}

func benchmarkHandlerAllocationSite(route *GoxRouteRuntime, handler func(*gox.Context), ctx *gox.Context) benchmarkAllocationSite {
	file := ""
	line := 0
	name := ""

	if handler != nil {
		pc := reflect.ValueOf(handler).Pointer()
		if fn := runtime.FuncForPC(pc); fn != nil {
			name = shortBenchmarkFuncName(fn.Name())
			file, line = fn.FileLine(pc)
		}
	}
	if route != nil {
		if route.RelPath != "" {
			file = route.RelPath
		} else if route.AbsPath != "" {
			file = route.AbsPath
		}
		if name == "" && route.RoutePath != "" {
			name = route.RoutePath
		}
	}
	if ctx != nil && ctx.Request.Route.Method() != "" {
		method := ctx.Request.Route.Method()
		if name == "" {
			name = method
		} else if !strings.Contains(name, method) {
			name += " " + method
		}
	}
	return benchmarkAllocationSite{File: file, Line: line, Name: name}
}

func shortBenchmarkFuncName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if idx := strings.LastIndex(name, "/"); idx >= 0 && idx+1 < len(name) {
		name = name[idx+1:]
	}
	name = strings.TrimPrefix(name, "main.")
	name = strings.TrimPrefix(name, "gox.")
	return name
}

func strconvItoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	v := value
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
