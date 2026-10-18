package serverruntime

import (
	"fmt"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"testing"
	"time"
)

func BenchmarkPhase16_1GoxBuildPipeline(b *testing.B) {
	b.Run("RouteCompileAfterGoxChange", func(b *testing.B) {
		runtime, route := phase16BuildBenchmarkRoute(b)
		phase16PrimeRouteCompileAndBuild(b, runtime, route)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			phase16WriteBenchmarkPage(b, route.AbsPath, i+1)
			b.StartTimer()
			if err := runtime.compileGoxRouteLocked(route); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("GoBuildPluginChangedGeneratedSource", func(b *testing.B) {
		runtime, route := phase16BuildBenchmarkRoute(b)
		phase16PrimeRouteCompileAndBuild(b, runtime, route)
		base, err := os.ReadFile(route.GeneratedPath)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			contents := append(append([]byte(nil), base...), []byte(fmt.Sprintf("\n// phase16-build-%d\n", i+1))...)
			if err := os.WriteFile(route.GeneratedPath, contents, 0644); err != nil {
				b.Fatal(err)
			}
			route.PluginExists = false
			route.PluginSize = 0
			b.StartTimer()
			if err := runtime.buildGoxRoutePluginLocked(route); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		b.ReportMetric(float64(route.PluginSize), "plugin-B/op")
	})

	b.Run("GoxChangeCompilePlusGoBuild", func(b *testing.B) {
		runtime, route := phase16BuildBenchmarkRoute(b)
		phase16PrimeRouteCompileAndBuild(b, runtime, route)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			phase16WriteBenchmarkPage(b, route.AbsPath, i+1000)
			b.StartTimer()
			if err := runtime.compileGoxRouteLocked(route); err != nil {
				b.Fatal(err)
			}
			if err := runtime.buildGoxRoutePluginLocked(route); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		b.ReportMetric(float64(route.PluginSize), "plugin-B/op")
	})
}

func BenchmarkPhase16_1DependencyFingerprintScaling(b *testing.B) {
	for _, count := range []int{1, 8, 32, 128} {
		deps := phase16FingerprintDependencies(b, count, 4096)
		b.Run(fmt.Sprintf("Deps%d", count), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, version, err := goxDependencyBuildVersion(deps); err != nil {
					b.Fatal(err)
				} else if version == "" {
					b.Fatal("empty dependency fingerprint")
				}
			}
		})
	}
}

func phase16BuildBenchmarkRoute(b *testing.B) (*Runtime, *GoxRouteRuntime) {
	b.Helper()
	root := b.TempDir()
	phase16WriteRuntimeBenchmarkFile(b, filepath.Join(root, "go.mod"), `module example.com/phase16runtime

go 1.24

require github.com/gox-lang/gox v1.0.0
`)
	phase16CopyRepositoryGoSum(b, root)
	entry := filepath.Join(root, "pages", "index.gox")
	phase16WriteBenchmarkPage(b, entry, 0)

	info, err := os.Stat(entry)
	if err != nil {
		b.Fatal(err)
	}
	buildDir, buildVersion, generatedPath, pluginPath, err := resolveGoxBuildPaths(root, "/index", info.ModTime())
	if err != nil {
		b.Fatal(err)
	}
	goBinary := filepath.Join(stdruntime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goBinary); err != nil {
		b.Fatalf("Go binary unavailable: %v", err)
	}

	// Prime the normal Go build cache outside the timed section. This mirrors
	// hot reload after the server/toolchain dependencies are already warm.
	b.Setenv("GOWORK", "off")

	runtime := &Runtime{ProjectRoot: root}
	route := &GoxRouteRuntime{
		RoutePath:     "/index",
		AbsPath:       entry,
		RelPath:       "pages/index.gox",
		ModTime:       info.ModTime(),
		Size:          info.Size(),
		BuildDir:      buildDir,
		BuildVersion:  buildVersion,
		GeneratedPath: generatedPath,
		PluginPath:    pluginPath,
		GoBinary:      goBinary,
		State:         GoxRouteCold,
	}
	return runtime, route
}

func phase16PrimeRouteCompileAndBuild(b *testing.B, runtime *Runtime, route *GoxRouteRuntime) {
	b.Helper()
	if err := runtime.compileGoxRouteLocked(route); err != nil {
		b.Fatalf("prime GOX compile: %v", err)
	}
	if err := runtime.buildGoxRoutePluginLocked(route); err != nil {
		b.Fatalf("prime Go plugin build: %v", err)
	}
}

func phase16WriteBenchmarkPage(b *testing.B, path string, version int) {
	b.Helper()
	phase16WriteRuntimeBenchmarkFile(b, path, fmt.Sprintf(`module index

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <main>phase16-%d</main>
}
`, version))
	stamp := time.Now().Add(time.Duration(version+1) * time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		b.Fatal(err)
	}
}

func phase16FingerprintDependencies(b *testing.B, count, bytesPerFile int) []GoxRouteDependency {
	b.Helper()
	root := b.TempDir()
	deps := make([]GoxRouteDependency, 0, count)
	for i := 0; i < count; i++ {
		path := filepath.Join(root, fmt.Sprintf("dep-%03d.gox", i))
		contents := make([]byte, bytesPerFile)
		for j := range contents {
			contents[j] = byte('a' + (i+j)%26)
		}
		if err := os.WriteFile(path, contents, 0644); err != nil {
			b.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			b.Fatal(err)
		}
		deps = append(deps, GoxRouteDependency{AbsPath: path, Size: info.Size(), ModTime: info.ModTime(), IsEntry: i == 0})
	}
	return deps
}

func phase16WriteRuntimeBenchmarkFile(b *testing.B, path, contents string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		b.Fatal(err)
	}
}

func phase16CopyRepositoryGoSum(b *testing.B, root string) {
	b.Helper()
	_, sourcePath, _, ok := stdruntime.Caller(0)
	if !ok {
		b.Fatal("resolve benchmark source path")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourcePath), "..", ".."))
	contents, err := os.ReadFile(filepath.Join(repositoryRoot, "go.sum"))
	if err != nil {
		b.Fatalf("read repository go.sum: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), contents, 0644); err != nil {
		b.Fatalf("write benchmark go.sum: %v", err)
	}
}
