//go:build !windows

package selfupdate

import "os"

func replaceExecutable(target, candidate string) error {
	// Atomic replacement leaves already-running applications on their old inode.
	backup := target + ".previous"
	_ = os.Remove(backup)
	if err := os.Link(target, backup); err != nil {
		return err
	}
	return os.Rename(candidate, target)
}
