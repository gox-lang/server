package clientbuilder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FrameworkImportMapEntry struct {
	Specifier string
	URL       string
}

type frameworkModuleFacade struct {
	Name       string
	Specifiers []string
	Source     string
}

func frameworkModuleFacades(framework string) []frameworkModuleFacade {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "react":
		return []frameworkModuleFacade{
			{Name: "vendor/react", Specifiers: []string{"react"}},
			{Name: "vendor/react-dom", Specifiers: []string{"react-dom"}},
			{Name: "vendor/react-dom-client", Specifiers: []string{"react-dom/client"}},
			{Name: "vendor/react-jsx-runtime", Specifiers: []string{"react/jsx-runtime"}},
			{Name: "vendor/react-jsx-dev-runtime", Specifiers: []string{"react/jsx-dev-runtime"}},
		}
	case "preact":
		return []frameworkModuleFacade{
			{Name: "vendor/preact", Specifiers: []string{"preact"}},
			{Name: "vendor/preact-jsx-runtime", Specifiers: []string{"preact/jsx-runtime"}},
			{Name: "vendor/preact-jsx-dev-runtime", Specifiers: []string{"preact/jsx-dev-runtime"}},
			{Name: "vendor/preact-hooks", Specifiers: []string{"preact/hooks"}},
			{Name: "vendor/preact-compat", Specifiers: []string{"preact/compat"}},
		}
	case "solid":
		return []frameworkModuleFacade{
			{Name: "vendor/solid-js", Specifiers: []string{"solid-js"}},
			{Name: "vendor/solid-js-web", Specifiers: []string{"solid-js/web"}},
		}
	case "vue":
		return []frameworkModuleFacade{{Name: "vendor/vue", Specifiers: []string{"vue"}}}
	case "svelte":
		return []frameworkModuleFacade{
			{Name: "vendor/svelte", Specifiers: []string{"svelte"}},
			{Name: "vendor/svelte-internal-client", Specifiers: []string{"svelte/internal/client"}},
			{Name: "vendor/svelte-internal-flags-legacy", Specifiers: []string{"svelte/internal/flags/legacy"}},
			{Name: "vendor/svelte-internal-flags-async", Specifiers: []string{"svelte/internal/flags/async"}},
			{Name: "vendor/svelte-internal-flags-tracing", Specifiers: []string{"svelte/internal/flags/tracing"}},
		}
	default:
		return nil
	}
}

func frameworkPackageName(framework string) string {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "react":
		return "react"
	case "preact":
		return "preact"
	case "solid":
		return "solid-js"
	case "vue":
		return "vue"
	case "svelte":
		return "svelte"
	default:
		return ""
	}
}

// FrameworkVersion returns the effective installed framework version from the
// framework workspace node_modules package metadata.
func FrameworkVersion(projectRoot string, framework string) (string, error) {
	framework = strings.ToLower(strings.TrimSpace(framework))
	pkg := frameworkPackageName(framework)
	if pkg == "" {
		return "", fmt.Errorf("unknown client framework %q", framework)
	}
	path := filepath.Join(projectRoot, "client", framework, "node_modules", filepath.FromSlash(pkg), "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read installed %s version: %w", framework, err)
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", fmt.Errorf("read installed %s version: invalid %s: %w", framework, path, err)
	}
	version := strings.TrimSpace(meta.Version)
	if version == "" {
		return "", fmt.Errorf("read installed %s version: missing version in %s", framework, path)
	}
	for _, r := range version {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune(".-_+", r) {
			continue
		}
		return "", fmt.Errorf("read installed %s version: unsafe version %q", framework, version)
	}
	return version, nil
}

func FrameworkImportMapEntries(framework string, version string) []FrameworkImportMapEntry {
	facades := frameworkModuleFacades(framework)
	out := make([]FrameworkImportMapEntry, 0, len(facades))
	for _, facade := range facades {
		url := PublicClientPrefix + "/" + facade.Name + "." + strings.TrimSpace(version) + ".js"
		for _, specifier := range facade.Specifiers {
			out = append(out, FrameworkImportMapEntry{Specifier: specifier, URL: url})
		}
	}
	return out
}

func FrameworkImportSpecifiers(framework string) []string {
	facades := frameworkModuleFacades(framework)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(facades))
	for _, facade := range facades {
		for _, specifier := range facade.Specifiers {
			if strings.TrimSpace(specifier) == "" {
				continue
			}
			if _, ok := seen[specifier]; ok {
				continue
			}
			seen[specifier] = struct{}{}
			out = append(out, specifier)
		}
	}
	sort.Strings(out)
	return out
}

func writeFrameworkModuleFacades(asset Asset) error {
	projectRoot := asset.ProjectRoot
	framework := asset.Framework
	mode := asset.Mode
	frameworkRoot := assetFrameworkRoot(asset)
	facades := frameworkModuleFacades(framework)
	if len(facades) == 0 {
		return nil
	}
	if mode == "" {
		mode = ModeProd
	}
	dir := filepath.Join(projectRoot, ".build", "client", "runtime", "modules", strings.ToLower(strings.TrimSpace(framework)), mode)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for _, facade := range facades {
		file := filepath.Join(dir, filepath.FromSlash(facade.Name)+".js")
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			return err
		}
		source := frameworkModuleFacadeSource(frameworkRoot, framework, facade.Name)
		if cur, err := os.ReadFile(file); err == nil && string(cur) == source {
			continue
		}
		if err := os.WriteFile(file, []byte(source), 0644); err != nil {
			return err
		}
	}
	return nil
}

