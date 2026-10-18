package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"
)

func benchmarkRootCauseDocument(b *testing.B, source string) (string, Document) {
	b.Helper()
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(root, "page.gox")
	return root, Document{URI: FileURI(path), Path: path, Version: 1, Text: source}
}

// Measures only the canonical GOX semantic snapshot with no Go imports.
func BenchmarkPhase15RootCauseAnalysisSnapshotNoGoImportCold(b *testing.B) {
	root, document := benchmarkRootCauseDocument(b, `module page
func Build() {
	text := "hello"
	_ = text
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		server := NewServer()
		server.workspaceRoots = []string{root}
		b.StartTimer()
		snapshot := server.analysisSnapshot(document)
		if snapshot == nil || snapshot.Err != nil {
			b.Fatalf("snapshot = %#v", snapshot)
		}
	}
}

// Same semantic snapshot, but with one direct standard-library import. The
// delta from AnalysisSnapshotNoGoImportCold isolates semanticWorkspace's
// primeGoImports/packages.Load cost.
func BenchmarkPhase15RootCauseAnalysisSnapshotStringsImportCold(b *testing.B) {
	root, document := benchmarkRootCauseDocument(b, `module page
import "strings"
func Build() string {
	return strings.TrimSpace(" hello ")
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		server := NewServer()
		server.workspaceRoots = []string{root}
		b.StartTimer()
		snapshot := server.analysisSnapshot(document)
		if snapshot == nil || snapshot.Err != nil {
			b.Fatalf("snapshot = %#v", snapshot)
		}
	}
}

// Measures only GoPackageIndex.Discover on a fresh index. This is the cold
// package-name inventory paid by lexical auto-import state construction.
func BenchmarkPhase15RootCauseDiscoverCold(b *testing.B) {
	root, _ := benchmarkRootCauseDocument(b, `module page
func Build() {}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		index := NewGoPackageIndex()
		b.StartTimer()
		if _, err := index.Discover(root); err != nil {
			b.Fatal(err)
		}
	}
}

// Measures the raw x/tools NeedName request used by Discover, without GOX
// candidate merging/maps. This separates go/packages cost from GOX overhead.
func BenchmarkPhase15RootCausePackagesNeedNameStdCold(b *testing.B) {
	root, _ := benchmarkRootCauseDocument(b, `module page
func Build() {}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loaded, err := packages.Load(&packages.Config{Mode: packages.NeedName, Dir: root}, "std", "./...")
		if err != nil {
			b.Fatal(err)
		}
		if len(loaded) == 0 {
			b.Fatal("packages.Load returned no packages")
		}
	}
}

// Measures the raw x/tools file-manifest request used by the global export
// index before GOX parses any source files.
func BenchmarkPhase15RootCausePackagesNeedFilesStdCold(b *testing.B) {
	root, _ := benchmarkRootCauseDocument(b, `module page
func Build() {}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loaded, err := packages.Load(&packages.Config{Mode: goGlobalExportFileMode, Dir: root}, "std")
		if err != nil {
			b.Fatal(err)
		}
		if len(loaded) == 0 {
			b.Fatal("packages.Load returned no packages")
		}
	}
}

// Measures only the current production shallow source projection after the
// stdlib file manifest is already known. Phase 15.3c-A3 switched this path to
// the top-level scanner, so this benchmark tracks the active implementation.
func BenchmarkPhase15RootCauseGlobalExportSourceParseCold(b *testing.B) {
	root, _ := benchmarkRootCauseDocument(b, `module page
func Build() {}
`)
	loaded, err := packages.Load(&packages.Config{Mode: goGlobalExportFileMode, Dir: root}, "std")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, candidate := range loaded {
			if candidate == nil || candidate.Name == "main" || !goPackageCandidateVisible(candidate.PkgPath) || len(candidate.Errors) > 0 {
				continue
			}
			if _, err := goPackageExportNamesFromScanner(candidate, candidate.PkgPath, false); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// Measures ExportedPackages as a whole on a fresh index so the sum can be
// checked against NeedFiles + source parsing instead of inferred indirectly
// from completion benchmarks.
func BenchmarkPhase15RootCauseExportedPackagesCold(b *testing.B) {
	root, _ := benchmarkRootCauseDocument(b, `module page
func Build() {}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		index := NewGoPackageIndex()
		b.StartTimer()
		if _, err := index.ExportedPackages(root); err != nil {
			b.Fatal(err)
		}
	}
}

// Full cold lexical completion without any direct Go import. The delta from
// the existing LocalColdAutoImport benchmark isolates the cost of semantic
// loading for its direct strings import.
func BenchmarkPhase15RootCauseCompletionLowercaseNoGoImportCold(b *testing.B) {
	_, document := benchmarkSemanticArchitectureDocument(b, `module page
func Build() {
	text := "hello"
	_ = te
}
`)
	position := benchmarkPosition(b, document, "te")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		server := NewServer()
		if err := server.symbols.UpdateOpen(document); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		_ = server.completions(document, position)
	}
}
