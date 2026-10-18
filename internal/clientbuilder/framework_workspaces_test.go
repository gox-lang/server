package clientbuilder

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectFrameworkWorkspacesMissingClientDirectory(t *testing.T) {
	if got := DetectFrameworkWorkspaces(t.TempDir()); got != nil {
		t.Fatalf("DetectFrameworkWorkspaces() = %#v, want nil", got)
	}
}

func TestDetectFrameworkWorkspacesFiltersAndSortsManagedFrameworks(t *testing.T) {
	root := t.TempDir()
	clientRoot := filepath.Join(root, "client")

	for _, framework := range []string{"vue", "react", "solid", "unknown", "gox", "wasm"} {
		if err := os.MkdirAll(filepath.Join(clientRoot, framework), 0755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", framework, err)
		}
	}
	if err := os.WriteFile(filepath.Join(clientRoot, "preact"), []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	got := DetectFrameworkWorkspaces(root)
	want := []string{"react", "solid", "vue"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectFrameworkWorkspaces() = %#v, want %#v", got, want)
	}
}

func TestDetectFrameworkWorkspacesNormalizesDirectoryNames(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "client", "React"), 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}

	got := DetectFrameworkWorkspaces(root)
	want := []string{"react"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectFrameworkWorkspaces() = %#v, want %#v", got, want)
	}
}

func TestFrameworkUsesCentralSupportRegistry(t *testing.T) {
	for _, name := range []string{"react", "preact", "solid", "vue", "svelte"} {
		if _, ok := Framework(name); !ok {
			t.Fatalf("Framework(%q) must be supported", name)
		}
	}
	for _, name := range []string{"gox", "wasm", "unknown"} {
		if _, ok := Framework(name); ok {
			t.Fatalf("Framework(%q) must not be buildable", name)
		}
	}
}
