package toolchain

import (
	"os"
	"path/filepath"
)

type Paths struct {
	HomeDir      string
	ToolchainDir string
	VersionDir   string
	GoRoot       string
	GoBin        string
	GoExec       string
	GofmtExec    string
	ArchivePath  string
}

func ResolvePaths(info Info, platform Platform) (Paths, error) {
	goxHome, err := ResolveGoxHome()
	if err != nil {
		return Paths{}, err
	}

	toolchainDir := filepath.Join(goxHome, ".toolchain")
	versionDir := filepath.Join(toolchainDir, info.ExactVersion)
	goRoot := filepath.Join(versionDir, "go")
	goBin := filepath.Join(goRoot, "bin")

	suffix := ""
	if platform.OS == "windows" {
		suffix = ".exe"
	}
	return Paths{
		HomeDir:      goxHome,
		ToolchainDir: toolchainDir,
		VersionDir:   versionDir,
		GoRoot:       goRoot,
		GoBin:        goBin,
		GoExec:       filepath.Join(goBin, "go"+suffix),
		GofmtExec:    filepath.Join(goBin, "gofmt"+suffix),
		ArchivePath:  filepath.Join(versionDir, ArchiveName(info.ExactVersion, platform)),
	}, nil
}

func ResolveGoxHome() (string, error) {
	if override := os.Getenv("GOX_HOME"); override != "" {
		return filepath.Clean(override), nil
	}

	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}

	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", err
	}

	return filepath.Dir(execPath), nil
}
