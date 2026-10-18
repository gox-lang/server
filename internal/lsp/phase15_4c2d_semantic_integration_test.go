package lsp

import (
	"go/types"
	"os"
	"path/filepath"
	"sync"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestPhase15_4C2DFullGoChangeFlowRebuildsAffectedSnapshotsWithNewCanonicalUniverse(t *testing.T) {
	root := phase15_4C2DProject(t, "example.com/project")
	modelFile := phase15_4C2DWriteGoFile(t, root, "model/user.go", `package model

type User struct { Name string }
`)
	phase15_4C2DWriteGoFile(t, root, "service/service.go", `package service

import "example.com/project/model"

func Current() model.User { return model.User{Name: "George"} }
`)
	phase15_4C2DWriteGoFile(t, root, "other/other.go", `package other

func Value() string { return "ok" }
`)

	server := NewServer()
	server.analysis = NewAnalysisStore()
	serviceDocument := phase15_4C2DDocument(root, "service.gox", `module service_page
import "example.com/project/service"
func Build() string { return service.Current().Name }
`)
	modelDocument := phase15_4C2DDocument(root, "model.gox", `module model_page
import "example.com/project/model"
func Build() string { return model.User{Name: "Milo"}.Name }
`)
	otherDocument := phase15_4C2DDocument(root, "other.gox", `module other_page
import "example.com/project/other"
func Build() string { return other.Value() }
`)

	oldServiceSnapshot := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(root, serviceDocument))
	oldModelSnapshot := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(root, modelDocument))
	oldOtherSnapshot := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(root, otherDocument))

	oldService := phase15_4C2DImportedPackage(oldServiceSnapshot.Module, "example.com/project/service")
	oldModelDirect := phase15_4C2DImportedPackage(oldModelSnapshot.Module, "example.com/project/model")
	oldModelTransitive := phase15_4C2DCurrentResultPackage(oldService)
	if oldService == nil || oldModelDirect == nil || oldModelTransitive == nil {
		t.Fatalf("initial semantic universe incomplete: service=%p direct-model=%p transitive-model=%p", oldService, oldModelDirect, oldModelTransitive)
	}
	if oldModelDirect != oldModelTransitive {
		t.Fatalf("initial semantic universe is not canonical: direct-model=%p transitive-model=%p", oldModelDirect, oldModelTransitive)
	}
	if phase15_4C2DStructHasField(oldModelDirect, "User", "Age") {
		t.Fatal("initial model unexpectedly contains Age field")
	}

	phase15_4C2DWriteGoFile(t, root, "model/user.go", `package model

type User struct {
	Name string
	Age int
}
`)
	if !server.handleWorkspaceFileChange(FileURI(modelFile), fileChanged) {
		t.Fatal("production .go change was not classified as Go-package-affecting")
	}

	if _, ok := server.analysis.Current(serviceDocument.URI); ok {
		t.Fatal("service-dependent snapshot survived integrated Go package invalidation")
	}
	if _, ok := server.analysis.Current(modelDocument.URI); ok {
		t.Fatal("model-dependent snapshot survived integrated Go package invalidation")
	}
	if current, ok := server.analysis.Current(otherDocument.URI); !ok || current != oldOtherSnapshot {
		t.Fatalf("unrelated immutable snapshot changed across importer rotation: current=%p ok=%v want=%p", current, ok, oldOtherSnapshot)
	}

	newServiceSnapshot := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(root, serviceDocument))
	newModelSnapshot := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(root, modelDocument))
	newService := phase15_4C2DImportedPackage(newServiceSnapshot.Module, "example.com/project/service")
	newModelDirect := phase15_4C2DImportedPackage(newModelSnapshot.Module, "example.com/project/model")
	newModelTransitive := phase15_4C2DCurrentResultPackage(newService)
	if newService == nil || newModelDirect == nil || newModelTransitive == nil {
		t.Fatalf("rebuilt semantic universe incomplete: service=%p direct-model=%p transitive-model=%p", newService, newModelDirect, newModelTransitive)
	}
	if newModelDirect != newModelTransitive {
		t.Fatalf("rebuilt semantic universe is not canonical: direct-model=%p transitive-model=%p", newModelDirect, newModelTransitive)
	}
	if newModelDirect == oldModelDirect {
		t.Fatalf("semantic importer rotation reused stale model identity: old=%p new=%p", oldModelDirect, newModelDirect)
	}
	if newService == oldService {
		t.Fatalf("semantic importer rotation reused stale service identity: old=%p new=%p", oldService, newService)
	}
	if !phase15_4C2DStructHasField(newModelDirect, "User", "Age") {
		t.Fatal("rebuilt model export data did not observe newly added Age field")
	}
}

