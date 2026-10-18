package clientbuilder

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gox-lang/server/internal/clientframework"
	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/projectdefaults"
)

// FrameworkDefinition contains the build behavior for a supported framework.
// Framework names and support states come from clientframework.
type FrameworkDefinition struct {
	Name              string
	Dependencies      map[string]string
	DevDependencies   map[string]string
	PluginSetup       string
	PluginExprs       []string
	VendorPackages    []string
	TypeScriptOptions map[string]any
}

func Framework(framework string) (FrameworkDefinition, bool) {
	registryDef, ok := clientframework.Lookup(framework)
	if !ok || registryDef.Status != clientframework.Supported {
		return FrameworkDefinition{}, false
	}

	switch registryDef.Name {
	case "react":
		return FrameworkDefinition{
			Name: "react",
			Dependencies: map[string]string{
				"react":     projectdefaults.ReactVersion,
				"react-dom": projectdefaults.ReactDOMVersion,
			},
			DevDependencies: map[string]string{
				"vite":                 projectdefaults.ViteVersion,
				"@vitejs/plugin-react": projectdefaults.ReactPluginVersion,
				"@types/react":         projectdefaults.ReactTypesVersion,
				"@types/react-dom":     projectdefaults.ReactDOMTypesVersion,
				"typescript":           projectdefaults.TypeScriptVersion,
			},
			PluginSetup:       `const react = (await import(require.resolve("@vitejs/plugin-react"))).default`,
			PluginExprs:       []string{"react()"},
			VendorPackages:    []string{"react", "react-dom", "scheduler"},
			TypeScriptOptions: map[string]any{"jsx": "react-jsx"},
		}, true
	case "preact":
		return FrameworkDefinition{
			Name: "preact",
			Dependencies: map[string]string{
				"preact": projectdefaults.PreactVersion,
			},
			DevDependencies: map[string]string{
				"vite":                projectdefaults.ViteVersion,
				"@preact/preset-vite": projectdefaults.PreactPresetVersion,
				"typescript":          projectdefaults.TypeScriptVersion,
			},
			PluginSetup: `const goxPreactPreset = await import(require.resolve("@preact/preset-vite"))
const preact = goxPreactPreset.default?.default || goxPreactPreset.default || goxPreactPreset.preact || goxPreactPreset`,
			PluginExprs:       []string{"preact()"},
			VendorPackages:    []string{"preact"},
			TypeScriptOptions: map[string]any{"jsx": "react-jsx", "jsxImportSource": "preact"},
		}, true
	case "solid":
		return FrameworkDefinition{
			Name: "solid",
			Dependencies: map[string]string{
				"solid-js": projectdefaults.SolidVersion,
			},
			DevDependencies: map[string]string{
				"vite":              projectdefaults.ViteVersion,
				"vite-plugin-solid": projectdefaults.SolidPluginVersion,
				"typescript":        projectdefaults.TypeScriptVersion,
			},
			PluginSetup:       `const solid = (await import(require.resolve("vite-plugin-solid"))).default`,
			PluginExprs:       []string{"solid()"},
			VendorPackages:    []string{"solid-js"},
			TypeScriptOptions: map[string]any{"jsx": "preserve", "jsxImportSource": "solid-js"},
		}, true
	case "vue":
		return FrameworkDefinition{
			Name: "vue",
			Dependencies: map[string]string{
				"vue": projectdefaults.VueVersion,
			},
			DevDependencies: map[string]string{
				"vite":               projectdefaults.ViteVersion,
				"@vitejs/plugin-vue": projectdefaults.VuePluginVersion,
				"typescript":         projectdefaults.TypeScriptVersion,
			},
			PluginSetup:       `const vue = (await import(require.resolve("@vitejs/plugin-vue"))).default`,
			PluginExprs:       []string{"vue()"},
			TypeScriptOptions: map[string]any{},
			VendorPackages: []string{
				"vue",
				"@vue/runtime-dom",
				"@vue/runtime-core",
				"@vue/reactivity",
				"@vue/shared",
				"@vue/compiler-dom",
				"@vue/compiler-core",
			},
		}, true
	case "svelte":
		return FrameworkDefinition{
			Name: "svelte",
			Dependencies: map[string]string{
				"svelte": projectdefaults.SvelteVersion,
			},
			DevDependencies: map[string]string{
				"vite":                         projectdefaults.ViteVersion,
				"@sveltejs/vite-plugin-svelte": projectdefaults.SveltePluginVersion,
				"typescript":                   projectdefaults.TypeScriptVersion,
			},
			PluginSetup:       `const { svelte } = await import(require.resolve("@sveltejs/vite-plugin-svelte"))`,
			PluginExprs:       []string{"svelte()"},
			VendorPackages:    []string{"svelte"},
			TypeScriptOptions: map[string]any{},
		}, true
	default:
		return FrameworkDefinition{}, false
	}
}

func ProjectManagedDependencies(projectRoot, framework string) (dependencies map[string]string, devDependencies map[string]string, err error) {
	def, ok := Framework(framework)
	if !ok || len(def.Dependencies)+len(def.DevDependencies) == 0 {
		return nil, nil, fmt.Errorf("unsupported client framework %q", framework)
	}

	manifest, err := deps.ReadGoxJSON(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	frameworkVersions, ok := manifest.Frameworks[def.Name]
	if !ok {
		return nil, nil, fmt.Errorf("%s frameworks.%s is required", deps.GoxJSONFile, def.Name)
	}

	dependencies = make(map[string]string, len(def.Dependencies))
	for name := range def.Dependencies {
		version := strings.TrimSpace(frameworkVersions[name])
		if version == "" {
			return nil, nil, fmt.Errorf("%s frameworks.%s[%q] is required", deps.GoxJSONFile, def.Name, name)
		}
		dependencies[name] = version
	}

	devDependencies = make(map[string]string, len(def.DevDependencies))
	for name := range def.DevDependencies {
		var version string
		switch name {
		case "vite":
			version = strings.TrimSpace(manifest.Toolchain.Vite)
		case "typescript":
			version = strings.TrimSpace(manifest.Toolchain.TypeScript)
		default:
			version = strings.TrimSpace(frameworkVersions[name])
		}
		if version == "" {
			if name == "vite" || name == "typescript" {
				return nil, nil, fmt.Errorf("%s toolchain.%s is required", deps.GoxJSONFile, name)
			}
			return nil, nil, fmt.Errorf("%s frameworks.%s[%q] is required", deps.GoxJSONFile, def.Name, name)
		}
		devDependencies[name] = version
	}
	return dependencies, devDependencies, nil
}

func ManagedDependencies(framework string) (dependencies map[string]string, devDependencies map[string]string, ok bool) {
	def, ok := Framework(framework)
	if !ok || len(def.Dependencies)+len(def.DevDependencies) == 0 {
		return nil, nil, false
	}
	return cloneStringMap(def.Dependencies), cloneStringMap(def.DevDependencies), true
}

func vitePlugin(framework string) (string, []string, error) {
	def, ok := Framework(framework)
	if !ok {
		return "", nil, fmt.Errorf("unsupported client framework %q", framework)
	}
	return def.PluginSetup, append([]string(nil), def.PluginExprs...), nil
}

func vendorPackages(framework string) []string {
	def, ok := Framework(framework)
	if !ok || len(def.VendorPackages) == 0 {
		return nil
	}
	out := append([]string(nil), def.VendorPackages...)
	sort.Strings(out)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
