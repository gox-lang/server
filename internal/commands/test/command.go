package test

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gox-lang/gox/server"
	sdktest "github.com/gox-lang/gox/test"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/project"
	goxruntime "github.com/gox-lang/server/internal/runtime"
	"github.com/gox-lang/server/internal/serverruntime"
	"github.com/gox-lang/server/internal/toolchain"
)

type Command struct{}

var testStdoutMu sync.Mutex

func New() *Command { return &Command{} }

func (c *Command) Name() string { return "test" }

func (c *Command) Description() string { return "Run GOX tests and benchmarks" }

func (c *Command) Help() string {
	return `gox test

Run GOX tests and benchmarks.

Usage:
  gox test [files] [options]

Options:
  --run <pattern>        Run tests matching pattern
  --bench <pattern>      Run benchmarks matching pattern
  -v                     Print each test result
  --out <file>           Write the JSON result
  -h, --help             Show help information`
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	restoreLogs := silenceTestLogs()
	defer restoreLogs()

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	run := fs.String("run", "", "run tests matching pattern")
	bench := fs.String("bench", "", "run benchmarks matching pattern")
	verbose := fs.Bool("v", false, "verbose output")
	out := fs.String("out", "", "write global JSON result")

	flagArgs, positionalArgs := splitTestArgs(args)
	var err error
	flagArgs, err = cli.NormalizeOptions(flagArgs, map[string]struct{}{"v": {}}, map[string]struct{}{"run": {}, "bench": {}, "out": {}})
	if err != nil {
		return err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if strings.TrimSpace(*run) != "" && strings.TrimSpace(*bench) != "" {
		return fmt.Errorf("gox test: --run and --bench cannot be used together")
	}
	if !*verbose {
		restoreStdout, err := silenceTestStdout()
		if err != nil {
			return err
		}
		defer restoreStdout()
	}

	p, err := project.Detect(ctx.WorkDir)
	if err != nil {
		return err
	}
	if err := p.UseAsWorkingDir(); err != nil {
		return err
	}

	fileArgs, positionalRun, err := splitTestSelection(p, positionalArgs, *run)
	if err != nil {
		return err
	}

	testFiles, err := discoverTestFiles(p, fileArgs)
	if err != nil {
		return err
	}
	if len(testFiles) == 0 {
		return fmt.Errorf("no .gox test files found in %s", filepath.Join(p.RootDir, "tests"))
	}

	mode := sdktest.ModeTest
	effectiveRun := strings.TrimSpace(*run)
	if strings.TrimSpace(*bench) != "" {
		mode = sdktest.ModeBench
		effectiveRun = strings.TrimSpace(*bench)
	}
	if effectiveRun == "" && positionalRun != "" {
		effectiveRun = positionalRun
	}
	if effectiveRun == "." {
		effectiveRun = ""
	}

	var runRE *regexp.Regexp
	if effectiveRun != "" {
		runRE, err = regexp.Compile(effectiveRun)
		if err != nil {
			flagName := "--run"
			if mode == sdktest.ModeBench {
				flagName = "--bench"
			}
			return fmt.Errorf("invalid %s pattern: %w", flagName, err)
		}
	}

	tc := toolchain.New(buildinfo.GoVersion)
	paths, err := toolchain.Ensure(tc)
	if err != nil {
		return err
	}

	loadedConfig, err := goxruntime.CompileConfigPlugin(p, paths, ctx.Stdout)
	if err != nil {
		return err
	}
	rt, err := serverruntime.New(loadedConfig.Config, ctx.Stdout, p, paths, loadedConfig.CompileResult)
	if err != nil {
		return err
	}
	if err := goxruntime.CleanupConfigBuildArtifacts(p.RootDir, loadedConfig); err != nil {
		return err
	}

	baseConfig := loadedConfig.Config
	sdktest.SetExecutor(serverruntime.NewTestExecutorWithOptions(rt, serverruntime.TestExecutorOptions{
		OutputRoot: filepath.Join(p.RootDir, "tests"),
		BaseConfig: &baseConfig,
		RuntimeFactory: func(cfg server.Config) (*serverruntime.Runtime, error) {
			return serverruntime.New(cfg, ctx.Stdout, p, paths, loadedConfig.CompileResult)
		},
	}))
	defer sdktest.SetExecutor(nil)

	runResult := testRunResult{
		StartedAt: time.Now(),
		Files:     testFiles,
	}

	for _, file := range testFiles {
		loaded, err := goxruntime.CompileTestPlugin(p, paths, file, ctx.Stdout)
		if err != nil {
			res := testCaseResult{File: p.Rel(file), Match: false, Error: err.Error(), Messages: []string{err.Error()}}
			runResult.Tests = append(runResult.Tests, res)
			runResult.Failed++
			fmt.Fprintf(ctx.Stdout, "FAIL %s\n", res.File)
			fmt.Fprintf(ctx.Stdout, "  %s\n", res.Error)
			continue
		}
		if err := goxruntime.CleanupTestBuildArtifacts(loaded); err != nil {
			return err
		}

		names := make([]string, 0, len(loaded.Tests))
		for name := range loaded.Tests {
			if runRE == nil || runRE.MatchString(name) {
				names = append(names, name)
			}
		}
		sort.Strings(names)

		for _, name := range names {
			res := runTestFunc(p.Rel(file), name, loaded.Tests[name], filepath.Join(p.RootDir, "tests", "results"), mode)
			runResult.Tests = append(runResult.Tests, res)
			if mode == sdktest.ModeBench {
				runResult.Benchmarks++
				fmt.Fprintf(ctx.Stdout, "BENCH %s::%s\n", res.File, res.Name)
				for _, message := range res.Messages {
					fmt.Fprintf(ctx.Stdout, "  %s\n", message)
				}
				if res.Match {
					if len(res.Messages) == 0 {
						printBenchSummary(ctx.Stdout, res)
					}
				} else {
					runResult.Failed++
					if res.Error != "" && len(res.Messages) == 0 {
						fmt.Fprintf(ctx.Stdout, "  error: %s\n", res.Error)
					}
				}
			} else if res.Match {
				runResult.Passed++
				fmt.Fprintf(ctx.Stdout, resultLabel(mode, true)+" %s::%s\n", res.File, res.Name)
				for _, message := range res.Messages {
					fmt.Fprintf(ctx.Stdout, "  %s\n", message)
				}
			} else {
				runResult.Failed++
				fmt.Fprintf(ctx.Stdout, resultLabel(mode, false)+" %s::%s\n", res.File, res.Name)
				for _, message := range res.Messages {
					fmt.Fprintf(ctx.Stdout, "  %s\n", message)
				}
				if res.Error != "" && len(res.Messages) == 0 {
					fmt.Fprintf(ctx.Stdout, "  %s\n", res.Error)
				}
			}
			if *verbose {
				if len(res.Results) == 0 && res.Result != nil {
					res.Results = []sdktest.ExecuteResult{*res.Result}
				}
				for i := range res.Results {
					data, _ := json.MarshalIndent(res.Results[i], "  ", "  ")
					fmt.Fprintf(ctx.Stdout, "  result[%d]: %s\n", i+1, data)
				}
			}
		}
	}

	if len(runResult.Tests) == 0 {
		if runRE != nil {
			return fmt.Errorf("no %s matched %q", runNoun(mode), effectiveRun)
		}
		return fmt.Errorf("no %s matched", runNoun(mode))
	}

	runResult.FinishedAt = time.Now()
	runResult.DurationMS = float64(runResult.FinishedAt.Sub(runResult.StartedAt)) / float64(time.Millisecond)
	runResult.Match = runResult.Failed == 0

	if mode == sdktest.ModeBench {
		fmt.Fprintf(ctx.Stdout, "\n%d benchmark", runResult.Benchmarks)
		if runResult.Benchmarks != 1 {
			fmt.Fprint(ctx.Stdout, "s")
		}
		fmt.Fprintf(ctx.Stdout, "\n%d error", runResult.Failed)
		if runResult.Failed != 1 {
			fmt.Fprint(ctx.Stdout, "s")
		}
		fmt.Fprintln(ctx.Stdout)
	} else {
		fmt.Fprintf(ctx.Stdout, "\n%d passed\n%d failed\n", runResult.Passed, runResult.Failed)
	}

	if *out != "" {
		outputPath := resolveTestOutputPath(p, *out)
		if err := validateTestInputPath(p, outputPath); err != nil {
			return fmt.Errorf("invalid --out path: %w", err)
		}
		if err := writeRunOutput(outputPath, runResult); err != nil {
			return err
		}
	}

	if runResult.Failed > 0 {
		return cli.SilentExit(1)
	}
	return nil
}

func silenceTestLogs() func() {
	previousLogs := logs.Enabled()
	previousRequestLogs := logs.RequestEnabled()
	logs.SetEnabled(false)
	logs.SetRequestEnabled(false)
	return func() {
		logs.SetRequestEnabled(previousRequestLogs)
		logs.SetEnabled(previousLogs)
	}
}

// silenceTestStdout hides output written directly by application code while the
// test runtime is active. Command results still use cli.Context.Stdout, whose
// writer is captured before this redirection. Verbose mode intentionally skips
// this redirection so application diagnostics remain available when requested.
func silenceTestStdout() (func(), error) {
	testStdoutMu.Lock()

	previous := os.Stdout
	previousLogOutput := log.Writer()
	discard, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		testStdoutMu.Unlock()
		return nil, fmt.Errorf("silence test stdout: %w", err)
	}
	os.Stdout = discard
	log.SetOutput(discard)

	return func() {
		log.SetOutput(previousLogOutput)
		os.Stdout = previous
		_ = discard.Close()
		testStdoutMu.Unlock()
	}, nil
}

