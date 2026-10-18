package gox

import (
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/clientbuilder"
)

func TestCompileClientBuilderAssetsReusesEquivalentBuilds(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "client", "react", "menu", "menu.jsx")
	clients := []CompileClientComponent{
		{Name: "Menu", Framework: "react", Entry: "menu/menu.jsx", Path: "client/react/menu/menu.jsx", AbsPath: source, ModulePath: "pages/home.gox", Mode: "prod"},
		{Name: "Navigation", Framework: "react", Entry: "menu/menu.jsx", Path: "client/react/menu/menu.jsx", AbsPath: source, ModulePath: "pages/dashboard.gox", Mode: "prod"},
	}
	assets := compileClientBuilderAssets(root, clients)
	if len(assets) != 1 {
		t.Fatalf("equivalent declarations produced %d build assets, want 1", len(assets))
	}
}

func TestCompileClientBuilderAssetsKeepsDistinctBuilds(t *testing.T) {
	root := t.TempDir()
	makeClient := func(name, framework, path, mode string, dev bool) CompileClientComponent {
		return CompileClientComponent{
			Name: name, Framework: framework, Entry: path, Path: "client/" + framework + "/" + path,
			AbsPath: filepath.Join(root, "client", framework, filepath.FromSlash(path)), Mode: mode, Dev: dev,
		}
	}
	clients := []CompileClientComponent{
		makeClient("Menu", "react", "menu/menu.jsx", "prod", false),
		makeClient("Menu", "react", "other/menu.jsx", "prod", false),
		makeClient("Menu", "preact", "menu/menu.jsx", "prod", false),
		makeClient("Menu", "react", "menu/menu.jsx", "dev", true),
	}
	assets := compileClientBuilderAssets(root, clients)
	if len(assets) != len(clients) {
		t.Fatalf("distinct declarations produced %d build assets, want %d", len(assets), len(clients))
	}
	seen := map[string]struct{}{}
	for _, asset := range assets {
		key := asset.BuildIdentity()
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate build identity %q", key)
		}
		seen[key] = struct{}{}
	}
	if assets[0].PublicEntryURL() == assets[1].PublicEntryURL() {
		t.Fatalf("different sources collided on public bundle URL")
	}
	if clientbuilder.PublicComponentDirName("Menu", "react", clients[0].Path, "prod") ==
		clientbuilder.PublicComponentDirName("Menu", "react", clients[1].Path, "prod") {
		t.Fatalf("different sources collided on workspace component directory")
	}
}
