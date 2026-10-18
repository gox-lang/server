package clienttypes

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed static/*.d.ts
var staticTypes embed.FS

func Dir(projectRoot string) string { return filepath.Join(projectRoot, ".build") }

func EnsureStatic(projectRoot string) error {
	dir := Dir(projectRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	entries, err := staticTypes.ReadDir("static")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() != "websocket.d.ts" {
			continue
		}
		data, err := staticTypes.ReadFile("static/" + entry.Name())
		if err != nil {
			return err
		}
		if err := writeIfChanged(filepath.Join(dir, entry.Name()), data); err != nil {
			return err
		}
	}
	return nil
}

func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
