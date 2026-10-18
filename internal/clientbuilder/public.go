package clientbuilder

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"strings"
)

const PublicClientPrefix = "/_gox/client"

func PublicBaseName(name string, framework string, path string, mode string) string {
	framework = strings.ToLower(strings.TrimSpace(framework))
	path = canonicalPublicClientPath(framework, path)
	mode = NormalizeMode(mode, false)

	cleanName := sanitizePublicName(sourcePublicName(path))
	if cleanName == "" {
		cleanName = sanitizePublicName(name)
	}
	if cleanName == "" {
		cleanName = "client"
	}

	h := sha1.Sum([]byte(BuildIdentity(framework, path, mode, false)))
	return cleanName + "-" + hex.EncodeToString(h[:])[:8]
}

func PublicComponentDirName(name string, framework string, path string, mode string) string {
	return PublicBaseName(name, framework, path, mode)
}

func PublicEntryURL(name string, framework string, path string, mode string) string {
	return PublicClientPrefix + "/" + PublicComponentDirName(name, framework, path, mode) + "/" + PublicBaseName(name, framework, path, mode) + ".js"
}

func PublicMapURL(name string, framework string, path string, mode string) string {
	return PublicEntryURL(name, framework, path, mode) + ".map"
}

func canonicalPublicClientPath(framework string, path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.Trim(path, "/")
	framework = strings.ToLower(strings.TrimSpace(framework))
	if path == "" {
		return ""
	}
	lower := strings.ToLower(path)
	if framework != "" {
		prefix := "client/" + framework + "/"
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimPrefix(path, path[:len(prefix)])
		}
	}
	if strings.HasPrefix(lower, "client/") {
		return strings.TrimPrefix(path, path[:len("client/")])
	}
	return path
}

func sourcePublicName(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

func sanitizePublicName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	var b strings.Builder
	dash := false
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
