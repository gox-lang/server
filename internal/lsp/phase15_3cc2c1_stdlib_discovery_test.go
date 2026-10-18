package lsp

import (
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPhase15_3CC2C1StandardLibraryScannerFindsPublicPackages(t *testing.T) {
	candidates := discoverStandardLibraryGoPackages()
	if len(candidates) == 0 {
		t.Fatal("standard-library scanner returned no candidates")
	}
	byPath := make(map[string]GoPackageCandidate, len(candidates))
	for _, candidate := range candidates {
		if candidate.Path == "" || candidate.Name == "" {
			t.Fatalf("invalid standard-library candidate: %#v", candidate)
		}
		if !goPackageCandidateVisible(candidate.Path) {
			t.Fatalf("scanner exposed implementation-only package %q", candidate.Path)
		}
		if _, exists := byPath[candidate.Path]; exists {
			t.Fatalf("duplicate standard-library candidate %q", candidate.Path)
		}
		byPath[candidate.Path] = candidate
	}

	for path, wantName := range map[string]string{
		"strings":  "strings",
		"net/http": "http",
		"unsafe":   "unsafe",
	} {
		candidate, ok := byPath[path]
		if !ok {
			t.Fatalf("standard-library scanner is missing %q", path)
		}
		if candidate.Name != wantName {
			t.Fatalf("standard-library candidate %q name = %q, want %q", path, candidate.Name, wantName)
		}
	}

	for _, forbidden := range []string{"builtin", "internal/abi", "crypto/internal/alias", "cmd/go"} {
		if _, ok := byPath[forbidden]; ok {
			t.Fatalf("standard-library scanner exposed non-public package %q", forbidden)
		}
	}
}

func TestPhase15_3CC2C1StandardLibraryScannerRespectsBuildSelection(t *testing.T) {
	goroot := t.TempDir()
	sourceRoot := filepath.Join(goroot, "src")
	write := func(relative, contents string) {
		t.Helper()
		filename := filepath.Join(sourceRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("portable/portable.go", "package portable\n")
	write("platform/name_linux.go", "package platform\n")
	write("platform/name_windows.go", "package wrongwindows\n")
	write("tagged/enabled.go", "//go:build phase15tag\n\npackage tagged\n")
	write("tagged/disabled.go", "//go:build !phase15tag\n\npackage wrongtag\n")
	write("onlytest/only_test.go", "package onlytest\n")
	write("internal/hidden/hidden.go", "package hidden\n")
	write("cmd/tool/main.go", "package main\n")
	write("builtin/builtin.go", "package builtin\n")

	context := build.Default
	context.GOROOT = goroot
	context.GOOS = "linux"
	context.GOARCH = "amd64"
	context.BuildTags = append([]string(nil), "phase15tag")
	candidates := discoverStandardLibraryGoPackagesWithContext(context)
	byPath := make(map[string]GoPackageCandidate, len(candidates))
	for _, candidate := range candidates {
		byPath[candidate.Path] = candidate
	}

	for path, wantName := range map[string]string{
		"portable": "portable",
		"platform": "platform",
		"tagged":   "tagged",
	} {
		candidate, ok := byPath[path]
		if !ok {
			t.Fatalf("scanner is missing active package %q on %s/%s", path, context.GOOS, context.GOARCH)
		}
		if candidate.Name != wantName {
			t.Fatalf("package %q name = %q, want %q", path, candidate.Name, wantName)
		}
	}
	for _, forbidden := range []string{"onlytest", "internal/hidden", "cmd/tool", "builtin"} {
		if _, ok := byPath[forbidden]; ok {
			t.Fatalf("scanner exposed excluded package %q", forbidden)
		}
	}
}

func TestPhase15_3CC2C1StandardLibraryScannerUsesCurrentToolchainContext(t *testing.T) {
	context := build.Default
	if context.GOROOT == "" {
		t.Fatal("build.Default.GOROOT is empty")
	}
	if context.GOOS != runtime.GOOS || context.GOARCH != runtime.GOARCH {
		t.Fatalf("build context = %s/%s, runtime = %s/%s", context.GOOS, context.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
}
