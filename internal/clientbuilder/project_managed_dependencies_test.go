package clientbuilder

import (
	"testing"

	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/projectdefaults"
)

func TestProjectManagedDependenciesUseProjectManifestForAllFrameworks(t *testing.T) {
	root := t.TempDir()
	manifest := projectdefaults.Manifest()
	manifest.Toolchain.Vite = "8.9.1-project"
	manifest.Toolchain.TypeScript = "6.4.2-project"
	for framework, packages := range manifest.Frameworks {
		for name := range packages {
			packages[name] = "project-" + framework + "-" + name
		}
	}
	if err := deps.WriteGoxJSON(root, manifest); err != nil {
		t.Fatal(err)
	}

	for _, framework := range []string{"react", "preact", "solid", "vue", "svelte"} {
		dependencies, devDependencies, err := ProjectManagedDependencies(root, framework)
		if err != nil {
			t.Fatalf("%s: %v", framework, err)
		}
		for name, want := range manifest.Frameworks[framework] {
			got, ok := dependencies[name]
			if !ok {
				got, ok = devDependencies[name]
			}
			if !ok || got != want {
				t.Fatalf("%s %s = %q, want %q", framework, name, got, want)
			}
		}
		if devDependencies["vite"] != manifest.Toolchain.Vite {
			t.Fatalf("%s vite = %q", framework, devDependencies["vite"])
		}
		if devDependencies["typescript"] != manifest.Toolchain.TypeScript {
			t.Fatalf("%s typescript = %q", framework, devDependencies["typescript"])
		}
	}
}

func TestFrameworkWorkspacesKeepProjectVersionsIndependent(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()

	manifestA := projectdefaults.Manifest()
	manifestB := projectdefaults.Manifest()
	manifestA.Frameworks["react"]["react"] = "18.3.1-a"
	manifestA.Toolchain.Vite = "5.4.0-a"
	manifestB.Frameworks["react"]["react"] = "19.2.0-b"
	manifestB.Toolchain.Vite = "8.1.0-b"
	if err := deps.WriteGoxJSON(a, manifestA); err != nil {
		t.Fatal(err)
	}
	if err := deps.WriteGoxJSON(b, manifestB); err != nil {
		t.Fatal(err)
	}

	depsA, devA, err := ProjectManagedDependencies(a, "react")
	if err != nil {
		t.Fatal(err)
	}
	depsB, devB, err := ProjectManagedDependencies(b, "react")
	if err != nil {
		t.Fatal(err)
	}
	if depsA["react"] == depsB["react"] || devA["vite"] == devB["vite"] {
		t.Fatalf("project versions crossed: A=%#v/%#v B=%#v/%#v", depsA, devA, depsB, devB)
	}
}

func TestProjectManagedDependenciesRejectMissingManifestVersions(t *testing.T) {
	root := t.TempDir()
	manifest := projectdefaults.Manifest()
	manifest.Toolchain.Vite = ""
	if err := deps.WriteGoxJSON(root, manifest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ProjectManagedDependencies(root, "react"); err == nil {
		t.Fatal("expected missing Vite version error")
	}

	manifest = projectdefaults.Manifest()
	delete(manifest.Frameworks["react"], "react")
	if err := deps.WriteGoxJSON(root, manifest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ProjectManagedDependencies(root, "react"); err == nil {
		t.Fatal("expected missing React version error")
	}
}
