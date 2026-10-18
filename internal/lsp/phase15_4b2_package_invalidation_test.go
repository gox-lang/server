package lsp

import (
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestPhase15_4B2LocalGoChangePreservesGlobalIndexesAndUnrelatedPackageCaches(t *testing.T) {
	root := phase15_4AProjectRoot(t, "project")
	phase15_4B2WriteGoFile(t, filepath.Join(root, "model", "user.go"), "package model\ntype User struct{ Name string }\n")
	phase15_4B2WriteGoFile(t, filepath.Join(root, "service", "service.go"), "package service\nimport \"example.com/project/model\"\nfunc Current() model.User { return model.User{} }\n")
	phase15_4B2WriteGoFile(t, filepath.Join(root, "other", "other.go"), "package other\nfunc Value() string { return \"ok\" }\n")

	modelPath := "example.com/project/model"
	servicePath := "example.com/project/service"
	otherPath := "example.com/project/other"
	stdlibPath := "strings"
	server := NewServer()
	server.analysis = newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
			SourcePath:  document.Path,
			Kind:        goxcompiler.SemanticDependencyGoPackage,
			ImportPath:  modelPath,
			PackagePath: modelPath,
		}}}}, nil
	})
	server.analysis.semanticCache = goxcompiler.NewSemanticWorkspaceCache()
	document := phase15_4ASnapshot(t, server, root, "page.gox")
	phase15_4B2SeedPackageIndex(server.goPackages, root, modelPath, servicePath, otherPath, stdlibPath)

	semanticPackage := types.NewPackage(stdlibPath, "strings")
	server.analysis.semanticCache.StoreGoPackage(root, stdlibPath, semanticPackage)

	if !server.handleWorkspaceFileChange(FileURI(filepath.Join(root, "model", "user.go")), fileChanged) {
		t.Fatal("production .go change was not classified as Go-package-affecting")
	}

	if _, ok := server.analysis.Current(document.URI); ok {
		t.Fatal("snapshot depending on affected model package survived granular semantic invalidation")
	}
	if _, ok := server.analysis.semanticCache.GoPackage(root, stdlibPath); ok {
		t.Fatal("semantic importer universe survived production .go invalidation")
	}

	root = filepath.Clean(root)
	if _, ok := server.goPackages.discovery[root]; !ok {
		t.Fatal("local .go change discarded the combined discovery cache instead of patching its package entry")
	}
	if _, ok := server.goPackages.exports[root]; !ok {
		t.Fatal("local .go change invalidated global shallow exports")
	}
	if _, ok := server.goPackages.exportSources[root]; !ok {
		t.Fatal("local .go change invalidated global export source manifest")
	}
	if _, ok := server.goPackages.exportDetails[root+"\x00"+stdlibPath]; !ok {
		t.Fatal("local .go change invalidated unrelated global export details")
	}
	for _, path := range []string{modelPath, servicePath} {
		key := root + "\x00" + path
		if _, ok := server.goPackages.cache[key]; ok {
			t.Fatalf("affected local package cache %q survived invalidation", path)
		}
		if _, ok := server.goPackages.packageNames[key]; ok {
			t.Fatalf("affected local package-name cache %q survived invalidation", path)
		}
	}
	for _, path := range []string{otherPath, stdlibPath} {
		key := root + "\x00" + path
		if _, ok := server.goPackages.cache[key]; !ok {
			t.Fatalf("unrelated package cache %q was invalidated", path)
		}
		if _, ok := server.goPackages.packageNames[key]; !ok {
			t.Fatalf("unrelated package-name cache %q was invalidated", path)
		}
	}
	if candidate, ok := phase15_4B2DiscoveryCandidate(server.goPackages.discovery[root], modelPath); !ok || candidate.Name != "model" {
		t.Fatalf("changed package discovery candidate = %#v/%v, want model/true", candidate, ok)
	}
}