func TestPhase15_4C2DWorkspaceEventRejectsInflightOldSemanticPublicationAndRebuilds(t *testing.T) {
	root := phase15_4C2DProject(t, "example.com/inflight")
	modelFile := phase15_4C2DWriteGoFile(t, root, "model/user.go", `package model

type User struct { Name string }
`)
	document := phase15_4C2DDocument(root, "index.gox", `module page
import "example.com/inflight/model"
func Build() string { return model.User{Name: "Milo"}.Name }
`)

	semanticCache := goxcompiler.NewSemanticWorkspaceCache()
	started := make(chan *AnalysisSnapshot, 1)
	release := make(chan struct{})
	var once sync.Once
	store := newAnalysisStore(func(root string, document Document) (*goxcompiler.Module, error) {
		module, err := goxcompiler.ParseProjectModuleCached(root, document.Path, document.Text, semanticCache)
		if err == nil {
			once.Do(func() {
				started <- &AnalysisSnapshot{Root: root, Document: document, Module: module}
				<-release
			})
		}
		return module, err
	})
	store.semanticCache = semanticCache
	server := NewServer()
	server.analysis = store

	done := make(chan *AnalysisSnapshot, 1)
	go func() { done <- store.Snapshot(root, document) }()
	oldParsed := <-started
	oldModel := phase15_4C2DImportedPackage(oldParsed.Module, "example.com/inflight/model")
	if oldModel == nil || phase15_4C2DStructHasField(oldModel, "User", "Age") {
		t.Fatalf("old in-flight semantic result is invalid: model=%p", oldModel)
	}

	phase15_4C2DWriteGoFile(t, root, "model/user.go", `package model

type User struct {
	Name string
	Age int
}
`)
	if !server.handleWorkspaceFileChange(FileURI(modelFile), fileChanged) {
		t.Fatal("workspace event did not rotate semantic generation")
	}
	close(release)
	oldResult := <-done
	if oldResult == nil || oldResult.Module == nil {
		t.Fatalf("caller lost completed old-generation result: %#v", oldResult)
	}
	if _, ok := store.Current(document.URI); ok {
		t.Fatal("old-generation in-flight result repopulated AnalysisStore after workspace event")
	}

	newSnapshot := phase15_4C2DRequireSnapshot(t, store.Snapshot(root, document))
	newModel := phase15_4C2DImportedPackage(newSnapshot.Module, "example.com/inflight/model")
	if newModel == nil || newModel == oldModel {
		t.Fatalf("post-event rebuild did not enter a new semantic universe: old=%p new=%p", oldModel, newModel)
	}
	if !phase15_4C2DStructHasField(newModel, "User", "Age") {
		t.Fatal("post-event rebuild did not observe new model API")
	}
}

