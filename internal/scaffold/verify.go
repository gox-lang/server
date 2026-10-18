package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

func VerifyProject(root string, opts Options) error {
	for _, dir := range ProjectDirs() {
		if err := expectDir(filepath.Join(root, dir)); err != nil {
			return err
		}
	}

	for _, file := range ProjectFiles(opts) {
		if err := expectFile(filepath.Join(root, file.Path)); err != nil {
			return err
		}
	}

	return nil
}

func expectDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("expected directory %q: %w", path, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("expected directory %q, found file", path)
	}

	return nil
}

func expectFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("expected file %q: %w", path, err)
	}

	if info.IsDir() {
		return fmt.Errorf("expected file %q, found directory", path)
	}

	return nil
}
