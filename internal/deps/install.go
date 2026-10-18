package deps

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/gocmd"
	"github.com/gox-lang/server/internal/logs"
)

func Install(root string, goExec string) error {
	cfg, err := ReadGoxJSON(root)
	if err != nil {
		return err
	}

	logs.Info("synchronizing Go dependency bridge...")
	if err := WriteDepsGo(root, cfg, buildinfo.GoxModule); err != nil {
		return err
	}

	for _, dep := range SortedDependencies(cfg) {
		if dep.Path == buildinfo.GoxModule {
			continue
		}
		version := dep.Version
		if version == "" {
			version = "latest"
		}
		if err := gocmd.Get(goExec, root, fmt.Sprintf("%s@%s", dep.Path, version)); err != nil {
			return err
		}
	}

	return gocmd.ModTidy(goExec, root)
}

// AddAndInstall adds a dependency and reconciles the Go module atomically.
// If reconciliation fails, dependency metadata and Go module files are restored.
func AddAndInstall(root, goExec, module, version string) error {
	return changeAndInstall(root, goExec, func() error {
		return AddDependency(root, module, version)
	})
}

// RemoveAndInstall removes a dependency and reconciles the Go module atomically.
// If reconciliation fails, dependency metadata and Go module files are restored.
func RemoveAndInstall(root, goExec, module string) error {
	return changeAndInstall(root, goExec, func() error {
		return RemoveDependency(root, module)
	})
}

func changeAndInstall(root, goExec string, change func() error) error {
	snapshot, err := snapshotDependencyFiles(root)
	if err != nil {
		return err
	}
	if err := change(); err != nil {
		return err
	}
	if err := Install(root, goExec); err != nil {
		if restoreErr := snapshot.restore(root); restoreErr != nil {
			return fmt.Errorf("dependency operation failed: %w; restore failed: %v", err, restoreErr)
		}
		return err
	}
	return nil
}

type dependencyFileState struct {
	name   string
	data   []byte
	mode   os.FileMode
	exists bool
}

type dependencySnapshot []dependencyFileState

func snapshotDependencyFiles(root string) (dependencySnapshot, error) {
	names := []string{GoxJSONFile, DepsGoFile, "go.mod", "go.sum"}
	result := make(dependencySnapshot, 0, len(names))
	for _, name := range names {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				result = append(result, dependencyFileState{name: name})
				continue
			}
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		result = append(result, dependencyFileState{name: name, data: data, mode: info.Mode().Perm(), exists: true})
	}
	return result, nil
}

func (s dependencySnapshot) restore(root string) error {
	for _, file := range s {
		path := filepath.Join(root, file.name)
		if !file.exists {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := os.WriteFile(path, file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}
