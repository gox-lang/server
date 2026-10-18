package gocmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGoCommandEnvPinsManagedToolchain(t *testing.T) {
	goRoot := filepath.Join(t.TempDir(), "toolchain", "go1.24.0", "go")
	goExec := filepath.Join(goRoot, "bin", "go")

	env := goCommandEnv(goExec)

	if got := envValue(env, "GOROOT"); got != goRoot {
		t.Fatalf("GOROOT = %q; want %q", got, goRoot)
	}
	if got := envValue(env, "GOTOOLCHAIN"); got != "local" {
		t.Fatalf("GOTOOLCHAIN = %q; want local", got)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}
