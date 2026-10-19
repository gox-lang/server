package ls

import (
	"fmt"
	"github.com/gox-lang/server/internal/toolchain"
	"go/build"
	"os"
)

// The CLI owns this process. Configure Go before starting any LSP workers so
// go/packages subprocesses and source discovery use the same managed toolchain.
func useToolchain(paths toolchain.Paths) (func(), error) {
	previous := build.Default
	type variable struct {
		value  string
		exists bool
	}
	saved := map[string]variable{}
	restore := func() {
		build.Default = previous
		for key, old := range saved {
			if old.exists {
				_ = os.Setenv(key, old.value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}
	for key, value := range map[string]string{
		"GOROOT":      paths.GoRoot,
		"GOTOOLCHAIN": "local",
		"PATH":        paths.GoBin + string(os.PathListSeparator) + os.Getenv("PATH"),
	} {
		old, exists := os.LookupEnv(key)
		saved[key] = variable{old, exists}
		if err := os.Setenv(key, value); err != nil {
			restore()
			return nil, fmt.Errorf("configure language server %s: %w", key, err)
		}
	}
	build.Default.GOROOT = paths.GoRoot
	return restore, nil
}
