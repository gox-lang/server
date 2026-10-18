package clientbuilder

import (
	"path/filepath"
	"strings"
)

// BuildIdentity returns the canonical identity of a client build. Declaration
// names and .gox module paths are intentionally excluded so equivalent
// declarations can reuse the same artifact.
func BuildIdentity(framework string, sourcePath string, mode string, dev bool) string {
	framework = strings.ToLower(strings.TrimSpace(framework))
	sourcePath = canonicalBuildSourcePath(framework, sourcePath)
	mode = NormalizeMode(mode, dev)
	if framework == "" || sourcePath == "" {
		return ""
	}
	return framework + "\x00" + sourcePath + "\x00" + mode
}

func NormalizeMode(mode string, dev bool) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "" {
		return mode
	}
	if dev {
		return ModeDev
	}
	return ModeProd
}

func canonicalBuildSourcePath(framework string, sourcePath string) string {
	sourcePath = filepath.ToSlash(strings.TrimSpace(sourcePath))
	sourcePath = strings.Trim(sourcePath, "/")
	if sourcePath == "" {
		return ""
	}
	framework = strings.ToLower(strings.TrimSpace(framework))
	lower := strings.ToLower(sourcePath)
	if framework != "" {
		prefix := "client/" + framework + "/"
		if strings.HasPrefix(lower, prefix) {
			return sourcePath[len(prefix):]
		}
	}
	if strings.HasPrefix(lower, "client/") {
		return sourcePath[len("client/"):]
	}
	return sourcePath
}

func (a Asset) BuildIdentity() string {
	sourcePath := a.Path
	if strings.TrimSpace(sourcePath) == "" {
		sourcePath = a.Entry
	}
	return BuildIdentity(a.Framework, sourcePath, a.Mode, a.Dev)
}
