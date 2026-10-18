package gox

import (
	"go/types"
	"os"
	"path/filepath"
	"testing"
)

func TestPhase15_4C1ExportImporterRetainsPackageIdentityAfterSourceChanges(t *testing.T) {
	root := t.TempDir()
	phase15C1WriteFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	modelPath := filepath.Join(root, "model", "user.go")
	phase15C1WriteFile(t, modelPath, `package model

type User struct{ Name string }
`)

	importer := newSemanticGoExportImporter(root)
	first, err := importer.Import("example.com/project/model")
	if err != nil {
		t.Fatal(err)
	}
	if fields := phase15C1StructFieldCount(first, "User"); fields != 1 {
		t.Fatalf("initial model.User fields = %d, want 1", fields)
	}

	phase15C1WriteFile(t, modelPath, `package model

type User struct {
	Name string
	Age  int
}
`)

	second, err := importer.Import("example.com/project/model")
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("same importer replaced package identity unexpectedly: first=%p second=%p", first, second)
	}
	if fields := phase15C1StructFieldCount(second, "User"); fields != 1 {
		t.Fatalf("same importer observed edited package without rotation: fields=%d want stale cached 1", fields)
	}

	t.Log("audit: semanticGoExportImporter has an internal package cache with no package-level eviction; source changes remain stale until the importer universe is rotated")
}

func TestPhase15_4C1RotatingImporterCreatesDistinctButInternallyCanonicalUniverse(t *testing.T) {
	root := t.TempDir()
	phase15C1WriteFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	phase15C1WriteFile(t, filepath.Join(root, "model", "user.go"), `package model

type User struct{ Name string }
`)
	phase15C1WriteFile(t, filepath.Join(root, "service", "service.go"), `package service

import "example.com/project/model"

func Current() model.User { return model.User{Name: "George"} }
`)

	firstImporter := newSemanticGoExportImporter(root)
	first, err := firstImporter.ImportMany([]string{"example.com/project/service", "example.com/project/model"})
	if err != nil {
		t.Fatal(err)
	}
	firstService := first["example.com/project/service"]
	firstModel := first["example.com/project/model"]
	if firstService == nil || firstModel == nil {
		t.Fatalf("first universe missing packages: service=%p model=%p", firstService, firstModel)
	}
	if transitive := phase15C1CurrentResultPackage(firstService); transitive != firstModel {
		t.Fatalf("first universe is not canonical internally: service result=%p model=%p", transitive, firstModel)
	}

	secondImporter := newSemanticGoExportImporter(root)
	second, err := secondImporter.ImportMany([]string{"example.com/project/service", "example.com/project/model"})
	if err != nil {
		t.Fatal(err)
	}
	secondService := second["example.com/project/service"]
	secondModel := second["example.com/project/model"]
	if secondService == nil || secondModel == nil {
		t.Fatalf("second universe missing packages: service=%p model=%p", secondService, secondModel)
	}
	if transitive := phase15C1CurrentResultPackage(secondService); transitive != secondModel {
		t.Fatalf("second universe is not canonical internally: service result=%p model=%p", transitive, secondModel)
	}
	if firstService == secondService || firstModel == secondModel {
		t.Fatalf("rotated importers unexpectedly reused package identities: service=%p/%p model=%p/%p", firstService, secondService, firstModel, secondModel)
	}
	if transitive := phase15C1CurrentResultPackage(secondService); transitive == firstModel {
		t.Fatalf("new service package unexpectedly references old model identity: service result=%p old model=%p", transitive, firstModel)
	}

	t.Log("audit: rotating the importer safely refreshes changed packages only as a whole identity universe; preserving old cached packages alongside packages from the new importer would mix distinct go/types identities for the same import path")
}

func phase15C1WriteFile(t *testing.T, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func phase15C1StructFieldCount(pkg *types.Package, name string) int {
	if pkg == nil || pkg.Scope() == nil {
		return -1
	}
	object, _ := pkg.Scope().Lookup(name).(*types.TypeName)
	if object == nil {
		return -1
	}
	named, _ := types.Unalias(object.Type()).(*types.Named)
	if named == nil {
		return -1
	}
	structure, _ := named.Underlying().(*types.Struct)
	if structure == nil {
		return -1
	}
	return structure.NumFields()
}

func phase15C1CurrentResultPackage(pkg *types.Package) *types.Package {
	if pkg == nil || pkg.Scope() == nil {
		return nil
	}
	function, _ := pkg.Scope().Lookup("Current").(*types.Func)
	if function == nil {
		return nil
	}
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Results() == nil || signature.Results().Len() != 1 {
		return nil
	}
	return phase15C1TypePackage(signature.Results().At(0).Type())
}

func phase15C1TypePackage(value types.Type) *types.Package {
	switch typed := value.(type) {
	case *types.Named:
		if typed.Obj() != nil {
			return typed.Obj().Pkg()
		}
	case *types.Alias:
		if typed.Obj() != nil {
			return typed.Obj().Pkg()
		}
	case *types.Pointer:
		return phase15C1TypePackage(typed.Elem())
	}
	return nil
}
