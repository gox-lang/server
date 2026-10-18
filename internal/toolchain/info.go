package toolchain

import "strings"

type Info struct {
	ExactVersion string
	ModVersion   string
}

func New(exactVersion string) Info {
	return Info{
		ExactVersion: exactVersion,
		ModVersion:   ToModVersion(exactVersion),
	}
}

func ToModVersion(exactVersion string) string {
	version := strings.TrimPrefix(exactVersion, "go")

	parts := strings.Split(version, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}

	return version
}