func TestPhase15_4C2DConsecutiveGoEventsRotateSemanticUniverseWithoutDroppingUnrelatedSnapshot(t *testing.T) {
	root := phase15_4C2DProject(t, "example.com/consecutive")
	modelFile := phase15_4C2DWriteGoFile(t, root, "model/user.go", "package model\ntype User struct { Name string }\n")
	phase15_4C2DWriteGoFile(t, root, "other/other.go", "package other\nfunc Value() string { return \"ok\" }\n")
	server := NewServer()
	server.analysis = NewAnalysisStore()
	modelDocument := phase15_4C2DDocument(root, "model.gox", "module page\nimport \"example.com/consecutive/model\"\nfunc Build() string { return model.User{Name: \"Milo\"}.Name }\n")
	otherDocument := phase15_4C2DDocument(root, "other.gox", "module other_page\nimport \"example.com/consecutive/other\"\nfunc Build() string { return other.Value() }\n")

	first := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(root, modelDocument))
	unrelated := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(root, otherDocument))
	firstModel := phase15_4C2DImportedPackage(first.Module, "example.com/consecutive/model")

	phase15_4C2DWriteGoFile(t, root, "model/user.go", "package model\ntype User struct { Name string; Age int }\n")
	server.handleWorkspaceFileChange(FileURI(modelFile), fileChanged)
	second := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(root, modelDocument))
	secondModel := phase15_4C2DImportedPackage(second.Module, "example.com/consecutive/model")
	if firstModel == nil || secondModel == nil || firstModel == secondModel {
		t.Fatalf("first rotation did not replace model identity: first=%p second=%p", firstModel, secondModel)
	}

	phase15_4C2DWriteGoFile(t, root, "model/user.go", "package model\ntype User struct { Name string; Age int; Active bool }\n")
	server.handleWorkspaceFileChange(FileURI(modelFile), fileChanged)
	third := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(root, modelDocument))
	thirdModel := phase15_4C2DImportedPackage(third.Module, "example.com/consecutive/model")
	if thirdModel == nil || thirdModel == secondModel {
		t.Fatalf("second rotation did not replace model identity: second=%p third=%p", secondModel, thirdModel)
	}
	if !phase15_4C2DStructHasField(thirdModel, "User", "Active") {
		t.Fatal("second rotation did not observe latest model API")
	}
	if current, ok := server.analysis.Current(otherDocument.URI); !ok || current != unrelated {
		t.Fatalf("consecutive semantic rotations disturbed unrelated snapshot: current=%p ok=%v want=%p", current, ok, unrelated)
	}
}

func TestPhase15_4C2DConcurrentRootEventsRemainIsolated(t *testing.T) {
	rootOne := phase15_4C2DProject(t, "example.com/shared")
	rootTwo := phase15_4C2DProject(t, "example.com/shared")
	modelOne := phase15_4C2DWriteGoFile(t, rootOne, "model/user.go", "package model\ntype User struct { One string }\n")
	modelTwo := phase15_4C2DWriteGoFile(t, rootTwo, "model/user.go", "package model\ntype User struct { Two string }\n")
	server := NewServer()
	server.analysis = NewAnalysisStore()
	docOne := phase15_4C2DDocument(rootOne, "one.gox", "module one\nimport \"example.com/shared/model\"\nfunc Build() model.User { return model.User{} }\n")
	docTwo := phase15_4C2DDocument(rootTwo, "two.gox", "module two\nimport \"example.com/shared/model\"\nfunc Build() model.User { return model.User{} }\n")

	oldOne := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(rootOne, docOne))
	oldTwo := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(rootTwo, docTwo))
	oldPkgOne := phase15_4C2DImportedPackage(oldOne.Module, "example.com/shared/model")
	oldPkgTwo := phase15_4C2DImportedPackage(oldTwo.Module, "example.com/shared/model")
	if oldPkgOne == nil || oldPkgTwo == nil || oldPkgOne == oldPkgTwo {
		t.Fatalf("root-scoped semantic universes were not distinct initially: one=%p two=%p", oldPkgOne, oldPkgTwo)
	}

	phase15_4C2DWriteGoFile(t, rootOne, "model/user.go", "package model\ntype User struct { One string; ChangedOne bool }\n")
	phase15_4C2DWriteGoFile(t, rootTwo, "model/user.go", "package model\ntype User struct { Two string; ChangedTwo bool }\n")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.handleWorkspaceFileChange(FileURI(modelOne), fileChanged)
	}()
	go func() {
		defer wg.Done()
		server.handleWorkspaceFileChange(FileURI(modelTwo), fileChanged)
	}()
	wg.Wait()

	if _, ok := server.analysis.Current(docOne.URI); ok {
		t.Fatal("root-one dependent snapshot survived concurrent root event")
	}
	if _, ok := server.analysis.Current(docTwo.URI); ok {
		t.Fatal("root-two dependent snapshot survived concurrent root event")
	}
	newOne := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(rootOne, docOne))
	newTwo := phase15_4C2DRequireSnapshot(t, server.analysis.Snapshot(rootTwo, docTwo))
	newPkgOne := phase15_4C2DImportedPackage(newOne.Module, "example.com/shared/model")
	newPkgTwo := phase15_4C2DImportedPackage(newTwo.Module, "example.com/shared/model")
	if newPkgOne == nil || newPkgTwo == nil || newPkgOne == newPkgTwo {
		t.Fatalf("concurrent rebuilds mixed root semantic identities: one=%p two=%p", newPkgOne, newPkgTwo)
	}
	if newPkgOne == oldPkgOne || newPkgTwo == oldPkgTwo {
		t.Fatalf("concurrent root events kept stale identities: one old/new=%p/%p two old/new=%p/%p", oldPkgOne, newPkgOne, oldPkgTwo, newPkgTwo)
	}
	if !phase15_4C2DStructHasField(newPkgOne, "User", "ChangedOne") {
		t.Fatal("root-one rebuild did not observe root-one API change")
	}
	if !phase15_4C2DStructHasField(newPkgTwo, "User", "ChangedTwo") {
		t.Fatal("root-two rebuild did not observe root-two API change")
	}
}

