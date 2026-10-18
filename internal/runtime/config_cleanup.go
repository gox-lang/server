package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func cleanupConfigBuildArtifacts(rootDir, currentBuildDir string) error {
	configRoot := filepath.Clean(filepath.Join(rootDir, ".build", "config"))
	currentBuildDir = filepath.Clean(currentBuildDir)
	if rootDir == "" || currentBuildDir == "." || currentBuildDir == configRoot {
		return fmt.Errorf("refusing to clean unsafe config build directory: %q", currentBuildDir)
	}
	rel, err := filepath.Rel(configRoot, currentBuildDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to clean config build outside %q: %q", configRoot, currentBuildDir)
	}

	entries, err := os.ReadDir(configRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	currentName := filepath.Base(currentBuildDir)
	for _, entry := range entries {
		if entry.Name() == currentName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(configRoot, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// CleanupConfigBuildArtifacts removes every config build except the one that
// has already been accepted by the runtime.
func CleanupConfigBuildArtifacts(rootDir string, loaded *LoadedConfig) error {
	if loaded == nil {
		return nil
	}
	return cleanupConfigBuildArtifacts(rootDir, loaded.BuildDir)
}
