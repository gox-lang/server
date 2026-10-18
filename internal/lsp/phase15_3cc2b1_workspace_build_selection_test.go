package lsp

import (
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestPhase15_3CC2B1WorkspaceScannerMatchesGoPackagesAcrossRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root %q has no go.mod: %v", root, err)
	}
	got := discoverWorkspaceGoPackages(root)
	want := phase15WorkspaceCandidatesFromGoPackages(t, root, nil, nil)
	phase15RequireSameWorkspaceCandidates(t, got, want)
}

func TestPhase15_3CC2B1WorkspaceScannerMatchesGoPackagesForActiveBuild(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example.com/workspace\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "plain", "plain.go"), "package plain\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "tagged", "a_inactive.go"), "//go:build phase15_never\n\npackage wrongtag\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "tagged", "z_active.go"), "package tagged\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "legacy", "a_inactive.go"), "// +build phase15_never\n\npackage wronglegacy\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "legacy", "z_active.go"), "package legacy\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "conflict", "a.go"), "package conflict_a\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "conflict", "b.go"), "package conflict_b\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "onlytest", "only_test.go"), "package onlytest\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "command", "main.go"), "package main\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "nested", "go.mod"), "module example.com/nested\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "nested", "nested.go"), "package nested\n")

	got := discoverWorkspaceGoPackages(root)
	want := phase15WorkspaceCandidatesFromGoPackages(t, root, nil, nil)
	phase15RequireSameWorkspaceCandidates(t, got, want)
}

func TestPhase15_3CC2B1WorkspaceScannerMatchesGoPackagesForGOOSAndGOARCH(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example.com/workspace\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "platform", "platform_linux.go"), "package linuxpkg\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "platform", "platform_windows.go"), "package windowspkg\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "arch", "arch_amd64.go"), "package amd64pkg\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "arch", "arch_arm64.go"), "package arm64pkg\n")

	context := build.Default
	context.GOOS = "linux"
	context.GOARCH = "amd64"
	got := discoverWorkspaceGoPackagesWithContext(root, context)
	want := phase15WorkspaceCandidatesFromGoPackages(t, root, []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0"}, nil)
	phase15RequireSameWorkspaceCandidates(t, got, want)

	context.GOOS = "windows"
	context.GOARCH = "arm64"
	got = discoverWorkspaceGoPackagesWithContext(root, context)
	want = phase15WorkspaceCandidatesFromGoPackages(t, root, []string{"GOOS=windows", "GOARCH=arm64", "CGO_ENABLED=0"}, nil)
	phase15RequireSameWorkspaceCandidates(t, got, want)
}

func TestPhase15_3CC2B1WorkspaceScannerMatchesGoPackagesForCustomBuildTag(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example.com/workspace\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "feature", "feature_default.go"), "//go:build !phase15custom\n\npackage defaultfeature\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "feature", "feature_custom.go"), "//go:build phase15custom\n\npackage customfeature\n")

	context := build.Default
	context.BuildTags = append(append([]string(nil), context.BuildTags...), "phase15custom")
	got := discoverWorkspaceGoPackagesWithContext(root, context)
	want := phase15WorkspaceCandidatesFromGoPackages(t, root, nil, []string{"-tags=phase15custom"})
	phase15RequireSameWorkspaceCandidates(t, got, want)
}

func phase15WorkspaceCandidatesFromGoPackages(t *testing.T, root string, envOverrides, buildFlags []string) []GoPackageCandidate {
	t.Helper()
	// packages.Load("./...") also enumerates directories that contain only
	// *_test.go files. Such a directory has a package name, but it is not an
	// importable package for normal GOX/Go source. Compare the scanner against
	// the actual import surface by requiring at least one compiled non-test Go
	// file.
	configuration := &packages.Config{Mode: packages.LoadFiles, Dir: root, BuildFlags: buildFlags}
	if len(envOverrides) > 0 {
		configuration.Env = append(append([]string(nil), os.Environ()...), envOverrides...)
	}
	loaded, err := packages.Load(configuration, "./...")
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]GoPackageCandidate, 0, len(loaded))
	for _, candidate := range loaded {
		if candidate == nil || candidate.PkgPath == "" || candidate.Name == "" || candidate.Name == "main" || candidate.Name == "_" || len(candidate.CompiledGoFiles) == 0 || len(candidate.Errors) > 0 {
			continue
		}
		candidates = append(candidates, GoPackageCandidate{Path: candidate.PkgPath, Name: candidate.Name})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].Path != candidates[right].Path {
			return candidates[left].Path < candidates[right].Path
		}
		return candidates[left].Name < candidates[right].Name
	})
	return candidates
}

func phase15RequireSameWorkspaceCandidates(t *testing.T, got, want []GoPackageCandidate) {
	t.Helper()
	got = append([]GoPackageCandidate(nil), got...)
	sort.Slice(got, func(left, right int) bool {
		if got[left].Path != got[right].Path {
			return got[left].Path < got[right].Path
		}
		return got[left].Name < got[right].Name
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace scanner candidates differ from go/packages for %s/%s:\nscanner = %#v\ngo/packages = %#v", runtime.GOOS, runtime.GOARCH, got, want)
	}
}
