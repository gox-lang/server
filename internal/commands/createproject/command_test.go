package createproject

import (
	"github.com/gox-lang/server/internal/cli"
	"os"
	"path/filepath"
	"testing"
)

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		name string
		demo bool
		bad  bool
	}{
		{[]string{"app1"}, "app1", false, false},
		{[]string{"--demo"}, "demo", true, false},
		{[]string{"workspace", "--demo"}, "workspace", true, false},
		{[]string{"--demo", "workspace"}, "workspace", true, false},
		{nil, "", false, true},
		{[]string{"--unknown"}, "", false, true},
		{[]string{"--demo", "--demo"}, "", false, true},
		{[]string{"one", "two"}, "", false, true},
	} {
		name, demo, err := parseArgs(tc.args)
		if (err != nil) != tc.bad || (!tc.bad && (name != tc.name || demo != tc.demo)) {
			t.Fatalf("%v: %q %v %v", tc.args, name, demo, err)
		}
	}
}

func TestInvalidDestinationRejectedBeforeToolchain(t *testing.T) {
	t.Setenv("GOX_HOME", filepath.Join(t.TempDir(), "unused-tools"))
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "existing"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"../outside"}, {"existing", "--demo"}, {"--unknown"}} {
		if err := New().Run(cli.Context{WorkDir: root}, args); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
	if _, err := os.Stat(os.Getenv("GOX_HOME")); !os.IsNotExist(err) {
		t.Fatalf("toolchain touched: %v", err)
	}
}
