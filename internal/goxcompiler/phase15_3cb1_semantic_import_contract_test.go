package gox

import (
	"go/types"
	"path/filepath"
	"testing"
)

// TestPhase15_3CB1SemanticWorkspacePreservesTransitiveExportIdentityAcrossAnalyses
// captures the package-identity contract that any primeGoImports optimization
// must preserve. A cached direct package may expose a type owned by one of its
// transitive dependencies; a later analysis that imports that dependency
// directly must reuse the exact same *types.Package identity.
func TestPhase15_3CB1SemanticWorkspacePreservesTransitiveExportIdentityAcrossAnalyses(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "model", "user.go"), `package model

type User struct { Name string }
func Name(user User) string { return user.Name }
`)
	writeSemanticWorkspaceFile(t, filepath.Join(root, "service", "service.go"), `package service

import "example.com/project/model"

func Current() model.User { return model.User{Name: "George"} }
`)

	cache := NewSemanticWorkspaceCache()
	firstPath := filepath.Join(root, "pages", "first.gox")
	firstSource := `module first
import "example.com/project/service"
func Build() string { return service.Current().Name }
`
	first, err := ParseProjectModuleCached(root, firstPath, firstSource, cache)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, first.Semantic, "could not import")

	firstService := semanticImportedPackage(first, "example.com/project/service")
	if firstService == nil {
		t.Fatal("first analysis did not import service")
	}
	transitiveModel := importedTypesPackage(firstService, "example.com/project/model")
	if transitiveModel == nil {
		t.Fatal("service package does not expose its model dependency identity")
	}

	secondPath := filepath.Join(root, "pages", "second.gox")
	secondSource := `module second
import "example.com/project/service"
import "example.com/project/model"
func Build() string { return model.Name(service.Current()) }
`
	second, err := ParseProjectModuleCached(root, secondPath, secondSource, cache)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, second.Semantic, "could not import")
	assertNoSemanticErrorContaining(t, second.Semantic, "cannot use")

	secondService := semanticImportedPackage(second, "example.com/project/service")
	secondModel := semanticImportedPackage(second, "example.com/project/model")
	if secondService == nil || secondModel == nil {
		t.Fatalf("second imports missing: service=%p model=%p", secondService, secondModel)
	}
	if secondService != firstService {
		t.Fatalf("cached service identity changed: first=%p second=%p", firstService, secondService)
	}
	if secondModel != transitiveModel {
		t.Fatalf("transitive model identity was not reused: transitive=%p direct=%p", transitiveModel, secondModel)
	}

	current, _ := secondService.Scope().Lookup("Current").(*types.Func)
	if current == nil {
		t.Fatal("service.Current is missing from imported package scope")
	}
	signature, _ := current.Type().(*types.Signature)
	if signature == nil || signature.Results() == nil || signature.Results().Len() != 1 {
		t.Fatalf("service.Current signature = %v", current.Type())
	}
	resultNamed := namedType(signature.Results().At(0).Type())
	if resultNamed == nil || resultNamed.Obj() == nil || resultNamed.Obj().Pkg() != secondModel {
		t.Fatalf("service.Current result package = %p, want direct model package %p", packageOfNamed(resultNamed), secondModel)
	}
}

func importedTypesPackage(pkg *types.Package, importPath string) *types.Package {
	if pkg == nil {
		return nil
	}
	for _, imported := range pkg.Imports() {
		if imported != nil && imported.Path() == importPath {
			return imported
		}
	}
	return nil
}

func namedType(typ types.Type) *types.Named {
	switch value := typ.(type) {
	case *types.Named:
		return value
	case *types.Alias:
		if named, ok := types.Unalias(value).(*types.Named); ok {
			return named
		}
	}
	return nil
}

func packageOfNamed(named *types.Named) *types.Package {
	if named == nil || named.Obj() == nil {
		return nil
	}
	return named.Obj().Pkg()
}
