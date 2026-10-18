package toolchain

import (
	"fmt"
	"runtime"
)

type Platform struct {
	OS   string
	Arch string
	Ext  string
}

func CurrentPlatform() (Platform, error) {
	if runtime.GOOS == "windows" {
		return Platform{}, fmt.Errorf("gox does not support Windows because Go plugins are not supported there")
	}

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return Platform{}, fmt.Errorf("unsupported OS %q", runtime.GOOS)
	}

	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return Platform{}, fmt.Errorf("unsupported architecture %q", runtime.GOARCH)
	}

	return Platform{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		Ext:  "tar.gz",
	}, nil
}

func DownloadURL(version string, platform Platform) string {
	return fmt.Sprintf(
		"https://go.dev/dl/%s.%s-%s.%s",
		version,
		platform.OS,
		platform.Arch,
		platform.Ext,
	)
}

func ArchiveName(version string, platform Platform) string {
	return fmt.Sprintf("%s.%s-%s.%s", version, platform.OS, platform.Arch, platform.Ext)
}
