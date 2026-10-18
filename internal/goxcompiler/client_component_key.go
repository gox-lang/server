package gox

import (
	"path/filepath"
	"strings"
)

// ClientDeclarationKey returns the stable identity of one client declaration.
// Build identity is tracked separately so multiple declarations may reuse one
// equivalent bundle without colliding at runtime.
func ClientDeclarationKey(modulePath string, name string, framework string, src string, dev bool) string {
	modulePath = canonicalClientIdentityPath(modulePath)
	name = strings.TrimSpace(name)
	build := ClientComponentKey(framework, src, dev)
	if modulePath == "" || name == "" || build == "" {
		return ""
	}
	return modulePath + "#" + name + "#" + build
}

// ClientComponentKey returns the source-level portion of a client identity.
// It remains available for compatibility with compile results produced before
// declaration identities included module paths and names.
func ClientComponentKey(framework string, src string, dev bool) string {
	framework = strings.ToLower(strings.TrimSpace(framework))
	src = strings.TrimSpace(src)
	if framework == "" || src == "" {
		return ""
	}
	src = filepath.ToSlash(filepath.Clean(src))
	src = strings.TrimPrefix(src, "./")
	src = strings.Trim(src, "/")

	mode := "prod"
	if dev {
		mode = "dev"
	}

	return framework + "#" + src + "#" + mode
}

func canonicalClientIdentityPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = filepath.ToSlash(filepath.Clean(value))
	return strings.TrimPrefix(value, "./")
}
