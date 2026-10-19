package toolchain

import (
	"fmt"
	"os"

	"github.com/gox-lang/server/internal/logs"
)

func Ensure(info Info) (Paths, error) {
	logs.Info("checking Go toolchain: %s", info.ExactVersion)

	platform, err := CurrentPlatform()
	if err != nil {
		return Paths{}, err
	}

	logs.Info("platform: %s/%s", platform.OS, platform.Arch)

	paths, err := ResolvePaths(info, platform)
	if err != nil {
		return Paths{}, err
	}

	logs.Info("toolchain: %s", paths.VersionDir)

	if fileExists(paths.GoExec) {
		logs.Info("Go toolchain ready: %s", paths.GoExec)
		return paths, nil
	}

	logs.Info("Go toolchain not found")

	if err := os.MkdirAll(paths.VersionDir, 0o755); err != nil {
		return Paths{}, err
	}

	url := DownloadURL(info.ExactVersion, platform)

	if fileExists(paths.ArchivePath) {
		logs.Info("using cached archive: %s", paths.ArchivePath)
	} else {
		logs.Info("downloading Go toolchain: %s", url)
		if err := Download(url, paths.ArchivePath); err != nil {
			return Paths{}, err
		}
		logs.Info("downloaded archive: %s", paths.ArchivePath)
	}

	logs.Info("extracting Go toolchain to: %s", paths.VersionDir)
	extract := ExtractTarGz
	if platform.Ext == "zip" {
		extract = ExtractZip
	}
	if err := extract(paths.ArchivePath, paths.VersionDir); err != nil {
		return Paths{}, err
	}

	if !fileExists(paths.GoExec) {
		return Paths{}, fmt.Errorf("toolchain extraction failed: missing %s", paths.GoExec)
	}

	logs.Info("Go toolchain ready: %s", paths.GoExec)

	return paths, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
