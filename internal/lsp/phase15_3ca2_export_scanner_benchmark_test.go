package lsp

import (
	"testing"

	"golang.org/x/tools/go/packages"
)

func BenchmarkPhase15_3CA2ShallowExportsAST(b *testing.B) {
	loaded := phase15CA2StandardLibraryPackages(b)
	b.ReportAllocs()
	b.ResetTimer()

	for iteration := 0; iteration < b.N; iteration++ {
		exports := 0
		for _, pkg := range loaded {
			result, err := goPackageExportNamesFromASTForTest(pkg, pkg.PkgPath, false)
			if err != nil {
				b.Fatal(err)
			}
			exports += len(result.Exports)
		}
		if exports < 1000 {
			b.Fatalf("AST shallow export inventory unexpectedly small: %d", exports)
		}
	}
}

func BenchmarkPhase15_3CA2ShallowExportsScanner(b *testing.B) {
	loaded := phase15CA2StandardLibraryPackages(b)
	b.ReportAllocs()
	b.ResetTimer()

	for iteration := 0; iteration < b.N; iteration++ {
		exports := 0
		for _, pkg := range loaded {
			result, err := goPackageExportNamesFromScanner(pkg, pkg.PkgPath, false)
			if err != nil {
				b.Fatal(err)
			}
			exports += len(result.Exports)
		}
		if exports < 1000 {
			b.Fatalf("scanner shallow export inventory unexpectedly small: %d", exports)
		}
	}
}

func phase15CA2StandardLibraryPackages(tb testing.TB) []*packages.Package {
	tb.Helper()
	loaded, err := packages.Load(&packages.Config{Mode: goGlobalExportFileMode}, "std")
	if err != nil {
		tb.Fatal(err)
	}

	result := make([]*packages.Package, 0, len(loaded))
	for _, pkg := range loaded {
		if pkg == nil || pkg.PkgPath == "" || len(pkg.Errors) > 0 || len(pkg.GoFiles) == 0 {
			continue
		}
		result = append(result, pkg)
	}
	if len(result) < 100 {
		tb.Fatalf("stdlib package inventory unexpectedly small: %d", len(result))
	}
	return result
}
