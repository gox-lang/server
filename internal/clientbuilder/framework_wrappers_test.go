package clientbuilder

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFrameworkWrapperSourcesExportMountForSupportedFrameworks(t *testing.T) {
	projectRoot := t.TempDir()
	frameworkRoot := filepath.Join(projectRoot, "client", "react")
	asset := Asset{
		Name:          "menu",
		Framework:     "react",
		AbsPath:       filepath.Join(projectRoot, "client", "react", "menu", "menu.jsx"),
		ProjectRoot:   projectRoot,
		FrameworkRoot: frameworkRoot,
		Mode:          ModeDev,
	}

	cases := map[string][]string{
		"react": {
			`import React from "react"`,
			`import { createRoot } from "react-dom/client"`,
			`export function mount(target, props = {})`,
			`root.render(React.createElement(Component, props))`,
			`return () => root.unmount()`,
		},
		"preact": {
			`import { h, render } from "preact"`,
			`export function mount(target, props = {})`,
			`render(h(Component, props), target)`,
			`return () => render(null, target)`,
		},
		"solid": {
			`import { render } from "solid-js/web"`,
			`export function mount(target, props = {})`,
			`return render(() => Component(props), target)`,
		},
		"vue": {
			`import { createApp } from "vue"`,
			`export function mount(target, props = {})`,
			`const app = createApp(Component, props)`,
			`return () => app.unmount()`,
		},
		"svelte": {
			`import { mount as svelteMount, unmount as svelteUnmount } from "svelte"`,
			`export function mount(target, props = {})`,
			`const instance = svelteMount(Component, { target, props })`,
			`return () => svelteUnmount(instance)`,
		},
	}

	for framework, expected := range cases {
		t.Run(framework, func(t *testing.T) {
			asset.Framework = framework
			asset.FrameworkRoot = filepath.Join(projectRoot, "client", framework)
			asset.AbsPath = filepath.Join(projectRoot, "client", framework, "menu", "menu.jsx")
			source := frameworkWrapperSource(asset)
			if !strings.Contains(source, `import Component from `) {
				t.Fatalf("wrapper does not import user component:\n%s", source)
			}
			for _, want := range expected {
				if !strings.Contains(source, want) {
					t.Fatalf("wrapper for %s missing %q:\n%s", framework, want, source)
				}
			}
			if strings.Contains(source, `Component.mount`) || strings.Contains(source, `not implemented`) {
				t.Fatalf("wrapper for %s fell back to generic Component.mount wrapper:\n%s", framework, source)
			}
		})
	}
}

func TestWriteFrameworkWrappersCreatesPhysicalWrapperOutsideClientFramework(t *testing.T) {
	projectRoot := t.TempDir()
	asset := Asset{
		Name:          "menu",
		Framework:     "solid",
		AbsPath:       filepath.Join(projectRoot, "client", "solid", "menu", "menu.jsx"),
		ProjectRoot:   projectRoot,
		FrameworkRoot: filepath.Join(projectRoot, "client", "solid"),
		Mode:          ModeDev,
	}
	assets, err := WriteFrameworkWrappers([]Asset{asset})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || strings.TrimSpace(assets[0].WrapperAbsPath) == "" {
		t.Fatalf("wrapper path not set: %#v", assets)
	}
	if !strings.Contains(assets[0].WrapperAbsPath, filepath.Join(".build", "client", "runtime", "wrappers", "solid", "dev")) {
		t.Fatalf("wrapper path should live under .build/client/runtime/wrappers, got %s", assets[0].WrapperAbsPath)
	}
	if strings.Contains(assets[0].WrapperAbsPath, filepath.Join("client", "solid", "node_modules")) {
		t.Fatalf("wrapper should not live inside client framework workspace: %s", assets[0].WrapperAbsPath)
	}
}

func TestFrameworkWrapperExtensionUsesJavaScriptForVueAndSvelteWrappers(t *testing.T) {
	projectRoot := t.TempDir()
	cases := []struct {
		framework string
		sourceExt string
		wantExt   string
	}{
		{framework: "react", sourceExt: ".jsx", wantExt: ".jsx"},
		{framework: "preact", sourceExt: ".jsx", wantExt: ".jsx"},
		{framework: "solid", sourceExt: ".jsx", wantExt: ".jsx"},
		{framework: "vue", sourceExt: ".vue", wantExt: ".js"},
		{framework: "svelte", sourceExt: ".svelte", wantExt: ".js"},
	}
	for _, tc := range cases {
		t.Run(tc.framework, func(t *testing.T) {
			asset := Asset{
				Name:          "menu",
				Framework:     tc.framework,
				AbsPath:       filepath.Join(projectRoot, "client", tc.framework, "menu", "menu"+tc.sourceExt),
				ProjectRoot:   projectRoot,
				FrameworkRoot: filepath.Join(projectRoot, "client", tc.framework),
				Mode:          ModeDev,
			}
			assets, err := WriteFrameworkWrappers([]Asset{asset})
			if err != nil {
				t.Fatal(err)
			}
			if got := filepath.Ext(assets[0].WrapperAbsPath); got != tc.wantExt {
				t.Fatalf("wrapper extension mismatch: got %s want %s path=%s", got, tc.wantExt, assets[0].WrapperAbsPath)
			}
		})
	}
}
