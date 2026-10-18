package lsp

import (
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestPhase15_4C2BInvalidateGoPackagesDropsOnlyDependentSnapshotsAndRotatesImporter(t *testing.T) {
	rootOne := t.TempDir()
	rootTwo := t.TempDir()
	modelPath := "example.com/project/model"
	servicePath := "example.com/project/service"
	otherPath := "example.com/project/other"

	packagesByURI := map[string]string{}
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		packagePath := packagesByURI[document.URI]
		module := &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{}}
		if packagePath != "" {
			module.Semantic.Dependencies = []goxcompiler.SemanticDependency{{
				SourcePath:  document.Path,
				Kind:        goxcompiler.SemanticDependencyGoPackage,
				ImportPath:  packagePath,
				PackagePath: packagePath,
			}}
		}
		return module, nil
	})
	store.semanticCache = goxcompiler.NewSemanticWorkspaceCache()

	modelDocument := phase15C2BDocument(rootOne, "model.gox")
	serviceDocument := phase15C2BDocument(rootOne, "service.gox")
	otherDocument := phase15C2BDocument(rootOne, "other.gox")
	otherRootDocument := phase15C2BDocument(rootTwo, "model.gox")
	packagesByURI[modelDocument.URI] = modelPath
	packagesByURI[serviceDocument.URI] = servicePath
	packagesByURI[otherDocument.URI] = otherPath
	packagesByURI[otherRootDocument.URI] = modelPath

	store.Snapshot(rootOne, modelDocument)
	store.Snapshot(rootOne, serviceDocument)
	store.Snapshot(rootOne, otherDocument)
	store.Snapshot(rootTwo, otherRootDocument)

	rootOnePackage := types.NewPackage(modelPath, "model")
	rootTwoPackage := types.NewPackage(modelPath, "model")
	store.semanticCache.StoreGoPackage(rootOne, modelPath, rootOnePackage)
	store.semanticCache.StoreGoPackage(rootTwo, modelPath, rootTwoPackage)

	invalidated := store.InvalidateGoPackages(rootOne, []string{modelPath, servicePath, modelPath})
	wantInvalidated := []string{modelDocument.URI, serviceDocument.URI}
	if !reflect.DeepEqual(invalidated, wantInvalidated) {
		t.Fatalf("InvalidateGoPackages invalidated %v, want %v", invalidated, wantInvalidated)
	}
	if _, ok := store.Current(modelDocument.URI); ok {
		t.Fatal("model-dependent snapshot survived Go package invalidation")
	}
	if _, ok := store.Current(serviceDocument.URI); ok {
		t.Fatal("service-dependent snapshot survived Go package invalidation")
	}
	if _, ok := store.Current(otherDocument.URI); !ok {
		t.Fatal("snapshot with disjoint Go package dependency was invalidated")
	}
	if _, ok := store.Current(otherRootDocument.URI); !ok {
		t.Fatal("snapshot in unrelated root was invalidated")
	}
	if _, ok := store.semanticCache.GoPackage(rootOne, modelPath); ok {
		t.Fatal("root-one semantic importer universe did not rotate")
	}
	if got, ok := store.semanticCache.GoPackage(rootTwo, modelPath); !ok || got != rootTwoPackage {
		t.Fatalf("unrelated root semantic identity changed: got=%p ok=%v want=%p", got, ok, rootTwoPackage)
	}
}

func TestPhase15_4C2BInvalidateGoPackagesDropsOnlyMatchingTransientAnalyses(t *testing.T) {
	root := t.TempDir()
	modelPath := "example.com/project/model"
	otherPath := "example.com/project/other"
	store := newAnalysisStore(nil)

	modelKey := transientAnalysisKey{URI: FileURI(filepath.Join(root, "pages", "model.gox")), Version: 1, Source: "model transient"}
	otherKey := transientAnalysisKey{URI: FileURI(filepath.Join(root, "pages", "other.gox")), Version: 1, Source: "other transient"}
	store.transient[modelKey] = &transientAnalysis{
		root:         root,
		document:     Document{URI: modelKey.URI, Path: filepath.Join(root, "pages", "model.gox"), Version: 1},
		dependencies: []goxcompiler.SemanticDependency{{Kind: goxcompiler.SemanticDependencyGoPackage, PackagePath: modelPath, ImportPath: modelPath}},
	}
	store.transient[otherKey] = &transientAnalysis{
		root:         root,
		document:     Document{URI: otherKey.URI, Path: filepath.Join(root, "pages", "other.gox"), Version: 1},
		dependencies: []goxcompiler.SemanticDependency{{Kind: goxcompiler.SemanticDependencyGoPackage, PackagePath: otherPath, ImportPath: otherPath}},
	}

	store.InvalidateGoPackages(root, []string{modelPath})
	if _, ok := store.transient[modelKey]; ok {
		t.Fatal("transient analysis depending on changed package survived invalidation")
	}
	if _, ok := store.transient[otherKey]; !ok {
		t.Fatal("unrelated transient analysis was invalidated")
	}
}

