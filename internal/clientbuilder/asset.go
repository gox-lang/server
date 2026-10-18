package clientbuilder

import (
	"path/filepath"
	"strings"
)

const (
	ModeDev  = "dev"
	ModeProd = "prod"
)

// Asset describes one client component build unit. It is intentionally
// independent from goxcompiler so the client builder can evolve separately.
type Asset struct {
	Name           string
	Framework      string
	Entry          string
	Path           string
	AbsPath        string
	Dev            bool
	Mode           string
	OutputDir      string
	OutputAbsDir   string
	ProjectRoot    string
	FrameworkRoot  string
	WrapperAbsPath string
}

func (a Asset) EffectiveRoot() string {
	if strings.TrimSpace(a.FrameworkRoot) != "" {
		return a.FrameworkRoot
	}
	return a.ProjectRoot
}

func (a Asset) BuildProfile() BuildProfile {
	if strings.EqualFold(a.Mode, ModeDev) || a.Dev {
		return BuildProfile{SourceMap: true, Minify: false}
	}
	return BuildProfile{SourceMap: false, Minify: true}
}

func (a Asset) PublicBaseName() string {
	return PublicBaseName(a.Name, a.Framework, a.Path, a.Mode)
}

func (a Asset) PublicEntryURL() string {
	return PublicEntryURL(a.Name, a.Framework, a.Path, a.Mode)
}

func (a Asset) PublicComponentDirName() string {
	return PublicComponentDirName(a.Name, a.Framework, a.Path, a.Mode)
}

func (a Asset) PublicMapURL() string {
	return PublicMapURL(a.Name, a.Framework, a.Path, a.Mode)
}

func (a Asset) ViteConfigPath() string {
	if strings.TrimSpace(a.OutputAbsDir) == "" {
		return ""
	}
	return filepath.Join(a.OutputAbsDir, a.PublicComponentDirName(), "vite.config.mjs")
}

type BuildProfile struct {
	SourceMap bool
	Minify    bool
}
