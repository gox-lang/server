package client

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/projectdefaults"
)

func TestNormalizeFrameworks(t *testing.T) {
	got, err := normalizeFrameworks([]string{" React ", "react", "VUE"}, "gox client add")
	if err != nil {
		t.Fatalf("normalizeFrameworks returned error: %v", err)
	}
	want := []string{"react", "vue"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frameworks mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestNormalizeFrameworksUsesNewCommand(t *testing.T) {
	_, err := normalizeFrameworks(nil, "gox client add")
	if err == nil || !strings.Contains(err.Error(), "usage: gox client add") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInstalledFrameworks(t *testing.T) {
	root := t.TempDir()
	for _, framework := range []string{"vue", "react"} {
		dir := filepath.Join(root, "client", framework)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "client", "unknown"), 0755); err != nil {
		t.Fatal(err)
	}

	got, err := installedFrameworks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"react", "vue"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frameworks mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestNormalizeFrameworkSpecs(t *testing.T) {
	got, err := normalizeFrameworkSpecs([]string{"React@19.2.0", "vue", "react@19.2.0"}, "gox client add")
	if err != nil {
		t.Fatal(err)
	}
	want := []frameworkSpec{{Framework: "react", Version: "19.2.0"}, {Framework: "vue"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("specs mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestNormalizeFrameworkSpecsRejectsInvalidVersionSyntax(t *testing.T) {
	for _, args := range [][]string{{"react@"}, {"react@19.2.0@next"}, {"react@19.2.0 next"}} {
		if _, err := normalizeFrameworkSpecs(args, "gox client add"); err == nil {
			t.Fatalf("normalizeFrameworkSpecs(%#v) succeeded, want error", args)
		}
	}
}

func TestNormalizeFrameworkSpecsRejectsConflictingVersions(t *testing.T) {
	if _, err := normalizeFrameworkSpecs([]string{"react@19.2.0", "react@19.1.0"}, "gox client add"); err == nil {
		t.Fatal("expected conflicting version error")
	}
}

func TestUpdateFrameworkVersionUpdatesOnlyFrameworkFamily(t *testing.T) {
	root := t.TempDir()
	manifest := projectdefaults.Manifest()
	if err := deps.WriteGoxJSON(root, manifest); err != nil {
		t.Fatal(err)
	}

	beforePlugin := manifest.Frameworks["react"]["@vitejs/plugin-react"]
	beforeTypes := manifest.Frameworks["react"]["@types/react"]
	if err := updateFrameworkVersions(root, []frameworkSpec{{Framework: "react", Version: "19.3.0"}}); err != nil {
		t.Fatal(err)
	}

	got, err := deps.ReadGoxJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Frameworks["react"]["react"] != "19.3.0" || got.Frameworks["react"]["react-dom"] != "19.3.0" {
		t.Fatalf("react family not updated: %#v", got.Frameworks["react"])
	}
	if got.Frameworks["react"]["@vitejs/plugin-react"] != beforePlugin || got.Frameworks["react"]["@types/react"] != beforeTypes {
		t.Fatalf("independent React tooling changed: %#v", got.Frameworks["react"])
	}
	if !reflect.DeepEqual(got.Toolchain, manifest.Toolchain) {
		t.Fatalf("toolchain changed: got %#v want %#v", got.Toolchain, manifest.Toolchain)
	}
}

func TestUpdateFrameworkVersionUpdatesMainPackageForEachFramework(t *testing.T) {
	tests := []struct {
		framework   string
		packageName string
		version     string
	}{
		{framework: "preact", packageName: "preact", version: "10.30.0"},
		{framework: "solid", packageName: "solid-js", version: "1.10.0"},
		{framework: "vue", packageName: "vue", version: "3.6.0"},
		{framework: "svelte", packageName: "svelte", version: "5.1.0"},
	}

	for _, tt := range tests {
		t.Run(tt.framework, func(t *testing.T) {
			root := t.TempDir()
			manifest := projectdefaults.Manifest()
			beforeFramework := make(map[string]string, len(manifest.Frameworks[tt.framework]))
			for name, version := range manifest.Frameworks[tt.framework] {
				beforeFramework[name] = version
			}
			if err := deps.WriteGoxJSON(root, manifest); err != nil {
				t.Fatal(err)
			}

			if err := updateFrameworkVersions(root, []frameworkSpec{{Framework: tt.framework, Version: tt.version}}); err != nil {
				t.Fatal(err)
			}

			got, err := deps.ReadGoxJSON(root)
			if err != nil {
				t.Fatal(err)
			}
			if got.Frameworks[tt.framework][tt.packageName] != tt.version {
				t.Fatalf("%s %s = %q, want %q", tt.framework, tt.packageName, got.Frameworks[tt.framework][tt.packageName], tt.version)
			}
			for name, version := range beforeFramework {
				if name == tt.packageName {
					continue
				}
				if got.Frameworks[tt.framework][name] != version {
					t.Fatalf("%s independent package %s changed: got %q, want %q", tt.framework, name, got.Frameworks[tt.framework][name], version)
				}
			}
			if !reflect.DeepEqual(got.Toolchain, manifest.Toolchain) {
				t.Fatalf("toolchain changed: got %#v want %#v", got.Toolchain, manifest.Toolchain)
			}
		})
	}
}

func TestUpdateFrameworkVersionsWithoutExplicitVersionDoesNotWriteManifest(t *testing.T) {
	root := t.TempDir()
	manifest := projectdefaults.Manifest()
	if err := deps.WriteGoxJSON(root, manifest); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, deps.GoxJSONFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := updateFrameworkVersions(root, []frameworkSpec{{Framework: "react"}, {Framework: "vue"}}); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("gox.json changed without an explicit framework version\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestAddFrameworkToEmptyManifest(t *testing.T) {
	root := t.TempDir()
	manifest := projectdefaults.Manifest()
	manifest.Frameworks = deps.Frameworks{}
	if err := deps.WriteGoxJSON(root, manifest); err != nil {
		t.Fatal(err)
	}
	if err := updateFrameworkVersions(root, []frameworkSpec{{Framework: "vue"}}); err != nil {
		t.Fatal(err)
	}
	got, err := deps.ReadGoxJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Frameworks) != 1 || got.Frameworks["vue"]["vue"] == "" {
		t.Fatalf("unexpected frameworks: %#v", got.Frameworks)
	}
	if got.Toolchain != manifest.Toolchain {
		t.Fatal("toolchain changed")
	}
}