func printBenchSummary(out io.Writer, res testCaseResult) {
	if out == nil {
		return
	}
	var result *sdktest.ExecuteResult
	if res.Result != nil {
		result = res.Result
	} else if len(res.Results) > 0 {
		result = &res.Results[len(res.Results)-1]
	}
	if result == nil || result.Benchmark == nil {
		return
	}
	bench := result.Benchmark
	if len(bench.Steps) > 1 {
		fmt.Fprintln(out, "  pipeline:")
		fmt.Fprintf(out, "    iterations    %d\n", bench.Iterations)
		fmt.Fprintf(out, "    steps         %d\n", len(bench.Steps))
		fmt.Fprintf(out, "    measured_ops  %d\n", bench.Iterations*len(bench.Steps))
	}
	fmt.Fprintln(out, "  metrics:")
	fmt.Fprintf(out, "    ns/op              %.0f\n", bench.NsPerOp)
	fmt.Fprintf(out, "    ops/sec            %.2f\n", bench.OpsPerSec)
	fmt.Fprintf(out, "    bytes_received/op  %.0f\n", bench.BytesRecvPerOp)
	fmt.Fprintf(out, "    bytes_sent/op      %.0f\n", bench.BytesSentPerOp)
	fmt.Fprintf(out, "    handler_allocs/op  %.0f\n", bench.HandlerAllocsOp)
	fmt.Fprintf(out, "    handler_bytes/op   %.0f\n", bench.HandlerBytesOp)
	if len(bench.Status) > 0 {
		keys := make([]int, 0, len(bench.Status))
		for status := range bench.Status {
			keys = append(keys, status)
		}
		sort.Ints(keys)
		fmt.Fprintln(out, "  status:")
		for _, status := range keys {
			fmt.Fprintf(out, "    %d  %d\n", status, bench.Status[status])
		}
	}
	if bench.AllocationSamples > 0 {
		percent := bench.SampleRate * 100
		rate := fmt.Sprintf("%.2f%%", percent)
		if percent == float64(int(percent)) {
			rate = fmt.Sprintf("%d%%", int(percent))
		}
		fmt.Fprintln(out, "  allocation sampling:")
		fmt.Fprintf(out, "    %s (%d samples)\n", rate, bench.AllocationSamples)
	}

	if len(bench.Steps) > 1 {
		fmt.Fprintln(out, "  steps:")
		for i, step := range bench.Steps {
			if i > 0 {
				fmt.Fprintln(out)
			}
			writeBenchmarkStepSummary(out, step)
		}
	}

	if len(bench.AllocationSites) > 0 && len(bench.Steps) <= 1 {
		fmt.Fprintln(out, "  allocation sites:")
		limit := len(bench.AllocationSites)
		if limit > 5 {
			limit = 5
		}
		for i := 0; i < limit; i++ {
			site := bench.AllocationSites[i]
			location := site.File
			if site.Line > 0 {
				location = fmt.Sprintf("%s:%d", location, site.Line)
			}
			name := cleanBenchmarkSiteName(site.Name)
			if name != "" {
				location = strings.TrimSpace(location + " " + name)
			}
			fmt.Fprintf(out, "    %d. %s  %d allocs  %d B\n", i+1, location, site.Allocs, site.Bytes)
		}
	}
}

