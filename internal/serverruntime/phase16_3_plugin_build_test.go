package serverruntime

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"slices"
	"strings"
	"testing"
)

func TestPhase16_3PluginBuildUsesStrippedLinkerOutput(t *testing.T) {
	pluginPath := filepath.Join("build", "page.so")
	generatedPath := filepath.Join("build", "page.go")
	got := goxPluginBuildArgs(pluginPath, generatedPath)
	want := []string{
		"build",
		"-buildmode=plugin",
		"-ldflags=-s -w",
		"-o", pluginPath,
		generatedPath,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plugin build args = %#v, want %#v", got, want)
	}
}

func TestPhase16_3StrippedPluginRemainsLoadable(t *testing.T) {
	if stdruntime.GOOS == "windows" {
		t.Skip("Go plugins are unavailable on Windows")
	}

	goBinary := filepath.Join(stdruntime.GOROOT(), "bin", "go")
	if stdruntime.GOOS == "windows" {
		goBinary += ".exe"
	}
	if info, err := os.Stat(goBinary); err != nil || info.IsDir() {
		t.Skipf("Go binary unavailable: %v", err)
	}

	root := t.TempDir()
	pluginSource := filepath.Join(root, "plugin.go")
	pluginPath := filepath.Join(root, "phase16_3.so")
	hostSource := filepath.Join(root, "host.go")
	hostPath := filepath.Join(root, "host")
	if stdruntime.GOOS == "windows" {
		hostPath += ".exe"
	}

	writePhase16_3File(t, pluginSource, `package main

var Phase16_3Value = 42
`)
	writePhase16_3File(t, hostSource, `package main

import (
	"fmt"
	"os"
	"plugin"
)

func main() {
	plug, err := plugin.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	symbol, err := plug.Lookup("Phase16_3Value")
	if err != nil {
		panic(err)
	}
	value, ok := symbol.(*int)
	if !ok {
		panic("Phase16_3Value has unexpected type")
	}
	fmt.Print(*value)
}
`)

	phase16_3RunGo(t, goBinary, root, goxPluginBuildArgs(pluginPath, pluginSource)...)
	phase16_3RunGo(t, goBinary, root, "build", "-o", hostPath, hostSource)

	cmd := exec.Command(hostPath, pluginPath)
	cmd.Dir = root
	cmd.Env = phase16_3GoEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("load stripped plugin: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "42" {
		t.Fatalf("stripped plugin result = %q, want 42", got)
	}
}

func BenchmarkPhase16_3PluginLinkerStrategy(b *testing.B) {
	strategies := []struct {
		name        string
		linkerFlags string
	}{
		{name: "Baseline"},
		{name: "Stripped", linkerFlags: goxPluginLinkerFlags},
	}

	for _, strategy := range strategies {
		strategy := strategy
		b.Run(strategy.name, func(b *testing.B) {
			runtime, route := phase16BuildBenchmarkRoute(b)
			if err := runtime.compileGoxRouteLocked(route); err != nil {
				b.Fatalf("compile GOX benchmark route: %v", err)
			}
			base, err := os.ReadFile(route.GeneratedPath)
			if err != nil {
				b.Fatal(err)
			}

			pluginPath := filepath.Join(route.BuildDir, "phase16_3-"+strings.ToLower(strategy.name)+".so")
			phase16_3WriteGeneratedVariant(b, route.GeneratedPath, base, 0)
			if err := phase16_3BuildPlugin(route.GoBinary, runtime.projectRoot(), pluginPath, route.GeneratedPath, strategy.linkerFlags); err != nil {
				b.Fatalf("prime %s plugin build: %v", strategy.name, err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				phase16_3WriteGeneratedVariant(b, route.GeneratedPath, base, i+1)
				b.StartTimer()
				if err := phase16_3BuildPlugin(route.GoBinary, runtime.projectRoot(), pluginPath, route.GeneratedPath, strategy.linkerFlags); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()

			info, err := os.Stat(pluginPath)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(info.Size()), "plugin-B/op")
		})
	}
}

func phase16_3BuildPlugin(goBinary, projectRoot, pluginPath, generatedPath, linkerFlags string) error {
	args := []string{"build", "-buildmode=plugin"}
	if strings.TrimSpace(linkerFlags) != "" {
		args = append(args, "-ldflags="+linkerFlags)
	}
	args = append(args, "-o", pluginPath, generatedPath)

	cmd := exec.Command(goBinary, args...)
	cmd.Dir = projectRoot
	cmd.Env = phase16_3GoEnv()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("go build plugin: %s", message)
	}
	return nil
}

func phase16_3WriteGeneratedVariant(tb testing.TB, path string, base []byte, version int) {
	tb.Helper()
	contents := make([]byte, 0, len(base)+64)
	contents = append(contents, base...)
	contents = append(contents, []byte(fmt.Sprintf("\nvar phase16_3BuildMarker = %d\n", version))...)
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		tb.Fatal(err)
	}
}

func phase16_3RunGo(t *testing.T, goBinary, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(goBinary, args...)
	cmd.Dir = dir
	cmd.Env = phase16_3GoEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", goBinary, strings.Join(args, " "), err, output)
	}
}

func phase16_3GoEnv() []string {
	env := os.Environ()
	for i := range env {
		if strings.HasPrefix(env[i], "GOWORK=") {
			env[i] = "GOWORK=off"
			return env
		}
	}
	return append(env, "GOWORK=off")
}

func writePhase16_3File(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
