package lsp

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestPhase15_4C2AGoPackageReverseIndexIsRootScopedAndDeduplicated(t *testing.T) {
	rootOne := t.TempDir()
	rootTwo := t.TempDir()
	modelPath := "example.com/project/model"
	servicePath := "example.com/project/service"
	aliasPath := "example.com/project/model-alias"

	modules := map[string]*goxcompiler.Module{}
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		return modules[document.URI], nil
	})

	modelDocument := phase15C2ADocument(rootOne, "model.gox", 1)
	serviceDocument := phase15C2ADocument(rootOne, "service.gox", 1)
	aliasDocument := phase15C2ADocument(rootOne, "alias.gox", 1)
	otherRootDocument := phase15C2ADocument(rootTwo, "model.gox", 1)

	modules[modelDocument.URI] = phase15C2AModule(modelDocument.Path, phase15C2AGoDependency(modelPath, modelPath))
	modules[serviceDocument.URI] = phase15C2AModule(serviceDocument.Path, phase15C2AGoDependency(servicePath, servicePath))
	modules[aliasDocument.URI] = phase15C2AModule(aliasDocument.Path,
		phase15C2AGoDependency(aliasPath, modelPath),
		phase15C2AGoDependency(modelPath, modelPath),
		goxcompiler.SemanticDependency{Kind: goxcompiler.SemanticDependencyGoxModule, ImportPath: "example.com/project/not-a-go-package", PackagePath: modelPath},
	)
	modules[otherRootDocument.URI] = phase15C2AModule(otherRootDocument.Path, phase15C2AGoDependency(modelPath, modelPath))

	store.Snapshot(rootOne, modelDocument)
	store.Snapshot(rootOne, serviceDocument)
	store.Snapshot(rootOne, aliasDocument)
	store.Snapshot(rootTwo, otherRootDocument)

	if got, want := store.goPackageDependentURIs(rootOne, []string{modelPath}), []string{aliasDocument.URI, modelDocument.URI}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root-one model dependents = %v, want %v", got, want)
	}
	if got, want := store.goPackageDependentURIs(rootOne, []string{aliasPath}), []string{aliasDocument.URI}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ImportPath alias dependents = %v, want %v", got, want)
	}
	if got, want := store.goPackageDependentURIs(rootOne, []string{modelPath, servicePath, modelPath}), []string{aliasDocument.URI, modelDocument.URI, serviceDocument.URI}; !reflect.DeepEqual(got, want) {
		t.Fatalf("combined dependents = %v, want %v", got, want)
	}
	if got, want := store.goPackageDependentURIs(rootTwo, []string{modelPath}), []string{otherRootDocument.URI}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root-two model dependents = %v, want %v", got, want)
	}
	if got := store.goPackageDependentURIs(rootOne, []string{"example.com/project/not-a-go-package"}); len(got) != 0 {
		t.Fatalf("GOX-module dependency leaked into Go-package reverse index: %v", got)
	}
}

func TestPhase15_4C2AGoPackageReverseIndexTracksSnapshotReplacementAndClose(t *testing.T) {
	root := t.TempDir()
	modelPath := "example.com/project/model"
	servicePath := "example.com/project/service"
	document := phase15C2ADocument(root, "index.gox", 1)

	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		packagePath := modelPath
		if document.Version == 2 {
			packagePath = servicePath
		}
		return phase15C2AModule(document.Path, phase15C2AGoDependency(packagePath, packagePath)), nil
	})

	store.Snapshot(root, document)
	if got := store.goPackageDependentURIs(root, []string{modelPath}); !reflect.DeepEqual(got, []string{document.URI}) {
		t.Fatalf("version-one model dependents = %v", got)
	}

	document.Version = 2
	document.Text = "module page // v2\n"
	store.Snapshot(root, document)
	if got := store.goPackageDependentURIs(root, []string{modelPath}); len(got) != 0 {
		t.Fatalf("replaced snapshot left stale model edge: %v", got)
	}
	if got := store.goPackageDependentURIs(root, []string{servicePath}); !reflect.DeepEqual(got, []string{document.URI}) {
		t.Fatalf("version-two service dependents = %v", got)
	}

	store.Close(document.URI)
	if got := store.goPackageDependentURIs(root, []string{servicePath}); len(got) != 0 {
		t.Fatalf("closed snapshot left stale service edge: %v", got)
	}
}

func TestPhase15_4C2AGoPackageReverseIndexPreservesLastGoodDependencies(t *testing.T) {
	root := t.TempDir()
	modelPath := "example.com/project/model"
	document := phase15C2ADocument(root, "index.gox", 1)
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		if document.Version == 2 {
			return nil, errors.New("broken source")
		}
		return phase15C2AModule(document.Path, phase15C2AGoDependency(modelPath, modelPath)), nil
	})

	store.Snapshot(root, document)
	document.Version = 2
	document.Text = "broken"
	broken := store.Snapshot(root, document)
	if broken == nil || broken.Err == nil || broken.LastGood == nil {
		t.Fatalf("broken snapshot did not preserve LastGood: %#v", broken)
	}
	if got := store.goPackageDependentURIs(root, []string{modelPath}); !reflect.DeepEqual(got, []string{document.URI}) {
		t.Fatalf("LastGood Go package dependencies = %v, want [%q]", got, document.URI)
	}
}

func TestPhase15_4C2AInvalidateAllDropsGoPackageReverseIndex(t *testing.T) {
	root := t.TempDir()
	modelPath := "example.com/project/model"
	document := phase15C2ADocument(root, "index.gox", 1)
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		return phase15C2AModule(document.Path, phase15C2AGoDependency(modelPath, modelPath)), nil
	})
	store.Snapshot(root, document)
	if got := store.goPackageDependentURIs(root, []string{modelPath}); len(got) != 1 {
		t.Fatalf("reverse index was not seeded: %v", got)
	}

	store.InvalidateAll()
	if got := store.goPackageDependentURIs(root, []string{modelPath}); len(got) != 0 {
		t.Fatalf("InvalidateAll preserved Go package reverse index: %v", got)
	}
}

func phase15C2ADocument(root, name string, version int) Document {
	path := filepath.Join(root, "pages", name)
	return Document{URI: FileURI(path), Path: path, Version: version, Text: "module page\n"}
}

func phase15C2AModule(sourcePath string, dependencies ...goxcompiler.SemanticDependency) *goxcompiler.Module {
	for index := range dependencies {
		if dependencies[index].SourcePath == "" {
			dependencies[index].SourcePath = sourcePath
		}
	}
	return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: dependencies}}
}

func phase15C2AGoDependency(importPath, packagePath string) goxcompiler.SemanticDependency {
	return goxcompiler.SemanticDependency{
		Kind:        goxcompiler.SemanticDependencyGoPackage,
		ImportPath:  importPath,
		PackagePath: packagePath,
	}
}
