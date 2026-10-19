package selfupdate

import (
	"fmt"
	"os"
)

func replaceExecutable(target, candidate string) error {
	// Windows permits renaming an in-use executable, but not overwriting it.
	backup := target + ".previous"
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("previous GOX version is still in use: %w", err)
	}
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(candidate, target); err != nil {
		if rollback := os.Rename(backup, target); rollback != nil {
			return fmt.Errorf("install failed: %v; restore %s manually: %w", err, backup, rollback)
		}
		return err
	}
	return nil
}
