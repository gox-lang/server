package deps

import (
	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/gocmd"
	"github.com/gox-lang/server/internal/logs"
)

// Tidy synchronizes deps.go and go.mod/go.sum from gox.json without modifying
// gox.json. deps.go remains in the project so the Go toolchain can see
// dependencies that are referenced only by GOX files.
func Tidy(root string, goExec string) error {
	cfg, err := ReadGoxJSON(root)
	if err != nil {
		return err
	}

	logs.Info("synchronizing Go dependency bridge...")
	if err := WriteDepsGo(root, cfg, buildinfo.GoxModule); err != nil {
		return err
	}

	return gocmd.ModTidy(goExec, root)
}
