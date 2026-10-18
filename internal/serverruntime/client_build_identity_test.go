package serverruntime

import (
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestClientAssetPolicyMapsIndependentDeclarationsToSharedBundle(t *testing.T) {
	homeKey := goxcompiler.ClientDeclarationKey("pages/home.gox", "Menu", "react", "menu/menu.jsx", false)
	dashboardKey := goxcompiler.ClientDeclarationKey("pages/dashboard.gox", "Menu", "react", "menu/menu.jsx", false)
	stableURL := "/_gox/client/menu-74e34e15/menu-74e34e15.js"
	actualURL := "/_gox/client/menu-built.js"

	policy := NewClientAssetPolicy(&goxcompiler.CompileResult{
		ClientComponents: []goxcompiler.CompileClientComponent{
			{Name: "Menu", Key: homeKey, Framework: "react", Entry: "menu/menu.jsx", Path: "client/react/menu/menu.jsx", ModulePath: "pages/home.gox", Mode: "prod"},
			{Name: "Menu", Key: dashboardKey, Framework: "react", Entry: "menu/menu.jsx", Path: "client/react/menu/menu.jsx", ModulePath: "pages/dashboard.gox", Mode: "prod"},
		},
		ClientAssetMap: map[string]string{actualURL: "/tmp/menu-built.js"},
		ClientBundles: []goxcompiler.CompileClientBundle{
			{
				Name: "Menu", Framework: "react", Entry: "menu/menu.jsx", Path: "client/react/menu/menu.jsx", Mode: "prod",
				EntryURL: actualURL, EntryFile: "/project/client/react/menu/menu.jsx",
				Assets: []goxcompiler.CompileClientAsset{{Kind: "entry", URL: actualURL, File: "/tmp/menu-built.js"}},
				Graph:  goxcompiler.CompileClientDependencyGraph{EntryFile: "/project/client/react/menu/menu.jsx"},
			},
		},
	})

	if homeKey == dashboardKey {
		t.Fatal("independent declarations share a runtime identity")
	}
	for _, key := range []string{homeKey, dashboardKey} {
		snapshot, ok := policy.ResolveClientIsland(key)
		if !ok {
			t.Fatalf("declaration %q was not registered", key)
		}
		if snapshot.ScriptURL != actualURL {
			t.Fatalf("declaration %q resolved %q, want %q (stable %q)", key, snapshot.ScriptURL, actualURL, stableURL)
		}
	}
}
