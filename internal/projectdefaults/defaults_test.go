package projectdefaults

import (
	"testing"

	"github.com/gox-lang/server/internal/buildinfo"
)

func TestManifestContainsCompleteDefaultToolchain(t *testing.T) {
	manifest := Manifest()

	if got := manifest.Dependencies[buildinfo.GoxModule]; got != buildinfo.GoxModuleVersion {
		t.Fatalf("GOX SDK version = %q, want %q", got, buildinfo.GoxModuleVersion)
	}
	if manifest.Toolchain.Node != buildinfo.NodeVersion {
		t.Fatalf("Node version = %q, want %q", manifest.Toolchain.Node, buildinfo.NodeVersion)
	}
	if manifest.Toolchain.PNPM != buildinfo.PNPMVersion {
		t.Fatalf("pnpm version = %q, want %q", manifest.Toolchain.PNPM, buildinfo.PNPMVersion)
	}
	if manifest.Toolchain.Vite != ViteVersion || manifest.Toolchain.TypeScript != TypeScriptVersion {
		t.Fatalf("frontend toolchain = %#v", manifest.Toolchain)
	}
}

func TestManifestContainsAllSupportedFrameworkPackages(t *testing.T) {
	manifest := Manifest()
	want := map[string]map[string]string{
		"react": {
			"react": ReactVersion, "react-dom": ReactDOMVersion,
			"@vitejs/plugin-react": ReactPluginVersion,
			"@types/react":         ReactTypesVersion, "@types/react-dom": ReactDOMTypesVersion,
		},
		"preact": {"preact": PreactVersion, "@preact/preset-vite": PreactPresetVersion},
		"solid":  {"solid-js": SolidVersion, "vite-plugin-solid": SolidPluginVersion},
		"vue":    {"vue": VueVersion, "@vitejs/plugin-vue": VuePluginVersion},
		"svelte": {"svelte": SvelteVersion, "@sveltejs/vite-plugin-svelte": SveltePluginVersion},
	}

	for framework, packages := range want {
		got, ok := manifest.Frameworks[framework]
		if !ok {
			t.Fatalf("missing framework %q", framework)
		}
		if len(got) != len(packages) {
			t.Fatalf("%s packages = %#v, want %#v", framework, got, packages)
		}
		for name, version := range packages {
			if got[name] != version {
				t.Fatalf("%s %s = %q, want %q", framework, name, got[name], version)
			}
		}
	}
}
