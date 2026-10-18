package serverruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBenchmarkAllocationSiteFromStackIgnoresMissingGoxFrame(t *testing.T) {
	if site, ok := benchmarkAllocationSiteFromStack(nil); ok {
		t.Fatalf("site = %#v, want no site", site)
	}
}

func TestBenchmarkRuntimeAllocationSiteKeyIncludesLine(t *testing.T) {
	site := benchmarkAllocationSite{File: "pages/index.gox", Line: 9, Name: "GET"}
	if got, want := benchmarkRuntimeAllocationSiteKey(site), "pages/index.gox:9:GET:"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestIsBenchmarkGoxSourceFile(t *testing.T) {
	cases := []struct {
		file string
		want bool
	}{
		{"pages/index.gox", true},
		{"/tmp/project/pages/index.gox", true},
		{"pages/index.go", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isBenchmarkGoxSourceFile(tc.file); got != tc.want {
			t.Fatalf("isBenchmarkGoxSourceFile(%q) = %v, want %v", tc.file, got, tc.want)
		}
	}
}

func TestBenchmarkAllocationSiteAllowedFiltersToHandlerSource(t *testing.T) {
	allowed := map[string]struct{}{"pages/index.gox": {}}
	if !benchmarkAllocationSiteAllowed(benchmarkAllocationSite{File: "pages/index.gox", Line: 9, Name: "GET"}, allowed) {
		t.Fatal("expected handler source site to be allowed")
	}
	if !benchmarkAllocationSiteAllowed(benchmarkAllocationSite{File: "/tmp/project/pages/index.gox", Line: 9, Name: "GET"}, allowed) {
		t.Fatal("expected absolute handler source site to be allowed by suffix")
	}
	if benchmarkAllocationSiteAllowed(benchmarkAllocationSite{File: "tests/index.gox", Line: 16, Name: "IndexTest"}, allowed) {
		t.Fatal("expected test source site to be rejected")
	}
	if benchmarkAllocationSiteAllowed(benchmarkAllocationSite{File: "internal/serverruntime/test_executor.go", Line: 1, Name: "run"}, allowed) {
		t.Fatal("expected runtime source site to be rejected")
	}
}

func TestInferBenchmarkAllocationSitesFromGeneratedUsesLineDirective(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.go")
	content := `package main

func (m *GoxModuleMain) GET(ctx *gox.Context) {
//line pages/index.gox:8:1
	name := "Gox"
//line pages/index.gox:9:1
	msg := "Hello " + name
	_ = msg
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sites, err := inferBenchmarkAllocationSitesFromGenerated(path, "pages/index.gox", "GET", 10, 160)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Fatalf("sites = %#v, want one inferred site", sites)
	}
	if sites[0].File != "pages/index.gox" || sites[0].Line != 9 || sites[0].Name != "GET" || sites[0].Allocs != 10 || sites[0].Bytes != 160 {
		t.Fatalf("site = %#v, want pages/index.gox:9 GET inferred", sites[0])
	}
}
