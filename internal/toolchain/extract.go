package toolchain

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gox-lang/server/internal/logs"
)

func ExtractTarGz(archivePath string, destinationDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}

	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	progress := newProgressReader(file, "extracting", info.Size())

	gz, err := gzip.NewReader(progress)
	if err != nil {
		return err
	}

	defer gz.Close()

	tr := tar.NewReader(gz)
	extractedFiles := 0

	for {
		header, err := tr.Next()
		if err == io.EOF {
			progress.render(true)
			logs.Info("extracted files: %d", extractedFiles)
			return nil
		}

		if err != nil {
			return err
		}

		target, err := safeJoin(destinationDir, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}

			dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			if _, err := io.Copy(dst, tr); err != nil {
				_ = dst.Close()
				return err
			}

			if err := dst.Close(); err != nil {
				return err
			}

			extractedFiles++

		case tar.TypeSymlink:
			linkTarget, err := safeJoin(filepath.Dir(target), header.Linkname)
			if err != nil {
				return err
			}

			if err := os.Symlink(linkTarget, target); err != nil && !os.IsExist(err) {
				return err
			}
		}
	}
}

func safeJoin(root string, name string) (string, error) {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	target, err := filepath.Abs(filepath.Join(root, name))
	if err != nil {
		return "", err
	}

	if target != cleanRoot && !strings.HasPrefix(target, cleanRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe archive path: %s", name)
	}

	return target, nil
}