func frameworkModuleFacadeSource(frameworkRoot string, framework string, name string) string {
	modulePath := func(parts ...string) string {
		args := append([]string{frameworkRoot, "node_modules"}, parts...)
		return filepath.Join(args...)
	}
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "react":
		switch name {
		case "vendor/react":
			return `import * as ReactModule from ` + jsString(modulePath("react", "index.js")) + `
const React = ReactModule.default || ReactModule
export default React
export const Children = React.Children
export const Component = React.Component
export const Fragment = React.Fragment
export const Profiler = React.Profiler
export const PureComponent = React.PureComponent
export const StrictMode = React.StrictMode
export const Suspense = React.Suspense
export const cloneElement = React.cloneElement
export const createContext = React.createContext
export const createElement = React.createElement
export const createRef = React.createRef
export const forwardRef = React.forwardRef
export const isValidElement = React.isValidElement
export const lazy = React.lazy
export const memo = React.memo
export const startTransition = React.startTransition
export const use = React.use
export const useActionState = React.useActionState
export const useCallback = React.useCallback
export const useContext = React.useContext
export const useDebugValue = React.useDebugValue
export const useDeferredValue = React.useDeferredValue
export const useEffect = React.useEffect
export const useId = React.useId
export const useImperativeHandle = React.useImperativeHandle
export const useInsertionEffect = React.useInsertionEffect
export const useLayoutEffect = React.useLayoutEffect
export const useMemo = React.useMemo
export const useOptimistic = React.useOptimistic
export const useReducer = React.useReducer
export const useRef = React.useRef
export const useState = React.useState
export const useSyncExternalStore = React.useSyncExternalStore
export const useTransition = React.useTransition
export const version = React.version
`
		case "vendor/react-dom":
			return `import * as DOMModule from ` + jsString(modulePath("react-dom", "index.js")) + `
const ReactDOM = DOMModule.default || DOMModule
export default ReactDOM
export * from ` + jsString(modulePath("react-dom", "index.js")) + `
export const flushSync = ReactDOM.flushSync
`
		case "vendor/react-dom-client":
			return `import * as ClientModule from ` + jsString(modulePath("react-dom", "client.js")) + `
const Client = ClientModule.default || ClientModule
export const createRoot = Client.createRoot
export const hydrateRoot = Client.hydrateRoot
export const version = Client.version
`
		case "vendor/react-jsx-runtime":
			return `import * as JSXRuntime from ` + jsString(modulePath("react", "jsx-runtime.js")) + `
export const Fragment = JSXRuntime.Fragment
export const jsx = JSXRuntime.jsx
export const jsxs = JSXRuntime.jsxs
`
		case "vendor/react-jsx-dev-runtime":
			return `import * as JSXDevRuntime from ` + jsString(modulePath("react", "jsx-dev-runtime.js")) + `
export const Fragment = JSXDevRuntime.Fragment
export const jsxDEV = JSXDevRuntime.jsxDEV
`
		}
	case "preact":
		switch name {
		case "vendor/preact":
			return `export * from ` + jsString(modulePath("preact", "dist", "preact.module.js")) + `
`
		case "vendor/preact-jsx-runtime":
			return `export * from ` + jsString(modulePath("preact", "jsx-runtime", "dist", "jsxRuntime.module.js")) + `
`
		case "vendor/preact-jsx-dev-runtime":
			return `export * from ` + jsString(modulePath("preact", "jsx-runtime", "dist", "jsxRuntime.module.js")) + `
`
		case "vendor/preact-hooks":
			return `export * from ` + jsString(modulePath("preact", "hooks", "dist", "hooks.module.js")) + `
`
		case "vendor/preact-compat":
			return `export * from ` + jsString(modulePath("preact", "compat", "dist", "compat.module.js")) + `
`
		}
	case "solid":
		switch name {
		case "vendor/solid-js":
			return `export * from ` + jsString(modulePath("solid-js", "dist", "solid.js")) + `
`
		case "vendor/solid-js-web":
			return `export * from ` + jsString(modulePath("solid-js", "web", "dist", "web.js")) + `
`
		}
	case "vue":
		if name == "vendor/vue" {
			return `export * from ` + jsString(modulePath("vue", "dist", "vue.runtime.esm-bundler.js")) + `
`
		}
	case "svelte":
		switch name {
		case "vendor/svelte":
			return `export * from ` + jsString(modulePath("svelte", "src", "index-client.js")) + `
`
		case "vendor/svelte-internal-client":
			return `export * from ` + jsString(modulePath("svelte", "src", "internal", "client", "index.js")) + `
`
		case "vendor/svelte-internal-flags-legacy":
			return `import ` + jsString(modulePath("svelte", "src", "internal", "flags", "legacy.js")) + `
`
		case "vendor/svelte-internal-flags-async":
			return `import ` + jsString(modulePath("svelte", "src", "internal", "flags", "async.js")) + `
`
		case "vendor/svelte-internal-flags-tracing":
			return `import ` + jsString(modulePath("svelte", "src", "internal", "flags", "tracing.js")) + `
`
		}
	}
	return ""
}

func frameworkModuleFacadePath(asset Asset, name string) string {
	mode := asset.Mode
	if mode == "" {
		if asset.Dev {
			mode = ModeDev
		} else {
			mode = ModeProd
		}
	}
	return filepath.Join(asset.ProjectRoot, ".build", "client", "runtime", "modules", strings.ToLower(strings.TrimSpace(asset.Framework)), mode, filepath.FromSlash(name)+".js")
}
