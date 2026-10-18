package test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdktest "github.com/gox-lang/gox/test"

	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/project"
)

func TestSilenceTestLogsDisablesAndRestoresAllCommandLogs(t *testing.T) {
	previousLogs := logs.Enabled()
	previousRequests := logs.RequestEnabled()
	t.Cleanup(func() {
		logs.SetEnabled(previousLogs)
		logs.SetRequestEnabled(previousRequests)
	})

	logs.SetEnabled(true)
	logs.SetRequestEnabled(true)
	restore := silenceTestLogs()
	if logs.Enabled() || logs.RequestEnabled() {
		t.Fatalf("test logs remained enabled: general=%v request=%v", logs.Enabled(), logs.RequestEnabled())
	}
	restore()
	if !logs.Enabled() || !logs.RequestEnabled() {
		t.Fatalf("test logs were not restored: general=%v request=%v", logs.Enabled(), logs.RequestEnabled())
	}
}

func TestSilenceTestStdoutHidesApplicationOutputAndRestoresWriter(t *testing.T) {
	testStdoutMu.Lock()
	previous := os.Stdout
	captured, err := os.CreateTemp(t.TempDir(), "stdout-*.log")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = captured
	testStdoutMu.Unlock()
	t.Cleanup(func() {
		testStdoutMu.Lock()
		os.Stdout = previous
		_ = captured.Close()
		testStdoutMu.Unlock()
	})

	restore, err := silenceTestStdout()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprint(os.Stdout, "hidden")
	restore()
	_, _ = fmt.Fprint(os.Stdout, "visible")
	if err := captured.Sync(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(captured.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "visible" {
		t.Fatalf("captured stdout = %q, want %q", got, "visible")
	}
}

func TestSplitTestArgsLeavesRemovedFlagsUnknown(t *testing.T) {
	flags, positional := splitTestArgs([]string{"--race", "--cpuprofile", "cpu.out", "--memprofile", "mem.out"})
	if strings.Join(flags, ",") != "--race,--cpuprofile,--memprofile" {
		t.Fatalf("flags = %#v", flags)
	}
	if strings.Join(positional, ",") != "cpu.out,mem.out" {
		t.Fatalf("positional = %#v", positional)
	}
}

func TestSplitTestArgsSupportsGNUEqualsSyntax(t *testing.T) {
	flags, positional := splitTestArgs([]string{"tests/index.gox", "--run=Index", "--out=result.json"})
	if strings.Join(flags, ",") != "--run=Index,--out=result.json" {
		t.Fatalf("flags = %#v", flags)
	}
	if strings.Join(positional, ",") != "tests/index.gox" {
		t.Fatalf("positional = %#v", positional)
	}
}

func TestResolveTestInputPathDoesNotEscapeTests(t *testing.T) {
	p := &project.Project{RootDir: t.TempDir()}
	for _, input := range []string{"../outside.gox", filepath.Join(string(filepath.Separator), "tmp", "outside.gox")} {
		path := resolveTestInputPath(p, input)
		root := filepath.Join(p.RootDir, "tests")
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
			t.Fatalf("input %q escaped tests: %q", input, path)
		}
	}
}

func TestSplitTestArgsKeepsBenchFlagValueOutOfPositionals(t *testing.T) {
	flags, positional := splitTestArgs([]string{"tests/index.gox", "--bench", "Index", "--", "Literal"})

	wantFlags := []string{"--bench", "Index"}
	if len(flags) != len(wantFlags) {
		t.Fatalf("flags = %#v, want %#v", flags, wantFlags)
	}
	for i := range wantFlags {
		if flags[i] != wantFlags[i] {
			t.Fatalf("flags = %#v, want %#v", flags, wantFlags)
		}
	}

	wantPositionals := []string{"tests/index.gox", "Literal"}
	if len(positional) != len(wantPositionals) {
		t.Fatalf("positionals = %#v, want %#v", positional, wantPositionals)
	}
	for i := range wantPositionals {
		if positional[i] != wantPositionals[i] {
			t.Fatalf("positionals = %#v, want %#v", positional, wantPositionals)
		}
	}
}

func TestResultLabelUsesBenchForSuccessfulBenchmarks(t *testing.T) {
	if got := resultLabel(sdktest.ModeBench, true); got != "BENCH" {
		t.Fatalf("bench success label = %q, want BENCH", got)
	}
	if got := resultLabel(sdktest.ModeBench, false); got != "ERROR" {
		t.Fatalf("bench failure label = %q, want ERROR", got)
	}
	if got := runNoun(sdktest.ModeBench); got != "benchmarks" {
		t.Fatalf("bench noun = %q, want benchmarks", got)
	}
}

func TestPrintBenchSummaryIncludesTransferAndSortedStatus(t *testing.T) {
	res := testCaseResult{Result: &sdktest.ExecuteResult{Benchmark: &sdktest.BenchmarkResult{
		NsPerOp:           1234,
		OpsPerSec:         810.5,
		BytesRecvPerOp:    99,
		BytesSentPerOp:    55,
		HandlerAllocsOp:   2.5,
		HandlerBytesOp:    512,
		SampleRate:        0.01,
		AllocationSamples: 10,
		Status:            map[int]int{500: 1, 200: 3},
		AllocationSites:   []sdktest.BenchmarkAllocationSite{{File: "pages/index.gox", Line: 8, Name: "Index", Allocs: 10, Bytes: 2048}},
	}}}

	var out bytes.Buffer
	printBenchSummary(&out, res)
	text := out.String()
	for _, part := range []string{"metrics:", "ns/op              1234", "ops/sec            810.50", "bytes_received/op  99", "bytes_sent/op      55", "handler_allocs/op  2", "handler_bytes/op   512", "allocation sampling:", "1% (10 samples)", "status:", "200  3", "500  1", "allocation sites:", "pages/index.gox:8 Index", "10 allocs", "2048 B"} {
		if !strings.Contains(text, part) {
			t.Fatalf("bench summary missing %q:\n%s", part, text)
		}
	}
}