func phase15_4C2DProject(t testing.TB, modulePath string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func phase15_4C2DWriteGoFile(t testing.TB, root, relative, source string) string {
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

func phase15_4C2DDocument(root, name, source string) Document {
	path := filepath.Join(root, "pages", name)
	return Document{URI: FileURI(path), Path: path, Version: 1, Text: source}
}

func phase15_4C2DRequireSnapshot(t testing.TB, snapshot *AnalysisSnapshot) *AnalysisSnapshot {
	t.Helper()
	if snapshot == nil {
		t.Fatal("semantic snapshot is nil")
	}
	if snapshot.Err != nil {
		t.Fatalf("semantic snapshot error: %v", snapshot.Err)
	}
	if snapshot.Module == nil || snapshot.Module.Semantic == nil {
		t.Fatalf("semantic snapshot is incomplete: %#v", snapshot)
	}
	for _, err := range snapshot.Module.Semantic.Errors {
		if err != nil {
			t.Fatalf("semantic type error: %v", err)
		}
	}
	return snapshot
}

func phase15_4C2DImportedPackage(module *goxcompiler.Module, importPath string) *types.Package {
	if module == nil || module.Semantic == nil || module.Semantic.Package == nil {
		return nil
	}
	for _, imported := range module.Semantic.Package.Imports() {
		if imported != nil && imported.Path() == importPath {
			return imported
		}
	}
	return nil
}

func phase15_4C2DCurrentResultPackage(pkg *types.Package) *types.Package {
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
	return phase15_4C2DTypePackage(signature.Results().At(0).Type())
}

func phase15_4C2DTypePackage(value types.Type) *types.Package {
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
		return phase15_4C2DTypePackage(typed.Elem())
	}
	return nil
}

func phase15_4C2DStructHasField(pkg *types.Package, typeName, fieldName string) bool {
	if pkg == nil {
		return false
	}
	object, _ := pkg.Scope().Lookup(typeName).(*types.TypeName)
	if object == nil {
		return false
	}
	named, _ := object.Type().(*types.Named)
	if named == nil {
		return false
	}
	structure, _ := named.Underlying().(*types.Struct)
	if structure == nil {
		return false
	}
	for index := 0; index < structure.NumFields(); index++ {
		if field := structure.Field(index); field != nil && field.Name() == fieldName {
			return true
		}
	}
	return false
}
