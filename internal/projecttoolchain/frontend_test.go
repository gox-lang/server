package projecttoolchain

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/toolchain"
)

func writeManifest(t *testing.T, root, node, pnpm string) {
	t.Helper()
	content := `{"dependencies":{},"toolchain":{"node":"` + node + `","pnpm":"` + pnpm + `","vite":"8.1.0","typescript":"6.0.3"},"frameworks":{}}`
	if err := os.WriteFile(filepath.Join(root, "gox.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveUsesProjectManifest(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "18.20.8", "9.15.9")

	got, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Node.ExactVersion != "node18.20.8" {
		t.Fatalf("node = %q", got.Node.ExactVersion)
	}
	if got.PNPM.ExactVersion != "pnpm9.15.9" {
		t.Fatalf("pnpm = %q", got.PNPM.ExactVersion)
	}
}

func TestResolveKeepsProjectsIndependent(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeManifest(t, a, "18.20.8", "9.15.9")
	writeManifest(t, b, "22.18.0", "11.9.0")

	gotA, err := Resolve(a)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := Resolve(b)
	if err != nil {
		t.Fatal(err)
	}
	if gotA.Node.ExactVersion == gotB.Node.ExactVersion || gotA.PNPM.ExactVersion == gotB.PNPM.ExactVersion {
		t.Fatalf("project toolchains were not isolated: A=%#v B=%#v", gotA, gotB)
	}
}

func TestResolveRejectsMissingProjectVersions(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "", "11.9.0")
	if _, err := Resolve(root); err == nil {
		t.Fatal("expected missing Node version error")
	}

	writeManifest(t, root, "22.18.0", "")
	if _, err := Resolve(root); err == nil {
		t.Fatal("expected missing pnpm version error")
	}
}

func TestResolvedVersionsUseSharedVersionedRepository(t *testing.T) {
	t.Setenv("GOX_HOME", t.TempDir())
	platform := toolchain.Platform{OS: "linux", Arch: "amd64"}

	node18, err := toolchain.ResolveNodePaths(toolchain.NewNode("18.20.8"), platform)
	if err != nil {
		t.Fatal(err)
	}
	node22, err := toolchain.ResolveNodePaths(toolchain.NewNode("22.18.0"), platform)
	if err != nil {
		t.Fatal(err)
	}
	pnpm9, err := toolchain.ResolvePNPMPaths(toolchain.NewPNPM("9.15.9"))
	if err != nil {
		t.Fatal(err)
	}
	pnpm11, err := toolchain.ResolvePNPMPaths(toolchain.NewPNPM("11.9.0"))
	if err != nil {
		t.Fatal(err)
	}

	if node18.ToolchainDir != node22.ToolchainDir || node18.VersionDir == node22.VersionDir {
		t.Fatalf("Node toolchain/version isolation invalid: 18=%#v 22=%#v", node18, node22)
	}
	if pnpm9.ToolchainDir != pnpm11.ToolchainDir || pnpm9.VersionDir == pnpm11.VersionDir {
		t.Fatalf("pnpm toolchain/version isolation invalid: 9=%#v 11=%#v", pnpm9, pnpm11)
	}
}
