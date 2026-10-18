package deps

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/gocmd"
	"golang.org/x/mod/modfile"
)

// EnsureSDK aligns an existing project's SDK with the server before plugins
// are compiled. Matching projects require no Go command or network access.
func EnsureSDK(root, goExec string) error {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return err
	}
	mod, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return err
	}
	cfg, err := ReadGoxJSON(root)
	if err != nil {
		return err
	}
	matched := false
	for _, req := range mod.Require {
		if req.Mod.Path == buildinfo.GoxModule {
			matched = req.Mod.Version == buildinfo.GoxModuleVersion
		}
	}
	if matched && cfg.Dependencies[buildinfo.GoxModule] == buildinfo.GoxModuleVersion {
		return nil
	}
	snapshot, err := snapshotDependencyFiles(root)
	if err != nil {
		return err
	}
	syncErr := func() error {
		if !matched {
			if err := gocmd.Get(goExec, root, buildinfo.GoxModule+"@"+buildinfo.GoxModuleVersion); err != nil {
				return err
			}
		}
		cfg.Dependencies[buildinfo.GoxModule] = buildinfo.GoxModuleVersion
		return WriteGoxJSON(root, cfg)
	}()
	if syncErr != nil {
		if err := snapshot.restore(root); err != nil {
			return fmt.Errorf("synchronize SDK: %w; restore: %v", syncErr, err)
		}
		return fmt.Errorf("synchronize SDK %s: %w", buildinfo.GoxModuleVersion, syncErr)
	}
	return nil
}
