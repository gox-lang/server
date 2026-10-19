package ls

import (
	"bytes"
	"errors"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/toolchain"
	"golang.org/x/tools/go/packages"
)

func TestManagedToolchainWithoutGoOnPath(t *testing.T) {
	root := runtime.GOROOT()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GOROOT", "/unavailable-go")
	t.Setenv("GOTOOLCHAIN", "auto")
	oldContext := build.Default
	restore, err := useToolchain(toolchain.Paths{GoRoot: root, GoBin: filepath.Join(root, "bin")})
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	found, err := exec.LookPath("go")
	if err != nil || filepath.Dir(found) != filepath.Join(root, "bin") {
		t.Fatalf("Go executable %q: %v", found, err)
	}
	if build.Default.GOROOT != root {
		t.Fatal("source discovery uses a different GOROOT")
	}
	loaded, err := packages.Load(&packages.Config{Mode: packages.NeedName | packages.NeedFiles}, "fmt")
	if err != nil || len(loaded) != 1 || len(loaded[0].Errors) != 0 || len(loaded[0].GoFiles) == 0 {
		t.Fatalf("cannot load standard library: %v %v", loaded, err)
	}
	restore()
	if os.Getenv("GOROOT") != "/unavailable-go" || os.Getenv("GOTOOLCHAIN") != "auto" || build.Default.GOROOT != oldContext.GOROOT {
		t.Fatal("environment not restored")
	}
}

func TestToolchainFailureDoesNotWriteProtocolOutput(t *testing.T) {
	command := New()
	failure := errors.New("download unavailable")
	command.ensureToolchain = func(info toolchain.Info) (toolchain.Paths, error) {
		if info.ExactVersion != buildinfo.GoVersion {
			t.Fatalf("toolchain version = %s", info.ExactVersion)
		}
		return toolchain.Paths{}, failure
	}
	var stdout, stderr bytes.Buffer
	err := command.Run(cli.Context{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("non-protocol stdout: %q", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("missing startup progress")
	}
}
