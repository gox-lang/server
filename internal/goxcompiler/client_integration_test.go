package gox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClientPipelineKeepsDeclarationsAndReusesEquivalentBuild(t *testing.T) {
	root := t.TempDir()
	writeIntegrationFile(t, filepath.Join(root, "go.mod"), "module example.com/client-integration\n\ngo 1.22\n")
	writeIntegrationFile(t, filepath.Join(root, "client", "react", "menu", "menu.jsx"), "export default function Menu() { return null }\n")

	entry := filepath.Join(root, "pages", "index.gox")
	dashboard := filepath.Join(root, "pages", "dashboard.gox")
	writeIntegrationFile(t, entry, `module main

import "dashboard.gox"

client Menu react {
	src: "menu/menu.jsx"
}
`)
	writeIntegrationFile(t, dashboard, `module dashboard

client Menu react {
	src: "menu/menu.jsx"
}
`)

	graph, err := LoadGraph(entry)
	if err != nil {
		t.Fatal(err)
	}
	components := buildCompileClientComponents(graph)
	if len(components) != 2 {
		t.Fatalf("client declarations = %d, want 2", len(components))
	}
	if components[0].Key == components[1].Key {
		t.Fatalf("independent module declarations share key %q", components[0].Key)
	}
	if components[0].ModulePath == components[1].ModulePath {
		t.Fatalf("independent declarations report the same module %q", components[0].ModulePath)
	}

	assets := compileClientBuilderAssets(graph.RootDir, components)
	if len(assets) != 1 {
		t.Fatalf("equivalent client declarations produced %d builds, want 1", len(assets))
	}
	if assets[0].BuildIdentity() == "" {
		t.Fatal("equivalent client build has an empty identity")
	}
}

func writeIntegrationFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
