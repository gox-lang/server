package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestModuleVersion(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{name: "nil build info", info: nil, want: ""},
		{name: "empty version", info: &debug.BuildInfo{}, want: ""},
		{name: "development build", info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, want: ""},
		{name: "tagged module", info: &debug.BuildInfo{Main: debug.Module{Version: "v1.0.0"}}, want: "v1.0.0"},
		{name: "module version is trimmed", info: &debug.BuildInfo{Main: debug.Module{Version: " v1.2.3 "}}, want: "v1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := moduleVersion(tt.info); got != tt.want {
				t.Fatalf("moduleVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersionPrefersInjectedVersion(t *testing.T) {
	original := GoxVersion
	GoxVersion = "v2.3.4"
	t.Cleanup(func() { GoxVersion = original })

	if got := Version(); got != "v2.3.4" {
		t.Fatalf("Version() = %q, want %q", got, "v2.3.4")
	}
}

func TestVersionDevelopmentFallback(t *testing.T) {
	original := GoxVersion
	GoxVersion = "dev"
	t.Cleanup(func() { GoxVersion = original })

	// `go test` builds the package as a development build. If that ever changes,
	// this assertion still accepts the module version embedded by the Go tool.
	got := Version()
	if got == "" {
		t.Fatal("Version() returned an empty version")
	}
}
