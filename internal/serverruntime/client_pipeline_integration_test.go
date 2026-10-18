package serverruntime

import (
	"os"
	"path/filepath"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestClientPipelineResolvesIndependentDeclarationsToOneBundle(t *testing.T) {
	root := t.TempDir()
	writeClientPipelineFile(t, filepath.Join(root, "go.mod"), "module example.com/client-pipeline\n\ngo 1.22\n")
	source := filepath.Join(root, "client", "react", "menu", "menu.jsx")
	writeClientPipelineFile(t, source, "export default function Menu() { return null }\n")

	entry := filepath.Join(root, "pages", "index.gox")
	writeClientPipelineFile(t, entry, `module main

import "dashboard.gox"

client Menu react {
	src: "menu/menu.jsx"
}
`)
	writeClientPipelineFile(t, filepath.Join(root, "pages", "dashboard.gox"), `module dashboard

client Menu react {
	src: "menu/menu.jsx"
}
`)

	result, err := goxcompiler.Analyze(entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ClientComponents) != 2 {
		t.Fatalf("client declarations = %d, want 2", len(result.ClientComponents))
	}

	bundleURL := "/_gox/client/menu-integration/menu-integration.js"
	bundleFile := filepath.Join(root, ".build", "client", "react", "prod", "menu-integration.js")
	result.ClientAssetMap = map[string]string{bundleURL: bundleFile}
	result.ClientBundles = []goxcompiler.CompileClientBundle{{
		Name:      "Menu",
		Framework: "react",
		Entry:     "menu/menu.jsx",
		Path:      "client/react/menu/menu.jsx",
		Mode:      "prod",
		EntryURL:  bundleURL,
		EntryFile: source,
		Assets: []goxcompiler.CompileClientAsset{{
			Kind: "entry",
			URL:  bundleURL,
			File: bundleFile,
		}},
		Graph: goxcompiler.CompileClientDependencyGraph{EntryFile: source},
	}}

	policy := NewClientAssetPolicy(result)
	first := result.ClientComponents[0]
	second := result.ClientComponents[1]
	if first.Key == second.Key {
		t.Fatalf("independent declarations share key %q", first.Key)
	}
	for _, component := range result.ClientComponents {
		snapshot, ok := policy.ResolveClientIsland(component.Key)
		if !ok {
			t.Fatalf("declaration %q from %q was not registered", component.Key, component.ModulePath)
		}
		if snapshot.ScriptURL != bundleURL {
			t.Fatalf("declaration %q resolved %q, want %q", component.Key, snapshot.ScriptURL, bundleURL)
		}
	}
}

func writeClientPipelineFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
