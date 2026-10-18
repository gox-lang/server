package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

// Phase 15.3c-C1 decomposes GoPackageIndex.Discover into its independent
// scopes. These benchmarks intentionally make no production changes: they
// measure which part of package-name discovery owns the remaining cold
// latency before C2 changes when discovery is requested.

func phase15C1RepositoryRoot(tb testing.TB) string {
	tb.Helper()
	directory, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		contents, readErr := os.ReadFile(filepath.Join(directory, "go.mod"))
		if readErr == nil && modfile.ModulePath(contents) == "github.com/gox-lang/server" {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			tb.Fatal("gox-server module root not found")
		}
		directory = parent
	}
}

func phase15C1DependencyPatterns(tb testing.TB, root string) []string {
	tb.Helper()
	contents, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		tb.Fatal(err)
	}
	module, err := modfile.Parse("go.mod", contents, nil)
	if err != nil {
		tb.Fatal(err)
	}
	patterns := make([]string, 0, len(module.Require))
	for _, requirement := range module.Require {
		if requirement == nil || requirement.Mod.Path == "" {
			continue
		}
		patterns = append(patterns, requirement.Mod.Path)
	}
	return patterns
}

func phase15C1BenchmarkNeedName(b *testing.B, root string, patterns ...string) {
	b.Helper()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		loaded, err := packages.Load(&packages.Config{Mode: packages.NeedName, Dir: root}, patterns...)
		if err != nil {
			b.Fatal(err)
		}
		visible := 0
		for _, candidate := range loaded {
			if candidate == nil || candidate.PkgPath == "" || candidate.Name == "" || len(candidate.Errors) > 0 {
				continue
			}
			visible++
		}
		b.ReportMetric(float64(len(loaded)), "loaded-pkgs/op")
		b.ReportMetric(float64(visible), "usable-pkgs/op")
	}
}

// StdOnly isolates the standard-library inventory that every current
// Discover call requests, even when completion ultimately needs only a local
// or dependency package name.
func BenchmarkPhase15_3CC1DiscoverStdOnly(b *testing.B) {
	root, _ := benchmarkRootCauseDocument(b, `module page
func Build() {}
`)
	phase15C1BenchmarkNeedName(b, root, "std")
}

// WorkspaceGoListOnly measures x/tools/go-list discovery of the current
// module separately from GOX's direct filesystem scanner.
func BenchmarkPhase15_3CC1DiscoverWorkspaceGoListOnly(b *testing.B) {
	root := phase15C1RepositoryRoot(b)
	phase15C1BenchmarkNeedName(b, root, "./...")
}

// WorkspaceScannerOnly measures the GOX-owned WalkDir/package-clause scan.
// Discover currently runs this even on a warm discovery cache so newly
// created workspace packages can become visible without another go list.
func BenchmarkPhase15_3CC1DiscoverWorkspaceScannerOnly(b *testing.B) {
	root := phase15C1RepositoryRoot(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		candidates := discoverWorkspaceGoPackages(root)
		b.ReportMetric(float64(len(candidates)), "candidates/op")
	}
}

// DependenciesOnly uses gox-server's real direct/indirect requirements so C1
// measures the dependency scope independently from std and ./....
func BenchmarkPhase15_3CC1DiscoverDependenciesOnly(b *testing.B) {
	root := phase15C1RepositoryRoot(b)
	patterns := phase15C1DependencyPatterns(b, root)
	if len(patterns) == 0 {
		b.Fatal("gox-server has no dependency patterns")
	}
	phase15C1BenchmarkNeedName(b, root, patterns...)
}

// CombinedPackagesLoad reproduces the external packages.Load portion of the
// current Discover implementation on the real project. Comparing this with
// the isolated scopes shows how much work the shared go-list invocation can
// amortize and avoids incorrectly summing independent subprocess timings.
func BenchmarkPhase15_3CC1DiscoverCombinedPackagesLoad(b *testing.B) {
	root := phase15C1RepositoryRoot(b)
	patterns := []string{"std", "./..."}
	patterns = append(patterns, phase15C1DependencyPatterns(b, root)...)
	phase15C1BenchmarkNeedName(b, root, patterns...)
}

// CurrentDiscover measures the complete production discovery path on a fresh
// GoPackageIndex, including candidate filtering, workspace merge, sorting and
// the filesystem scanner.
func BenchmarkPhase15_3CC1DiscoverCurrent(b *testing.B) {
	root := phase15C1RepositoryRoot(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		index := NewGoPackageIndex()
		b.StartTimer()
		candidates, err := index.Discover(root)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(len(candidates)), "candidates/op")
	}
}

// WarmDiscover exposes the cost that remains after the go-tool package list
// is cached. In the current implementation this still includes the workspace
// WalkDir scan and merge/sort on every call.
func BenchmarkPhase15_3CC1DiscoverWarm(b *testing.B) {
	root := phase15C1RepositoryRoot(b)
	index := NewGoPackageIndex()
	if _, err := index.Discover(root); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		candidates, err := index.Discover(root)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(len(candidates)), "candidates/op")
	}
}