func TestPhase15_4C2BServerUsesB2bAffectedClosureForSnapshotInvalidation(t *testing.T) {
	root := phase15_4AProjectRoot(t, "closure")
	modelFile := phase15C2BWriteGoFile(t, root, "model/model.go", "package model\n")
	phase15C2BWriteGoFile(t, root, "service/service.go", "package service\nimport _ \"example.com/closure/model\"\n")
	phase15C2BWriteGoFile(t, root, "api/api.go", "package api\nimport _ \"example.com/closure/service\"\n")
	phase15C2BWriteGoFile(t, root, "other/other.go", "package other\n")

	packagesByURI := map[string]string{}
	server := NewServer()
	server.analysis = newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		packagePath := packagesByURI[document.URI]
		module := &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{}}
		if packagePath != "" {
			module.Semantic.Dependencies = []goxcompiler.SemanticDependency{{
				SourcePath:  document.Path,
				Kind:        goxcompiler.SemanticDependencyGoPackage,
				ImportPath:  packagePath,
				PackagePath: packagePath,
			}}
		}
		return module, nil
	})
	server.analysis.semanticCache = goxcompiler.NewSemanticWorkspaceCache()

	modelDocument := phase15C2BDocument(root, "model.gox")
	serviceDocument := phase15C2BDocument(root, "service.gox")
	apiDocument := phase15C2BDocument(root, "api.gox")
	otherDocument := phase15C2BDocument(root, "other.gox")
	packagesByURI[modelDocument.URI] = "example.com/closure/model"
	packagesByURI[serviceDocument.URI] = "example.com/closure/service"
	packagesByURI[apiDocument.URI] = "example.com/closure/api"
	packagesByURI[otherDocument.URI] = "example.com/closure/other"
	server.analysis.Snapshot(root, modelDocument)
	server.analysis.Snapshot(root, serviceDocument)
	server.analysis.Snapshot(root, apiDocument)
	server.analysis.Snapshot(root, otherDocument)

	semanticPackage := types.NewPackage("example.com/closure/model", "model")
	server.analysis.semanticCache.StoreGoPackage(root, "example.com/closure/model", semanticPackage)

	if !server.handleWorkspaceFileChange(FileURI(modelFile), fileChanged) {
		t.Fatal("production .go change was not classified as Go-package-affecting")
	}
	for _, document := range []Document{modelDocument, serviceDocument, apiDocument} {
		if _, ok := server.analysis.Current(document.URI); ok {
			t.Fatalf("snapshot %s survived transitive Go package invalidation", document.URI)
		}
	}
	if _, ok := server.analysis.Current(otherDocument.URI); !ok {
		t.Fatal("snapshot outside B2b affected closure was invalidated")
	}
	if _, ok := server.analysis.semanticCache.GoPackage(root, "example.com/closure/model"); ok {
		t.Fatal("server did not rotate semantic importer root after granular snapshot invalidation")
	}
}

func TestPhase15_4C2BPlannerFailureFallsBackToRootInvalidation(t *testing.T) {
	root := phase15_4AProjectRoot(t, "fallback")
	changed := phase15C2BWriteGoFile(t, root, "broken/a.go", "package first\n")
	phase15C2BWriteGoFile(t, root, "broken/b.go", "package second\n")

	server := phase15_4AServerWithSyntheticAnalysis()
	first := phase15_4ASnapshot(t, server, root, "first.gox")
	second := phase15_4ASnapshot(t, server, root, "second.gox")

	if !server.handleWorkspaceFileChange(FileURI(changed), fileChanged) {
		t.Fatal("conflicting Go package change was not classified as Go-package-affecting")
	}
	if _, ok := server.analysis.Current(first.URI); ok {
		t.Fatal("planner failure did not fall back to root invalidation for first snapshot")
	}
	if _, ok := server.analysis.Current(second.URI); ok {
		t.Fatal("planner failure did not fall back to root invalidation for second snapshot")
	}
}

func phase15C2BDocument(root, name string) Document {
	path := filepath.Join(root, "pages", name)
	return Document{URI: FileURI(path), Path: path, Version: 1, Text: "module page\n"}
}

func phase15C2BWriteGoFile(t *testing.T, root, relative, source string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
