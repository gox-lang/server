package scaffold

import (
	"os"
	"path/filepath"
)

func WriteDirs(root string, dirs []string) error {
	for _, dir := range dirs {
		path := filepath.Join(root, dir)

		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}

	return nil
}

func WriteFiles(root string, files []File) error {
	for _, file := range files {
		path := filepath.Join(root, file.Path)

		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}

		if err := os.WriteFile(path, []byte(file.Content), 0o644); err != nil {
			return err
		}
	}

	return nil
}
