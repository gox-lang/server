package serverruntime

import (
	"os"
	"path/filepath"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestDeleteUnreferencedClientAssetFilesKeepsReferencedAndRemovesOld(t *testing.T) {
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "old.css")
	keptFile := filepath.Join(dir, "kept.css")
	for _, file := range []string{oldFile, keptFile, oldFile + ".l6.gzip"} {
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	policy := NewClientAssetPolicy(&goxcompiler.CompileResult{
		ClientBundles: []goxcompiler.CompileClientBundle{{
			EntryURL:  "/_gox/client/menu.js",
			EntryFile: filepath.Join(dir, "menu.jsx"),
			Assets: []goxcompiler.CompileClientAsset{
				{Kind: "css", URL: "/_gox/client/assets/kept.css", File: keptFile},
			},
		}},
	})

	deleteUnreferencedClientAssetFiles([]string{oldFile, keptFile}, policy)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatalf("old asset should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(oldFile + ".l6.gzip"); !os.IsNotExist(err) {
		t.Fatalf("old precompressed asset should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(keptFile); err != nil {
		t.Fatalf("referenced asset should be kept: %v", err)
	}
}

func TestDeleteUnreferencedClientAssetFilesKeepsSharedFrameworkVendorOutputs(t *testing.T) {
	root := t.TempDir()
	prod := filepath.Join(root, ".build", "client", "preact", "prod")
	vendor := filepath.Join(prod, "vendor", "preact.js")
	vendorChunk := filepath.Join(prod, "chunks", "vendor-runtime.js")
	componentChunk := filepath.Join(prod, "menu-12345678", "chunks", "component.js")
	for _, file := range []string{vendor, vendorChunk, componentChunk} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	deleteUnreferencedClientAssetFiles([]string{vendor, vendorChunk, componentChunk}, NewClientAssetPolicy(nil))

	for _, file := range []string{vendor, vendorChunk} {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("shared framework vendor output should be kept: %s: %v", file, err)
		}
	}
	if _, err := os.Stat(componentChunk); !os.IsNotExist(err) {
		t.Fatalf("unreferenced component chunk should be retired, stat err=%v", err)
	}
}

func TestIsSharedFrameworkVendorOutputOnlyMatchesStableProdVendorTree(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		file string
		want bool
	}{
		{filepath.Join(root, ".build", "client", "preact", "prod", "vendor", "preact.js"), true},
		{filepath.Join(root, ".build", "client", "svelte", "prod", "chunks", "runtime.js"), true},
		{filepath.Join(root, ".build", "client", "preact", "dev", "vendor", "preact.js"), false},
		{filepath.Join(root, ".build", "client", "preact", "prod", "menu-12345678", "chunks", "component.js"), false},
		{filepath.Join(root, "client", "preact", "prod", "vendor", "preact.js"), false},
	}
	for _, tc := range cases {
		if got := isSharedFrameworkVendorOutput(tc.file); got != tc.want {
			t.Fatalf("isSharedFrameworkVendorOutput(%q) = %v, want %v", tc.file, got, tc.want)
		}
	}
}
