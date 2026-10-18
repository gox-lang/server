package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoPackageIndexDiscoverIncludesWorkspacePackageWithDeclaredName(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), "package register\nfunc Register() {}\n")

	candidates, err := NewGoPackageIndex().Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.Path == "gox-editor-test/billing" && candidate.Name == "register" {
			return
		}
	}
	t.Fatalf("workspace Go package missing from discovery: %#v", candidates)
}

func TestGoPackageCandidateVisibleExcludesToolchainImplementationPaths(t *testing.T) {
	for _, importPath := range []string{
		"internal/strconv",
		"go/internal/srcimporter",
		"crypto/internal/fips140/sha512",
		"vendor/golang.org/x/crypto/chacha20",
	} {
		if goPackageCandidateVisible(importPath) {
			t.Fatalf("implementation package %q is visible", importPath)
		}
	}
	for _, importPath := range []string{"strings", "crypto/sha512", "net/http"} {
		if !goPackageCandidateVisible(importPath) {
			t.Fatalf("public package %q is hidden", importPath)
		}
	}
}

func TestPhase15_3CC2ADiscoverCacheWaitsForInvalidationBeforeAddingWorkspacePackage(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	index := NewGoPackageIndex()
	if _, err := index.Discover(root); err != nil {
		t.Fatal(err)
	}

	writeGoPackageHoverFile(t, filepath.Join(root, "billing", "register.go"), "package register\nfunc Register() {}\n")
	candidates, err := index.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if goPackageCandidatePresent(candidates, "gox-editor-test/billing", "register") {
		t.Fatalf("warm discovery rescanned workspace without invalidation: %#v", candidates)
	}

	index.InvalidateRoot(root)
	candidates, err = index.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if !goPackageCandidatePresent(candidates, "gox-editor-test/billing", "register") {
		t.Fatalf("invalidated discovery omitted new workspace Go package: %#v", candidates)
	}
}

func TestPhase15_3CC2ADiscoverCacheWaitsForInvalidationBeforeDroppingWorkspacePackage(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module gox-editor-test\n\ngo 1.24\n")
	packageFile := filepath.Join(root, "billing", "register.go")
	writeGoPackageHoverFile(t, packageFile, "package register\nfunc Register() {}\n")

	index := NewGoPackageIndex()
	candidates, err := index.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if !goPackageCandidatePresent(candidates, "gox-editor-test/billing", "register") {
		t.Fatalf("initial discovery omitted workspace Go package: %#v", candidates)
	}

	if err := os.Remove(packageFile); err != nil {
		t.Fatal(err)
	}
	candidates, err = index.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if !goPackageCandidatePresent(candidates, "gox-editor-test/billing", "register") {
		t.Fatalf("warm discovery unexpectedly mutated before invalidation: %#v", candidates)
	}

	index.InvalidateRoot(root)
	candidates, err = index.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if goPackageCandidatePresent(candidates, "gox-editor-test/billing", "register") {
		t.Fatalf("invalidated discovery retained deleted workspace Go package: %#v", candidates)
	}
}

func goPackageCandidatePresent(candidates []GoPackageCandidate, path, name string) bool {
	for _, candidate := range candidates {
		if candidate.Path == path && candidate.Name == name {
			return true
		}
	}
	return false
}
