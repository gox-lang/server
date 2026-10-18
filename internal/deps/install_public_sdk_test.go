package deps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/buildinfo"
)

func TestInstallKeepsPublishedSDKThroughTidyAndKeepsBridge(t *testing.T) {
	root := t.TempDir()
	cfg := NewGoxJSON()
	cfg.Dependencies[buildinfo.GoxModule] = buildinfo.GoxModuleVersion
	if err := WriteGoxJSON(root, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.24\n\nrequire "+buildinfo.GoxModule+" "+buildinfo.GoxModuleVersion+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(root, "go-calls.log")
	fakeGo := filepath.Join(root, "fake-go")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "` + logPath + `"
if [ "$1" = "mod" ] && [ "$2" = "tidy" ]; then
  grep -F '_ "` + buildinfo.GoxModule + `"' deps.go >/dev/null
  printf '` + buildinfo.GoxModule + ` ` + buildinfo.GoxModuleVersion + ` h1:test\n' > go.sum
  exit 0
fi
exit 91
`
	if err := os.WriteFile(fakeGo, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Install(root, fakeGo); err != nil {
		t.Fatal(err)
	}

	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(calls)); got != "mod tidy" {
		t.Fatalf("Go commands = %q, want only %q", got, "mod tidy")
	}
	bridge, err := os.ReadFile(filepath.Join(root, DepsGoFile))
	if err != nil {
		t.Fatalf("permanent %s missing after install: %v", DepsGoFile, err)
	}
	if !strings.Contains(string(bridge), `_ "`+buildinfo.GoxModule+`"`) {
		t.Fatalf("%s does not retain SDK import:\n%s", DepsGoFile, bridge)
	}
	if _, err := os.Stat(filepath.Join(root, "go.sum")); err != nil {
		t.Fatalf("go.sum was not produced by tidy: %v", err)
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goMod), "require "+buildinfo.GoxModule+" "+buildinfo.GoxModuleVersion) {
		t.Fatalf("go.mod lost published SDK requirement:\n%s", goMod)
	}
}

func TestInstallKeepsDependencyBridgeWhenTidyFails(t *testing.T) {
	root := t.TempDir()
	cfg := NewGoxJSON()
	cfg.Dependencies[buildinfo.GoxModule] = buildinfo.GoxModuleVersion
	if err := WriteGoxJSON(root, cfg); err != nil {
		t.Fatal(err)
	}

	fakeGo := filepath.Join(root, "fake-go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(root, fakeGo); err == nil {
		t.Fatal("Install succeeded, want tidy failure")
	}
	bridge, err := os.ReadFile(filepath.Join(root, DepsGoFile))
	if err != nil {
		t.Fatalf("permanent %s missing after tidy failure: %v", DepsGoFile, err)
	}
	if !strings.Contains(string(bridge), `_ "`+buildinfo.GoxModule+`"`) {
		t.Fatalf("%s does not retain SDK import after tidy failure:\n%s", DepsGoFile, bridge)
	}
}
