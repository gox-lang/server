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
	return platformFor(runtime.GOOS, runtime.GOARCH)
}

func platformFor(goos, goarch string) (Platform, error) {
	if goos != "linux" && goos != "darwin" && goos != "windows" {
		return Platform{}, fmt.Errorf("unsupported OS %q", goos)
	}

	if goarch != "amd64" && goarch != "arm64" {
		return Platform{}, fmt.Errorf("unsupported architecture %q", goarch)
	}

	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return Platform{
		OS:   goos,
		Arch: goarch,
		Ext:  ext,
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
