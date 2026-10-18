package lsp

import (
	"testing"

	"golang.org/x/tools/go/packages"
)

// Phase 15.3c-C2-C3 measures the two equivalent standard-library discovery
// authorities proven by C2-C2. Run with -benchtime=1x when comparing cold
// latency so packages.Load and the GOROOT scanner see the same benchmark
// shape. candidates/op must remain identical between both implementations.
func BenchmarkPhase15_3CC2C3StandardLibraryGoPackages(b *testing.B) {
	root := phase15C1RepositoryRoot(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		loaded, err := packages.Load(&packages.Config{Mode: packages.NeedName, Dir: root}, "std")
		if err != nil {
			b.Fatal(err)
		}

		seen := make(map[string]struct{}, len(loaded))
		visible := 0
		for _, candidate := range loaded {
			if candidate == nil || candidate.PkgPath == "" || candidate.Name == "" || candidate.Name == "main" ||
				len(candidate.Errors) > 0 || !goPackageCandidateVisible(candidate.PkgPath) {
				continue
			}
			if _, ok := seen[candidate.PkgPath]; ok {
				continue
			}
			seen[candidate.PkgPath] = struct{}{}
			visible++
		}
		b.ReportMetric(float64(visible), "candidates/op")
	}
}

func BenchmarkPhase15_3CC2C3StandardLibraryScanner(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		candidates := discoverStandardLibraryGoPackages()
		b.ReportMetric(float64(len(candidates)), "candidates/op")
	}
}
