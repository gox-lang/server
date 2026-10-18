package lsp

import (
	"path/filepath"
	"testing"
)

// C2-C4 removes the std pattern from packages.Load in production. A project
// without module dependencies must therefore discover both standard-library
// and workspace packages even when no external go executable is available.
// This is stronger than checking the final candidate list alone: if Discover
// regresses to packages.Load("std"), this test fails at the process boundary.
func TestPhase15_3CC2C4DiscoverDoesNotRequireGoListForStandardLibrary(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example.com/workspace\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "localpkg", "local.go"), "package localpkg\n")

	emptyPath := t.TempDir()
	t.Setenv("PATH", emptyPath)

	candidates, err := NewGoPackageIndex().Discover(root)
	if err != nil {
		t.Fatalf("Discover requires external go command for stdlib-only project: %v", err)
	}
	requireGoPackageCandidate(t, candidates, "strings", "strings")
	requireGoPackageCandidate(t, candidates, "unsafe", "unsafe")
	requireGoPackageCandidate(t, candidates, "example.com/workspace/localpkg", "localpkg")
}

func TestPhase15_3CC2C4DiscoverStillLoadsDeclaredDependencies(t *testing.T) {
	root := t.TempDir()
	dependencyRoot := t.TempDir()

	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example.com/workspace\n\ngo 1.24\n\nrequire example.com/library v0.0.0\nreplace example.com/library => "+filepath.ToSlash(dependencyRoot)+"\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "localpkg", "local.go"), "package localpkg\n")
	writeGoPackageHoverFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/library\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(dependencyRoot, "library.go"), "package toolkit\n")

	candidates, err := NewGoPackageIndex().Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	requireGoPackageCandidate(t, candidates, "strings", "strings")
	requireGoPackageCandidate(t, candidates, "example.com/workspace/localpkg", "localpkg")
	requireGoPackageCandidate(t, candidates, "example.com/library", "toolkit")
}

func TestPhase15_3CC2C4DiscoverNoDependenciesDoesNotInvokePackagesLoad(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example.com/workspace\n\ngo 1.24\n")

	// packages.Load with an empty pattern list has different semantics across
	// x/tools versions. C2-C4 must skip it entirely when the module declares no
	// requirements, otherwise removing "std" would still pay an unnecessary
	// package-driver invocation.
	t.Setenv("PATH", t.TempDir())
	candidates, err := NewGoPackageIndex().Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	requireGoPackageCandidate(t, candidates, "net/http", "http")
}
