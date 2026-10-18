package serverruntime

import (
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"

	"github.com/gox-lang/gox"
)

func TestClientAssetMetaCacheControl(t *testing.T) {
	cases := []struct {
		name string
		meta ClientAssetFileMeta
		want string
	}{
		{name: "prod entry", meta: ClientAssetFileMeta{Kind: "entry", Mode: "prod"}, want: "public, max-age=31536000, immutable"},
		{name: "dev entry", meta: ClientAssetFileMeta{Kind: "entry", Mode: "dev"}, want: "no-cache"},
		{name: "vendor", meta: ClientAssetFileMeta{Kind: "vendor", Mode: "dev"}, want: "public, max-age=31536000, immutable"},
		{name: "runtime", meta: ClientAssetFileMeta{Kind: "runtime"}, want: "public, max-age=31536000, immutable"},
		{name: "sourcemap", meta: ClientAssetFileMeta{Kind: "sourcemap", Mode: "prod"}, want: "no-cache"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.meta.CacheControl(); got != tc.want {
				t.Fatalf("CacheControl() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientStaticCacheControlUsesRoutePattern(t *testing.T) {
	policy := NewClientAssetPolicy(&goxcompiler.CompileResult{
		ClientAssetMap: map[string]string{"/_gox/client/menu.js": "/tmp/menu.js"},
		ClientBundles: []goxcompiler.CompileClientBundle{{
			Name:      "menu",
			Framework: "react",
			Mode:      "dev",
			EntryURL:  "/_gox/client/menu.js",
			EntryFile: "/tmp/menu-entry.jsx",
			Assets:    []goxcompiler.CompileClientAsset{{Kind: "entry", URL: "/_gox/client/menu.js", File: "/tmp/menu.js"}},
		}},
	})
	route := gox.NewRoute(gox.RouteInput{IsClientAsset: true, Pattern: "/_gox/client/menu.js", Target: "/tmp/menu.js"})

	if got := clientStaticCacheControl(policy, route); got != "no-cache" {
		t.Fatalf("cache-control = %q, want no-cache", got)
	}
}

func TestClientAssetPolicyReplaceBundleRemovesOldAssets(t *testing.T) {
	policy := NewClientAssetPolicy(&goxcompiler.CompileResult{
		ClientAssetMap: map[string]string{
			"/_gox/client/menu-old.js":            "/tmp/menu-old.js",
			"/_gox/client/vendor/vendor-react.js": "/tmp/vendor-react.js",
		},
		ClientBundles: []goxcompiler.CompileClientBundle{{
			Name:      "menu",
			Framework: "react",
			Mode:      "prod",
			EntryURL:  "/_gox/client/menu-old.js",
			EntryFile: "/tmp/menu-entry.jsx",
			Assets: []goxcompiler.CompileClientAsset{
				{Kind: "entry", URL: "/_gox/client/menu-old.js", File: "/tmp/menu-old.js"},
				{Kind: "vendor", URL: "/_gox/client/vendor/vendor-react.js", File: "/tmp/vendor-react.js"},
			},
		}},
	})

	policy.ReplaceBundle(goxcompiler.CompileClientBundle{
		Name:      "menu",
		Framework: "react",
		Mode:      "prod",
		EntryURL:  "/_gox/client/menu-new.js",
		EntryFile: "/tmp/menu-entry.jsx",
		Assets:    []goxcompiler.CompileClientAsset{{Kind: "entry", URL: "/_gox/client/menu-new.js", File: "/tmp/menu-new.js"}},
	})

	if _, ok := policy.Resolve("/_gox/client/menu-old.js"); ok {
		t.Fatal("old entry should be removed after ReplaceBundle")
	}
	if _, ok := policy.Resolve("/_gox/client/vendor/vendor-react.js"); ok {
		t.Fatal("old vendor should be removed after ReplaceBundle")
	}
	if got, ok := policy.Resolve("/_gox/client/menu-new.js"); !ok || got != "/tmp/menu-new.js" {
		t.Fatalf("new entry resolve = %q, %v", got, ok)
	}
}

func TestClientAssetPolicyFilesForEntryAndReferencedFiles(t *testing.T) {
	policy := NewClientAssetPolicy(&goxcompiler.CompileResult{
		ClientBundles: []goxcompiler.CompileClientBundle{{
			Name:      "menu",
			Framework: "react",
			Mode:      "dev",
			EntryURL:  "/_gox/client/menu.js",
			EntryFile: "/tmp/menu-entry.jsx",
			Assets: []goxcompiler.CompileClientAsset{
				{Kind: "entry", URL: "/_gox/client/menu.js", File: "/tmp/menu.js"},
				{Kind: "css", URL: "/_gox/client/assets/menu.css", File: "/tmp/assets/menu.css"},
			},
		}},
	})

	files := policy.FilesForEntry("/tmp/menu-entry.jsx")
	if len(files) != 2 {
		t.Fatalf("FilesForEntry len = %d, want 2: %#v", len(files), files)
	}
	refs := policy.ReferencedFiles()
	if _, ok := refs["/tmp/menu.js"]; !ok {
		t.Fatalf("ReferencedFiles missing entry: %#v", refs)
	}
	if _, ok := refs["/tmp/assets/menu.css"]; !ok {
		t.Fatalf("ReferencedFiles missing css: %#v", refs)
	}
}
