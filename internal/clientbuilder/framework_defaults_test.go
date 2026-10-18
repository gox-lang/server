package clientbuilder

import (
	"testing"

	"github.com/gox-lang/server/internal/projectdefaults"
)

func TestFrameworkManagedDependenciesUseProjectDefaults(t *testing.T) {
	manifest := projectdefaults.Manifest()

	for _, framework := range []string{"react", "preact", "solid", "vue", "svelte"} {
		dependencies, devDependencies, ok := ManagedDependencies(framework)
		if !ok {
			t.Fatalf("ManagedDependencies(%q) is not available", framework)
		}

		want := manifest.Frameworks[framework]
		for name, version := range want {
			got, exists := dependencies[name]
			if !exists {
				got, exists = devDependencies[name]
			}
			if !exists {
				t.Fatalf("%s package %q is missing from managed dependencies", framework, name)
			}
			if got != version {
				t.Fatalf("%s package %q = %q, want %q", framework, name, got, version)
			}
		}

		if got := devDependencies["vite"]; got != manifest.Toolchain.Vite {
			t.Fatalf("%s vite = %q, want %q", framework, got, manifest.Toolchain.Vite)
		}
		if got := devDependencies["typescript"]; got != manifest.Toolchain.TypeScript {
			t.Fatalf("%s typescript = %q, want %q", framework, got, manifest.Toolchain.TypeScript)
		}
	}
}
