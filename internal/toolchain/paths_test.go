package toolchain

import (
	"path/filepath"
	"testing"
)

func TestResolvePathsUsesHiddenToolchainDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOX_HOME", home)

	paths, err := ResolvePaths(New("1.27.1"), Platform{OS: "linux", Arch: "amd64"})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}

	want := filepath.Join(home, ".toolchain")
	if paths.ToolchainDir != want {
		t.Fatalf("ToolchainDir = %q, want %q", paths.ToolchainDir, want)
	}
}

func TestResolveFrontendPathsUseHiddenToolchainDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOX_HOME", home)
	platform := Platform{OS: "linux", Arch: "amd64"}
	want := filepath.Join(home, ".toolchain")

	node, err := ResolveNodePaths(NewNode("22.0.0"), platform)
	if err != nil {
		t.Fatalf("ResolveNodePaths() error = %v", err)
	}
	if node.ToolchainDir != want {
		t.Fatalf("Node ToolchainDir = %q, want %q", node.ToolchainDir, want)
	}

	pnpm, err := ResolvePNPMPaths(NewPNPM("10.0.0"))
	if err != nil {
		t.Fatalf("ResolvePNPMPaths() error = %v", err)
	}
	if pnpm.ToolchainDir != want {
		t.Fatalf("PNPM ToolchainDir = %q, want %q", pnpm.ToolchainDir, want)
	}
}