func TestPhase15_4B2ReverseClosureTracksTransitiveLocalImporters(t *testing.T) {
	root := phase15_4AProjectRoot(t, "closure")
	phase15_4B2WriteGoFile(t, filepath.Join(root, "model", "model.go"), "package model\ntype User struct{}\n")
	phase15_4B2WriteGoFile(t, filepath.Join(root, "service", "service.go"), "package service\nimport \"example.com/closure/model\"\nvar _ model.User\n")
	phase15_4B2WriteGoFile(t, filepath.Join(root, "api", "api.go"), "package api\nimport \"example.com/closure/service\"\nvar _ = service.Value\n")
	phase15_4B2WriteGoFile(t, filepath.Join(root, "service", "value.go"), "package service\nfunc Value() string { return \"ok\" }\n")
	phase15_4B2WriteGoFile(t, filepath.Join(root, "other", "other.go"), "package other\nfunc Value() string { return \"ok\" }\n")

	index := NewGoPackageIndex()
	invalidation, ok := index.ProjectGoPackageInvalidationForFile(root, filepath.Join(root, "model", "model.go"))
	if !ok {
		t.Fatal("failed to derive local package invalidation")
	}
	want := []string{"example.com/closure/api", "example.com/closure/model", "example.com/closure/service"}
	if !reflect.DeepEqual(invalidation.Affected, want) {
		t.Fatalf("affected package closure = %#v, want %#v", invalidation.Affected, want)
	}
}

func TestPhase15_4B2CreateAndDeletePatchDiscoveryInPlace(t *testing.T) {
	root := phase15_4AProjectRoot(t, "inventory")
	server := phase15_4AServerWithSyntheticAnalysis()
	root = filepath.Clean(root)
	server.goPackages.discovery[root] = []GoPackageCandidate{{Path: "strings", Name: "strings"}}

	createdPath := filepath.Join(root, "fresh", "fresh.go")
	phase15_4B2WriteGoFile(t, createdPath, "package renamed\nfunc Value() string { return \"ok\" }\n")
	if !server.handleWorkspaceFileChange(FileURI(createdPath), fileCreated) {
		t.Fatal("created .go file was not classified as Go-package-affecting")
	}
	freshImport := "example.com/inventory/fresh"
	if candidate, ok := phase15_4B2DiscoveryCandidate(server.goPackages.discovery[root], freshImport); !ok || candidate.Name != "renamed" {
		t.Fatalf("created package candidate = %#v/%v, want renamed/true", candidate, ok)
	}
	if _, ok := server.goPackages.discovery[root]; !ok {
		t.Fatal("created package discarded discovery cache")
	}

	if err := os.Remove(createdPath); err != nil {
		t.Fatal(err)
	}
	if !server.handleWorkspaceFileChange(FileURI(createdPath), fileDeleted) {
		t.Fatal("deleted .go file was not classified as Go-package-affecting")
	}
	if candidate, ok := phase15_4B2DiscoveryCandidate(server.goPackages.discovery[root], freshImport); ok {
		t.Fatalf("deleted package remained in discovery: %#v", candidate)
	}
	if candidate, ok := phase15_4B2DiscoveryCandidate(server.goPackages.discovery[root], "strings"); !ok || candidate.Name != "strings" {
		t.Fatal("patching local discovery disturbed stdlib candidate")
	}
}

func phase15_4B2WriteGoFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func phase15_4B2SeedPackageIndex(index *GoPackageIndex, root string, paths ...string) {
	root = filepath.Clean(root)
	index.discovery[root] = make([]GoPackageCandidate, 0, len(paths))
	for _, path := range paths {
		name := filepath.Base(path)
		index.discovery[root] = append(index.discovery[root], GoPackageCandidate{Path: path, Name: name})
		key := root + "\x00" + path
		index.cache[key] = goPackageCacheEntry{packageInfo: GoPackage{Path: path, Name: name}}
		index.packageNames[key] = name
	}
	index.exports[root] = []GoPackage{{Path: "strings", Name: "strings"}}
	index.exportSources[root] = map[string]goGlobalPackageSource{
		"strings": {Name: "strings", Files: []string{"/goroot/src/strings/strings.go"}},
	}
	index.exportDetails[root+"\x00strings"] = GoPackage{Path: "strings", Name: "strings"}
}

func phase15_4B2DiscoveryCandidate(candidates []GoPackageCandidate, importPath string) (GoPackageCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.Path == importPath {
			return candidate, true
		}
	}
	return GoPackageCandidate{}, false
}
