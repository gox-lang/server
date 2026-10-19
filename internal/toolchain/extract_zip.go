package toolchain

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ExtractZip extracts the Windows Go distribution without invoking a shell.
func ExtractZip(archivePath, destinationDir string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, entry := range archive.File {
		target, err := safeJoin(destinationDir, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("unsupported archive entry: %s", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		src, err := entry.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, entry.Mode().Perm())
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, src)
		srcErr := src.Close()
		dstErr := dst.Close()
		if copyErr != nil {
			return copyErr
		}
		if srcErr != nil {
			return srcErr
		}
		if dstErr != nil {
			return dstErr
		}
	}
	return nil
}