func writeBenchmarkStepSummary(out io.Writer, step sdktest.BenchmarkStepResult) {
	fmt.Fprintf(out, "    %d. %s\n", step.Index, benchmarkStepLabel(step))
	fmt.Fprintln(out, "       metrics")
	fmt.Fprintf(out, "         ns/op               %.0f\n", step.NsPerOp)
	fmt.Fprintf(out, "         ops/sec             %.2f\n", step.OpsPerSec)
	fmt.Fprintf(out, "         bytes_received/op   %.0f\n", step.BytesRecvPerOp)
	fmt.Fprintf(out, "         bytes_sent/op       %.0f\n", step.BytesSentPerOp)
	fmt.Fprintf(out, "         handler_allocs/op   %.0f\n", step.HandlerAllocsOp)
	fmt.Fprintf(out, "         handler_bytes/op    %.0f\n", step.HandlerBytesOp)
	if len(step.Status) > 0 {
		fmt.Fprintln(out, "       status")
		keys := make([]int, 0, len(step.Status))
		for code := range step.Status {
			keys = append(keys, code)
		}
		sort.Ints(keys)
		for _, code := range keys {
			fmt.Fprintf(out, "         %d  %d\n", code, step.Status[code])
		}
	}
	if step.AllocationSamples > 0 {
		fmt.Fprintln(out, "       allocation sampling")
		fmt.Fprintf(out, "         %d samples\n", step.AllocationSamples)
	}
	if len(step.AllocationSites) > 0 {
		fmt.Fprintln(out, "       allocation sites")
		limit := len(step.AllocationSites)
		if limit > 3 {
			limit = 3
		}
		for i := 0; i < limit; i++ {
			site := step.AllocationSites[i]
			location := site.File
			if site.Line > 0 {
				location = fmt.Sprintf("%s:%d", location, site.Line)
			}
			name := cleanBenchmarkSiteName(site.Name)
			if name != "" {
				location = strings.TrimSpace(location + " " + name)
			}
			fmt.Fprintf(out, "         %d. %s  %d allocs  %d B\n", i+1, location, site.Allocs, site.Bytes)
		}
	}
}

