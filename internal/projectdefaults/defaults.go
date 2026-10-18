package projectdefaults

import (
	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/deps"
)

var (
	ViteVersion       = "^8.1.0"
	TypeScriptVersion = "^6.0.3"

	ReactVersion         = "^19.2.0"
	ReactDOMVersion      = "^19.2.0"
	ReactPluginVersion   = "^6.0.3"
	ReactTypesVersion    = "^19.2.0"
	ReactDOMTypesVersion = "^19.2.0"

	PreactVersion       = "^10.28.0"
	PreactPresetVersion = "latest"

	SolidVersion       = "^1.9.0"
	SolidPluginVersion = "latest"

	VueVersion       = "^3.5.0"
	VuePluginVersion = "^6.0.0"

	SvelteVersion       = "^5.0.0"
	SveltePluginVersion = "^6.0.0"
)

func Manifest() deps.GoxJSON {
	return deps.GoxJSON{
		Dependencies: map[string]string{
			buildinfo.GoxModule: buildinfo.GoxModuleVersion,
		},
		Toolchain: deps.Toolchain{
			Node:       buildinfo.NodeVersion,
			PNPM:       buildinfo.PNPMVersion,
			Vite:       ViteVersion,
			TypeScript: TypeScriptVersion,
		},
		Frameworks: deps.Frameworks{
			"react": {
				"react":                ReactVersion,
				"react-dom":            ReactDOMVersion,
				"@vitejs/plugin-react": ReactPluginVersion,
				"@types/react":         ReactTypesVersion,
				"@types/react-dom":     ReactDOMTypesVersion,
			},
			"preact": {
				"preact":              PreactVersion,
				"@preact/preset-vite": PreactPresetVersion,
			},
			"solid": {
				"solid-js":          SolidVersion,
				"vite-plugin-solid": SolidPluginVersion,
			},
			"vue": {
				"vue":                VueVersion,
				"@vitejs/plugin-vue": VuePluginVersion,
			},
			"svelte": {
				"svelte":                       SvelteVersion,
				"@sveltejs/vite-plugin-svelte": SveltePluginVersion,
			},
		},
	}
}
