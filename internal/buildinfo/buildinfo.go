package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
)

var (
	// GoxVersion may be overridden for official builds with -ldflags -X.
	// When left as "dev", Version falls back to the Go module build info.
	GoxVersion       = "dev"
	GoVersion        = runtime.Version()
	GoxModule        = "github.com/gox-lang/gox"
	GoxModuleVersion = "v1.1.1"
	NodeVersion      = "node22.18.0"
	PNPMVersion      = "pnpm11.9.0"
)

func Version() string {
	if GoxVersion != "" && GoxVersion != "dev" {
		return GoxVersion
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		if version := moduleVersion(info); version != "" {
			return version
		}
	}

	return "dev"
}

func moduleVersion(info *debug.BuildInfo) string {
	if info == nil {
		return ""
	}

	version := strings.TrimSpace(info.Main.Version)
	if version == "" || version == "(devel)" {
		return ""
	}

	return version
}
