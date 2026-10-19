package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"golang.org/x/mod/semver"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (m *Manager) asset(r Release, name string) (string, error) {
	expected := "https://github.com/gox-lang/server/releases/download/" + r.Version + "/" + name
	for _, a := range r.Assets {
		if a.Name == name {
			if a.URL != expected {
				return "", fmt.Errorf("untrusted release asset URL")
			}
			return a.URL, nil
		}
	}
	return "", fmt.Errorf("release %s has no %s", r.Version, name)
}

func (m *Manager) Install(ctx context.Context, r Release) error {
	if !m.Newer(r) {
		return fmt.Errorf("release is not newer than this GOX version")
	}
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	suffix, binary := ".tar.gz", "gox"
	if m.OS == "windows" {
		suffix, binary = ".zip", "gox.exe"
	}
	name := "gox_" + strings.TrimPrefix(r.Version, "v") + "_" + m.OS + "_" + m.Arch + suffix
	url, err := m.asset(r, name)
	if err != nil {
		return err
	}
	checksumURL, err := m.asset(r, "checksums.sha256")
	if err != nil {
		return err
	}
	checksums, err := m.get(ctx, checksumURL, 1<<20)
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			if expected != "" {
				return fmt.Errorf("duplicate checksum")
			}
			expected = fields[0]
		}
	}
	if decoded, err := hex.DecodeString(expected); err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("missing or invalid checksum for %s", name)
	}
	archive, err := m.get(ctx, url, 256<<20)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), expected) {
		return fmt.Errorf("release checksum mismatch")
	}
	data, err := extractExecutable(archive, binary, suffix)
	if err != nil {
		return err
	}
	dir := filepath.Dir(m.Executable)
	stage, err := os.MkdirTemp(dir, ".gox-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	candidate := filepath.Join(stage, binary)
	if err := os.WriteFile(candidate, data, 0755); err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(checkCtx, candidate, "version").Output()
	if err != nil {
		return fmt.Errorf("candidate cannot start: %w", err)
	}
	if strings.TrimSpace(string(output)) != "GOX Server "+r.Version {
		return fmt.Errorf("candidate version does not match release")
	}
	// Another process may already have installed this release while we waited.
	currentOutput, err := exec.CommandContext(checkCtx, m.Executable, "version").Output()
	installed := strings.TrimPrefix(strings.TrimSpace(string(currentOutput)), "GOX Server ")
	if err == nil && stable.MatchString(installed) && semver.Compare(installed, r.Version) >= 0 {
		return nil
	}
	return replaceExecutable(m.Executable, candidate)
}

func extractExecutable(data []byte, name, suffix string) ([]byte, error) {
	var result []byte
	read := func(reader io.Reader) error {
		if result != nil {
			return fmt.Errorf("duplicate executable in release")
		}
		contents, err := io.ReadAll(io.LimitReader(reader, (256<<20)+1))
		if err != nil {
			return err
		}
		if len(contents) == 0 || len(contents) > 256<<20 {
			return fmt.Errorf("invalid executable size")
		}
		result = contents
		return nil
	}
	if suffix == ".zip" {
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, entry := range archive.File {
			if entry.Name == name {
				if !entry.Mode().IsRegular() {
					return nil, fmt.Errorf("executable is not a regular file")
				}
				r, err := entry.Open()
				if err != nil {
					return nil, err
				}
				err = read(r)
				r.Close()
				if err != nil {
					return nil, err
				}
			}
		}
	} else {
		gzipReader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		archive := tar.NewReader(gzipReader)
		for {
			header, err := archive.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if header.Name == name {
				if header.Typeflag != tar.TypeReg {
					return nil, fmt.Errorf("executable is not a regular file")
				}
				if err := read(archive); err != nil {
					return nil, err
				}
			}
		}
	}
	if result == nil {
		return nil, fmt.Errorf("release executable missing")
	}
	return result, nil
}
