package gox

import (
	"go/types"
	"os"
	"path/filepath"
	"testing"
)

func TestPhase15_3CB3BSemanticWorkspaceUsesOneExportDataUniverseForModuleAndStdlib(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "service", "service.go"), `package service

import "io"

func Reader() io.Reader { return nil }
`)
	path := filepath.Join(root, "pages", "index.gox")
	source := `module page
import "example.com/project/service"
import "io"
func Accept(reader io.Reader) string { return "ok" }
func Build() string { return Accept(service.Reader()) }
`

	cache := NewSemanticWorkspaceCache()
	module, err := ParseProjectModuleCached(root, path, source, cache)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, module.Semantic, "could not import")
	assertNoSemanticErrorContaining(t, module.Semantic, "cannot use")

	service := semanticImportedPackage(module, "example.com/project/service")
	ioPackage := semanticImportedPackage(module, "io")
	if service == nil || ioPackage == nil {
		t.Fatalf("imported packages missing: service=%p io=%p", service, ioPackage)
	}
	reader := service.Scope().Lookup("Reader")
	function, _ := reader.(*types.Func)
	if function == nil {
		t.Fatalf("service.Reader object = %#v", reader)
	}
	signature, _ := function.Type().(*types.Signature)
	if signature == nil || signature.Results() == nil || signature.Results().Len() != 1 {
		t.Fatalf("service.Reader signature = %v", function.Type())
	}
	resultPackage := phase15CB3BTypePackage(signature.Results().At(0).Type())
	if resultPackage != ioPackage {
		t.Fatalf("service.Reader io package identity = %p, direct io package = %p", resultPackage, ioPackage)
	}
}

func TestPhase15_3CB3BSharedCacheReusesExportImporterIdentityAcrossAnalyses(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "model", "user.go"), `package model

type User struct{ Name string }
func Name(user User) string { return user.Name }
`)
	writeSemanticWorkspaceFile(t, filepath.Join(root, "service", "service.go"), `package service

import "example.com/project/model"
func Current() model.User { return model.User{Name: "George"} }
`)
	cache := NewSemanticWorkspaceCache()

	firstPath := filepath.Join(root, "pages", "first.gox")
	first, err := ParseProjectModuleCached(root, firstPath, `module first
import "example.com/project/service"
func Build() string { return service.Current().Name }
`, cache)
	if err != nil {
		t.Fatal(err)
	}
	serviceFirst := semanticImportedPackage(first, "example.com/project/service")
	if serviceFirst == nil {
		t.Fatal("first analysis did not import service")
	}
	modelFromService := phase15CB3BCurrentResultPackage(serviceFirst)
	if modelFromService == nil {
		t.Fatal("service.Current result has no model package")
	}

	secondPath := filepath.Join(root, "pages", "second.gox")
	second, err := ParseProjectModuleCached(root, secondPath, `module second
import "example.com/project/service"
import "example.com/project/model"
func Build() string { return model.Name(service.Current()) }
`, cache)
	if err != nil {
		t.Fatal(err)
	}
	serviceSecond := semanticImportedPackage(second, "example.com/project/service")
	modelSecond := semanticImportedPackage(second, "example.com/project/model")
	if serviceSecond != serviceFirst {
		t.Fatalf("service identity changed across analyses: first=%p second=%p", serviceFirst, serviceSecond)
	}
	if modelSecond == nil || modelSecond != modelFromService {
		t.Fatalf("model identity changed across analyses: transitive=%p direct=%p", modelFromService, modelSecond)
	}
	assertNoSemanticErrorContaining(t, second.Semantic, "cannot use")
}

func TestPhase15_3CB3BSharedCacheInvalidationDropsModuleImporter(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := NewSemanticWorkspaceCache()
	_, first := cache.semanticWorkspaceLease(root, true)
	if first == nil {
		t.Fatal("first semantic Go importer is nil")
	}
	cache.InvalidateRoot(root)
	_, second := cache.semanticWorkspaceLease(root, true)
	if second == nil || second == first {
		t.Fatalf("root invalidation kept semantic Go importer: first=%p second=%p", first, second)
	}
}

func phase15CB3BCurrentResultPackage(pkg *types.Package) *types.Package {
	if pkg == nil {
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
	return phase15CB3BTypePackage(signature.Results().At(0).Type())
}

func phase15CB3BTypePackage(value types.Type) *types.Package {
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
		return phase15CB3BTypePackage(typed.Elem())
	case *types.Interface:
		// io.Reader itself is a named type; this branch is for defensive coverage
		// of unnamed interfaces exposed by future fixtures.
		return nil
	}
	return nil
}
