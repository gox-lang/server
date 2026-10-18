package lsp

import (
	"go/token"
	"go/types"
	"path/filepath"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestPhase15_4C1GoPackageDependenciesUseSeparateReverseIndex(t *testing.T) {
	root := t.TempDir()
	pagePath := filepath.Join(root, "pages", "index.gox")
	packagePath := "example.com/project/model"
	store := newAnalysisStore(func(root string, document Document) (*goxcompiler.Module, error) {
		return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
			SourcePath:  document.Path,
			Kind:        goxcompiler.SemanticDependencyGoPackage,
			ImportPath:  packagePath,
			PackagePath: packagePath,
		}}}}, nil
	})

	document := Document{URI: FileURI(pagePath), Path: pagePath, Version: 1, Text: "module page\n"}
	snapshot := store.Snapshot(root, document)
	if snapshot == nil || len(snapshot.Dependencies) != 1 {
		t.Fatalf("snapshot Go dependencies = %#v", snapshot)
	}
	dependency := snapshot.Dependencies[0]
	if dependency.Kind != goxcompiler.SemanticDependencyGoPackage || dependency.PackagePath != packagePath {
		t.Fatalf("snapshot Go dependency = %#v", dependency)
	}

	store.mu.RLock()
	_, packagePathIndexedAsFilesystemDependency := store.reverseDependencies[analysisDependencyPath(packagePath)]
	_, sourcePathIndexed := store.reverseDependencies[analysisDependencyPath(pagePath)]
	store.mu.RUnlock()
	if !sourcePathIndexed {
		t.Fatal("snapshot source path was not reverse-indexed")
	}
	if packagePathIndexedAsFilesystemDependency {
		t.Fatalf("Go import path %q unexpectedly entered the filesystem reverse-dependency index", packagePath)
	}
	if got := store.goPackageDependentURIs(root, []string{packagePath}); len(got) != 1 || got[0] != document.URI {
		t.Fatalf("Go package reverse index = %v, want [%q]", got, document.URI)
	}

	if invalidated := store.InvalidateDependencyPath(packagePath); len(invalidated) != 0 {
		t.Fatalf("filesystem dependency invalidation unexpectedly matched Go import path %q: %v", packagePath, invalidated)
	}
	store.mu.RLock()
	_, stillCurrent := store.current[document.URI]
	store.mu.RUnlock()
	if !stillCurrent {
		t.Fatal("Go import-path lookup unexpectedly changed production invalidation behavior")
	}

	t.Log("audit update: Go PackagePath/ImportPath now have a dedicated root-scoped reverse index while filesystem dependency invalidation remains a separate namespace")
}

func TestPhase15_4C1GoPackageInvalidationPreservesUnrelatedSnapshotsAcrossImporterRotation(t *testing.T) {
	root := t.TempDir()
	packagesByURI := map[string]string{}
	store := newAnalysisStore(func(root string, document Document) (*goxcompiler.Module, error) {
		packagePath := packagesByURI[document.URI]
		return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
			SourcePath:  document.Path,
			Kind:        goxcompiler.SemanticDependencyGoPackage,
			ImportPath:  packagePath,
			PackagePath: packagePath,
		}}}}, nil
	})
	store.semanticCache = goxcompiler.NewSemanticWorkspaceCache()

	modelPath := filepath.Join(root, "pages", "model.gox")
	otherPath := filepath.Join(root, "pages", "other.gox")
	modelDocument := Document{URI: FileURI(modelPath), Path: modelPath, Version: 1, Text: "module modelpage\n"}
	otherDocument := Document{URI: FileURI(otherPath), Path: otherPath, Version: 1, Text: "module otherpage\n"}
	modelPackage := "example.com/project/model"
	otherPackage := "example.com/project/other"
	packagesByURI[modelDocument.URI] = modelPackage
	packagesByURI[otherDocument.URI] = otherPackage
	store.Snapshot(root, modelDocument)
	store.Snapshot(root, otherDocument)

	invalidated := store.InvalidateGoPackages(root, []string{modelPackage})
	if len(invalidated) != 1 || invalidated[0] != modelDocument.URI {
		t.Fatalf("Go package invalidation = %v, want only %q", invalidated, modelDocument.URI)
	}
	if _, ok := store.Current(modelDocument.URI); ok {
		t.Fatal("snapshot depending on changed Go package survived invalidation")
	}
	if _, ok := store.Current(otherDocument.URI); !ok {
		t.Fatal("snapshot with disjoint Go package dependency was invalidated")
	}

	t.Log("audit update: Go package changes now invalidate only snapshots reached through the root-scoped package reverse index; unrelated immutable snapshots can survive the root-wide importer rotation")
}

func TestPhase15_4C1CrossSnapshotNavigationIdentityDoesNotRequirePackagePointerEquality(t *testing.T) {
	firstPackage := types.NewPackage("example.com/project/model", "model")
	secondPackage := types.NewPackage("example.com/project/model", "model")
	if firstPackage == secondPackage {
		t.Fatal("fixture unexpectedly reused package pointers")
	}
	firstObject := types.NewFunc(token.NoPos, firstPackage, "Lookup", types.NewSignatureType(nil, nil, nil, nil, nil, false))
	secondObject := types.NewFunc(token.NoPos, secondPackage, "Lookup", types.NewSignatureType(nil, nil, nil, nil, nil, false))
	firstPackage.Scope().Insert(firstObject)
	secondPackage.Scope().Insert(secondObject)

	firstModule := &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Info: &types.Info{}}}
	secondModule := &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Info: &types.Info{}}}
	identity, ok := semanticIdentityForObject(firstModule, firstObject)
	if !ok {
		t.Fatal("first semantic identity was not created")
	}
	if !semanticIdentityMatches(secondModule, secondObject, identity) {
		t.Fatalf("cross-snapshot semantic identity depended on package pointer: first package=%p second package=%p identity=%#v", firstPackage, secondPackage, identity)
	}

	t.Log("audit: cross-document semantic navigation compares stable package path/name/container identity rather than *types.Package pointer identity, so immutable unaffected snapshots can remain valid across a semantic importer generation rotation")
}
