package lsp

import (
	"path/filepath"
	"testing"
)

func BenchmarkPhase15_4B2ProjectGoPackageInvalidationPlanning(b *testing.B) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		b.Fatal(err)
	}
	changed := filepath.Join(root, "internal", "lsp", "server.go")
	index := NewGoPackageIndex()
	if _, ok := index.ProjectGoPackageInvalidationForFile(root, changed); !ok {
		b.Fatal("failed to build persistent project Go import graph")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		invalidation, ok := index.ProjectGoPackageInvalidationForFile(root, changed)
		if !ok {
			b.Fatal("failed to derive project Go package invalidation")
		}
		b.ReportMetric(float64(len(invalidation.Affected)), "affected-pkgs/op")
	}
}

func BenchmarkPhase15_4B2bProjectGoImportGraphColdBuild(b *testing.B) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		b.Fatal(err)
	}
	changed := filepath.Join(root, "internal", "lsp", "server.go")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		index := NewGoPackageIndex()
		invalidation, ok := index.ProjectGoPackageInvalidationForFile(root, changed)
		if !ok {
			b.Fatal("failed to build persistent project Go import graph")
		}
		b.ReportMetric(float64(len(invalidation.Affected)), "affected-pkgs/op")
	}
}
