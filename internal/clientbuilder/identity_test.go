package clientbuilder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildIdentityReusesEquivalentSources(t *testing.T) {
	want := BuildIdentity("react", "menu/menu.jsx", "prod", false)
	for _, path := range []string{
		"client/react/menu/menu.jsx",
		"client/React/menu/menu.jsx",
		"menu/menu.jsx",
	} {
		if got := BuildIdentity("React", path, "", false); got != want {
			t.Fatalf("BuildIdentity(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestBuildIdentityDistinguishesBuildInputs(t *testing.T) {
	base := BuildIdentity("react", "menu/menu.jsx", "", false)
	for _, changed := range []string{
		BuildIdentity("react", "other/menu.jsx", "", false),
		BuildIdentity("preact", "menu/menu.jsx", "", false),
		BuildIdentity("react", "menu/menu.jsx", "", true),
	} {
		if changed == base {
			t.Fatalf("different build inputs reused identity %q", base)
		}
	}
}

func TestAssetIdentityDrivesPublicAndCacheKeys(t *testing.T) {
	first := Asset{Name: "Menu", Framework: "react", Path: "client/react/menu/menu.jsx", Mode: "prod"}
	second := Asset{Name: "OtherName", Framework: "React", Path: "menu/menu.jsx", Dev: false}
	if first.BuildIdentity() != second.BuildIdentity() {
		t.Fatalf("equivalent assets have different build identities")
	}
	if first.PublicEntryURL() != second.PublicEntryURL() {
		t.Fatalf("equivalent assets have different public URLs")
	}
	if clientCacheKey(first) != clientCacheKey(second) {
		t.Fatalf("equivalent assets have different cache keys")
	}
}

func TestClientCacheReusesMatchingBuildIdentity(t *testing.T) {
	root := t.TempDir()
	frameworkRoot := filepath.Join(root, "client", "react")
	outputDir := filepath.Join(root, ".build", "client", "react", "prod")
	source := filepath.Join(frameworkRoot, "menu", "menu.jsx")
	config := filepath.Join(outputDir, "vite.config.mjs")
	entryFile := filepath.Join(outputDir, "menu.js")
	for _, file := range []string{source, config, entryFile} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(file), 0644); err != nil {
			t.Fatal(err)
		}
	}
	asset := Asset{
		Name: "Menu", Framework: "react", Entry: "menu/menu.jsx", Path: "client/react/menu/menu.jsx",
		AbsPath: source, Mode: "prod", OutputAbsDir: outputDir, ProjectRoot: root, FrameworkRoot: frameworkRoot,
	}
	built := BuiltAsset{
		Asset: asset, ConfigPath: config, EntryURL: asset.PublicEntryURL(), EntryFile: entryFile,
		Files: []BuiltClientFile{{Kind: string(BuiltAssetEntry), URL: asset.PublicEntryURL(), File: entryFile}},
	}
	StoreCachedViteAsset(built)
	cached, ok := TryLoadCachedViteAsset(asset)
	if !ok {
		t.Fatal("matching client build was not reused from cache")
	}
	if cached.EntryURL != asset.PublicEntryURL() {
		t.Fatalf("cached URL = %q, want %q", cached.EntryURL, asset.PublicEntryURL())
	}
}