func benchmarkStepLabel(step sdktest.BenchmarkStepResult) string {
	kind := strings.ToLower(strings.TrimSpace(step.Type))
	if kind == "" {
		kind = "step"
	}
	parts := []string{kind}
	if method := strings.TrimSpace(step.Method); method != "" {
		parts = append(parts, method)
	}
	if url := strings.TrimSpace(step.URL); url != "" {
		parts = append(parts, url)
	}
	return strings.Join(parts, " ")
}
func benchmarkStatusSummary(status map[int]int) string {
	if len(status) == 0 {
		return ""
	}
	keys := make([]int, 0, len(status))
	for code := range status {
		keys = append(keys, code)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, code := range keys {
		parts = append(parts, fmt.Sprintf("%d=%d", code, status[code]))
	}
	return strings.Join(parts, ",")
}

func cleanBenchmarkSiteName(name string) string {
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

func resultLabel(mode sdktest.Mode, match bool) string {
	if mode == sdktest.ModeBench {
		if match {
			return "BENCH"
		}
		return "ERROR"
	}
	if match {
		return "PASS"
	}
	return "FAIL"
}

func runNoun(mode sdktest.Mode) string {
	if mode == sdktest.ModeBench {
		return "benchmarks"
	}
	return "tests"
}

func splitTestSelection(p *project.Project, positional []string, runFlag string) ([]string, string, error) {
	if len(positional) == 0 {
		return nil, "", nil
	}
	if strings.TrimSpace(runFlag) != "" && len(positional) > 1 {
		return nil, "", fmt.Errorf("test name positional filters cannot be combined with --run")
	}

	fileArgs := []string{positional[0]}
	if len(positional) == 1 {
		return fileArgs, "", nil
	}

	// gox test <file> <test-name>
	// The first positional argument selects a file/directory inside tests/.
	// Remaining positional arguments select test names, not more files.
	firstPath := resolveTestInputPath(p, positional[0])
	if _, err := os.Stat(firstPath); err != nil {
		return nil, "", describeMissingTestInput(p, positional[0], firstPath, err)
	}

	patterns := make([]string, 0, len(positional)-1)
	for _, name := range positional[1:] {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		patterns = append(patterns, regexp.QuoteMeta(name))
	}
	if len(patterns) == 0 {
		return fileArgs, "", nil
	}
	return fileArgs, strings.Join(patterns, "|"), nil
}

func splitTestArgs(args []string) ([]string, []string) {
	flagArgs := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		switch arg {
		case "--run", "--bench", "--out":
			if i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		}
	}

	return flagArgs, positional
}

type testRunResult struct {
	Match      bool             `json:"match"`
	Passed     int              `json:"passed"`
	Failed     int              `json:"failed"`
	Benchmarks int              `json:"benchmarks,omitempty"`
	Files      []string         `json:"files"`
	Tests      []testCaseResult `json:"tests"`
	StartedAt  time.Time        `json:"startedAt"`
	FinishedAt time.Time        `json:"finishedAt"`
	DurationMS float64          `json:"durationMs"`
}

type testCaseResult struct {
	File     string                  `json:"file"`
	Name     string                  `json:"name,omitempty"`
	Match    bool                    `json:"match"`
	Result   *sdktest.ExecuteResult  `json:"result,omitempty"`
	Results  []sdktest.ExecuteResult `json:"results,omitempty"`
	Messages []string                `json:"messages,omitempty"`
	Error    string                  `json:"error,omitempty"`
}

func runTestFunc(file string, name string, fn func(), resultRoot string, mode sdktest.Mode) (out testCaseResult) {
	out = testCaseResult{File: file, Name: name, Match: true}
	var recorded []sdktest.ExecuteResult
	sdktest.SetRuntimeContext(sdktest.RuntimeContext{
		File:       file,
		Name:       name,
		Mode:       mode,
		ResultRoot: resultRoot,
	})
	sdktest.SetResultRecorder(func(result sdktest.ExecuteResult) {
		recorded = append(recorded, result)
	})
	defer sdktest.SetResultRecorder(nil)
	defer sdktest.ClearRuntimeContext()
	defer func() {
		out.Results = recorded
		if len(recorded) == 1 {
			out.Result = &recorded[0]
		}

		if recovered := recover(); recovered != nil {
			out.Match = false
			switch v := recovered.(type) {
			case sdktest.Failure:
				out.Error = v.Message
			case *sdktest.Failure:
				if v != nil {
					out.Error = v.Message
				}
			case sdktest.ExecuteResult:
				out.Result = &v
				out.Results = append(out.Results, v)
				out.Error = sdktest.Format(v)
			case *sdktest.ExecuteResult:
				out.Result = v
				if v != nil {
					out.Results = append(out.Results, *v)
					out.Error = sdktest.Format(*v)
				}
			default:
				out.Error = fmt.Sprint(recovered)
			}
		} else {
			for i := range recorded {
				if !recorded[i].Match {
					out.Match = false
					if out.Result == nil {
						out.Result = &recorded[i]
					}
					if out.Error == "" {
						out.Error = sdktest.Format(recorded[i])
					}
				}
			}
		}

		messages := sdktest.CurrentMessages()
		if len(messages.Fail) > 0 {
			out.Match = false
		}
		if mode == sdktest.ModeBench {
			if !out.Match {
				out.Messages = append(out.Messages, messages.Fail...)
				if out.Error != "" && len(out.Messages) == 0 && len(messages.Explain) == 0 {
					out.Messages = append(out.Messages, out.Error)
				}
			}
		} else if out.Match {
			out.Messages = append(out.Messages, messages.Pass...)
		} else {
			out.Messages = append(out.Messages, messages.Fail...)
			if out.Error != "" && len(out.Messages) == 0 && len(messages.Explain) == 0 {
				out.Messages = append(out.Messages, out.Error)
			}
		}
		out.Messages = append(out.Messages, messages.Explain...)
		if out.Error == "" && len(messages.Explain) > 0 {
			out.Error = strings.Join(messages.Explain, "\n")
		}
	}()
	fn()
	return out
}

func discoverTestFiles(p *project.Project, args []string) ([]string, error) {
	if len(args) == 0 {
		root := filepath.Join(p.RootDir, "tests")
		var files []string
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if strings.EqualFold(filepath.Ext(path), ".gox") {
				if err := validateTestInputPath(p, path); err != nil {
					return err
				}
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
		sort.Strings(files)
		return files, nil
	}

	files := make([]string, 0, len(args))
	seen := map[string]struct{}{}
	for _, arg := range args {
		path := resolveTestInputPath(p, arg)
		if err := validateTestInputPath(p, path); err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, describeMissingTestInput(p, arg, path, err)
		}
		if info.IsDir() {
			err := filepath.WalkDir(path, func(child string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				if strings.EqualFold(filepath.Ext(child), ".gox") {
					if _, ok := seen[child]; !ok {
						seen[child] = struct{}{}
						files = append(files, child)
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		if !strings.EqualFold(filepath.Ext(path), ".gox") {
			return nil, fmt.Errorf("test file must have .gox extension: %s", arg)
		}
		if _, ok := seen[path]; !ok {
			seen[path] = struct{}{}
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files, nil
}

func validateTestInputPath(p *project.Project, path string) error {
	root, err := filepath.Abs(filepath.Join(p.RootDir, "tests"))
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return fmt.Errorf("test input must be inside tests/: %s", p.Rel(path))
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		resolvedRel, relErr := filepath.Rel(root, resolved)
		if relErr != nil || resolvedRel == ".." || strings.HasPrefix(filepath.ToSlash(resolvedRel), "../") {
			return fmt.Errorf("test input symlink must remain inside tests/: %s", p.Rel(path))
		}
	} else {
		parent := filepath.Dir(target)
		for parent != filepath.Dir(parent) {
			if resolvedParent, parentErr := filepath.EvalSymlinks(parent); parentErr == nil {
				resolvedRel, relErr := filepath.Rel(root, resolvedParent)
				if relErr != nil || resolvedRel == ".." || strings.HasPrefix(filepath.ToSlash(resolvedRel), "../") {
					return fmt.Errorf("test path symlink must remain inside tests/: %s", p.Rel(path))
				}
				break
			}
			parent = filepath.Dir(parent)
		}
	}
	return nil
}

func describeMissingTestInput(p *project.Project, input string, resolved string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("test file not found: %s", p.Rel(resolved))
	}
	return fmt.Errorf("cannot access test input %q: %w", input, err)
}

func resolveTestInputPath(p *project.Project, input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return filepath.Join(p.RootDir, "tests")
	}
	if filepath.IsAbs(input) {
		return filepath.Join(p.RootDir, "tests", "__invalid_absolute_path__")
	}
	clean := filepath.Clean(input)
	if clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
		return filepath.Join(p.RootDir, "tests", "__invalid_parent_path__")
	}
	parts := strings.Split(filepath.ToSlash(clean), "/")
	if len(parts) > 0 && parts[0] == "tests" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return filepath.Join(p.RootDir, "tests")
	}
	path := filepath.Join(append([]string{p.RootDir, "tests"}, parts...)...)
	if filepath.Ext(path) == "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		return path + ".gox"
	}
	return path
}

func resolveTestOutputPath(p *project.Project, output string) string {
	output = strings.TrimSpace(output)
	if output == "" || filepath.IsAbs(output) {
		return output
	}
	clean := filepath.Clean(output)
	parts := strings.Split(filepath.ToSlash(clean), "/")
	if len(parts) > 0 && parts[0] == "tests" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return filepath.Join(p.RootDir, "tests")
	}
	return filepath.Join(append([]string{p.RootDir, "tests"}, parts...)...)
}

func writeRunOutput(path string, result testRunResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
