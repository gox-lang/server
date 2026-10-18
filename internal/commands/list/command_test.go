package list

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/gocmd"
)

func TestCommandMetadata(t *testing.T) {
	cmd := New()
	if got := cmd.Name(); got != "list" {
		t.Fatalf("Name() = %q, want %q", got, "list")
	}
	if !strings.Contains(cmd.Help(), "gox list") {
		t.Fatalf("Help() does not document gox list:\n%s", cmd.Help())
	}
}

func TestCommandListsDirectDependenciesSorted(t *testing.T) {
	root := t.TempDir()
	data := `{
  "dependencies": {
    "github.com/shopspring/decimal": "v1.4.0",
    "github.com/google/uuid": "v1.6.0"
  }
}
`
	if err := os.WriteFile(filepath.Join(root, "gox.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := New().Run(cli.Context{WorkDir: root, Stdout: &stdout, Stderr: &bytes.Buffer{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "github.com/google/uuid\tv1.6.0\ngithub.com/shopspring/decimal\tv1.4.0\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestCommandListsLatestVersion(t *testing.T) {
	root := t.TempDir()
	data := `{"dependencies":{"example.com/pkg":"latest"}}`
	if err := os.WriteFile(filepath.Join(root, "gox.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := New().Run(cli.Context{WorkDir: root, Stdout: &stdout}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "example.com/pkg\tlatest\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestCommandWithNoDependenciesPrintsNothing(t *testing.T) {
	var stdout bytes.Buffer
	if err := New().Run(cli.Context{WorkDir: t.TempDir(), Stdout: &stdout}, nil); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestCommandRejectsArguments(t *testing.T) {
	err := New().Run(cli.Context{WorkDir: t.TempDir(), Stdout: &bytes.Buffer{}}, []string{"extra"})
	if err == nil || err.Error() != "usage: gox list [-t|--transitive]" {
		t.Fatalf("Run() error = %v, want %q", err, "usage: gox list [-t|--transitive]")
	}
}

func TestCommandReturnsInvalidGoxJSONError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gox.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := New().Run(cli.Context{WorkDir: root, Stdout: &bytes.Buffer{}}, nil)
	if err == nil || !strings.Contains(err.Error(), "read gox.json") {
		t.Fatalf("Run() error = %v, want gox.json read error", err)
	}
}

func TestCommandListsTransitiveDependenciesWithShortFlag(t *testing.T) {
	cmd := New()
	cmd.listTransitive = func(workDir string) ([]gocmd.Module, error) {
		return []gocmd.Module{
			{Path: "example.com/direct", Version: "v1.0.0"},
			{Path: "example.com/transitive", Version: "v2.0.0"},
		}, nil
	}
	var stdout bytes.Buffer
	if err := cmd.Run(cli.Context{WorkDir: t.TempDir(), Stdout: &stdout}, []string{"-t"}); err != nil {
		t.Fatal(err)
	}
	want := "example.com/direct\tv1.0.0\nexample.com/transitive\tv2.0.0\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestCommandTransitiveFlagsAreEquivalent(t *testing.T) {
	cmd := New()
	cmd.listTransitive = func(workDir string) ([]gocmd.Module, error) {
		return []gocmd.Module{{Path: "example.com/pkg", Version: "v1.0.0"}}, nil
	}
	var shortOut, longOut bytes.Buffer
	if err := cmd.Run(cli.Context{WorkDir: t.TempDir(), Stdout: &shortOut}, []string{"-t"}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(cli.Context{WorkDir: t.TempDir(), Stdout: &longOut}, []string{"--transitive"}); err != nil {
		t.Fatal(err)
	}
	if shortOut.String() != longOut.String() {
		t.Fatalf("-t output = %q, --transitive output = %q", shortOut.String(), longOut.String())
	}
}

func TestCommandRejectsUnknownListOption(t *testing.T) {
	err := New().Run(cli.Context{WorkDir: t.TempDir(), Stdout: &bytes.Buffer{}}, []string{"--all"})
	if err == nil || err.Error() != "usage: gox list [-t|--transitive]" {
		t.Fatalf("Run() error = %v", err)
	}
}
