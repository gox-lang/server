package gox

import (
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseProjectModuleSemanticWorkspaceResolvesLocalGoPackageDeclaredName(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "domain", "user.go"), `package model

type User struct { Name string }
func New(name string) User { return User{Name: name} }
`)
	path := filepath.Join(root, "pages", "index.gox")
	source := `module page
import "example.com/project/domain"
func Build() string {
	user := model.New("George")
	return user.Name
}
`

	module, err := ParseProjectModule(root, path, source)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, module.Semantic, "could not import")
	assertNoSemanticErrorContaining(t, module.Semantic, "undefined: model")

	selector := semanticWorkspaceSelector(t, module, "New")
	object := module.Semantic.Info.ObjectOf(selector.Sel)
	if object == nil || object.Pkg() == nil {
		t.Fatalf("model.New has no imported go/types object: %#v", object)
	}
	if object.Pkg().Path() != "example.com/project/domain" || object.Pkg().Name() != "model" {
		t.Fatalf("package = %q/%q, want example.com/project/domain model", object.Pkg().Path(), object.Pkg().Name())
	}
	if len(module.Semantic.Dependencies) != 1 || module.Semantic.Dependencies[0].Kind != SemanticDependencyGoPackage || module.Semantic.Dependencies[0].PackagePath != "example.com/project/domain" {
		t.Fatalf("semantic dependencies = %#v", module.Semantic.Dependencies)
	}

	nameSelector := semanticWorkspaceSelector(t, module, "Name")
	if got := types.TypeString(module.Semantic.Info.TypeOf(nameSelector), nil); got != "string" {
		t.Fatalf("user.Name type = %q, want string", got)
	}
}

func TestParseProjectModuleSemanticWorkspaceResolvesTransitiveLocalGoPackageTypes(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "model", "user.go"), `package model

type User struct { Name string }
`)
	writeSemanticWorkspaceFile(t, filepath.Join(root, "service", "service.go"), `package service
import "example.com/project/model"
func Current() model.User { return model.User{Name: "George"} }
`)
	path := filepath.Join(root, "pages", "index.gox")
	source := `module page
import "example.com/project/service"
func Build() string { return service.Current().Name }
`

	module, err := ParseProjectModule(root, path, source)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, module.Semantic, "could not import")
	name := semanticWorkspaceSelector(t, module, "Name")
	if got := types.TypeString(module.Semantic.Info.TypeOf(name), nil); got != "string" {
		t.Fatalf("service.Current().Name type = %q, want string", got)
	}
}

func TestParseProjectModuleSemanticWorkspaceResolvesReplaceDependency(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), `module example.com/project

go 1.24

require example.com/library v0.0.0
replace example.com/library => ./third_party/library
`)
	writeSemanticWorkspaceFile(t, filepath.Join(root, "third_party", "library", "go.mod"), "module example.com/library\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "third_party", "library", "value.go"), `package toolkit
func Value() int { return 42 }
`)
	path := filepath.Join(root, "pages", "index.gox")
	source := `module page
import "example.com/library"
func Build() int { return toolkit.Value() }
`

	module, err := ParseProjectModule(root, path, source)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, module.Semantic, "could not import")
	assertNoSemanticErrorContaining(t, module.Semantic, "undefined: toolkit")
	selector := semanticWorkspaceSelector(t, module, "Value")
	object := module.Semantic.Info.ObjectOf(selector.Sel)
	if object == nil || object.Pkg() == nil || object.Pkg().Path() != "example.com/library" || object.Pkg().Name() != "toolkit" {
		t.Fatalf("toolkit.Value object = %#v", object)
	}
}

func TestParseProjectModuleSemanticWorkspaceResolvesGoxModule(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	child := filepath.Join(root, "pages", "users.gox")
	writeSemanticWorkspaceFile(t, child, `module users

type User struct { Name string }
func NewUser(name string) User { return User{Name: name} }
`)
	path := filepath.Join(root, "pages", "index.gox")
	source := `module page
import users "./users.gox"
func Build() string { return users.NewUser("George").Name }
`

	module, err := ParseProjectModule(root, path, source)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, module.Semantic, "could not import")
	assertNoSemanticErrorContaining(t, module.Semantic, "undefined: users")

	newUser := semanticWorkspaceSelector(t, module, "NewUser")
	object := module.Semantic.Info.ObjectOf(newUser.Sel)
	if object == nil || object.Pkg() == nil {
		t.Fatalf("users.NewUser has no imported go/types object: %#v", object)
	}
	resolved, ok := SemanticGoxPackageSource(object.Pkg().Path())
	if !ok || filepath.Clean(resolved) != filepath.Clean(child) {
		t.Fatalf("GOX package path %q resolved to %q, %v; want %q", object.Pkg().Path(), resolved, ok, child)
	}
	if object.Pkg().Name() != "users" {
		t.Fatalf("GOX package name = %q, want users", object.Pkg().Name())
	}
	if len(module.Semantic.Dependencies) != 1 {
		t.Fatalf("semantic dependencies = %#v", module.Semantic.Dependencies)
	}
	dependency := module.Semantic.Dependencies[0]
	if dependency.Kind != SemanticDependencyGoxModule || filepath.Clean(dependency.ResolvedPath) != filepath.Clean(child) || dependency.PackagePath != object.Pkg().Path() {
		t.Fatalf("GOX semantic dependency = %#v", dependency)
	}
	name := semanticWorkspaceSelector(t, module, "Name")
	if got := types.TypeString(module.Semantic.Info.TypeOf(name), nil); got != "string" {
		t.Fatalf("users.NewUser(...).Name type = %q, want string", got)
	}
}

func TestParseProjectModuleSemanticWorkspaceEnforcesGoxExportBoundary(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "pages", "users.gox"), `module users
func hidden() string { return "secret" }
`)
	path := filepath.Join(root, "pages", "index.gox")
	source := `module page
import users "./users.gox"
func Build() string { return users.hidden() }
`

	module, err := ParseProjectModule(root, path, source)
	if err != nil {
		t.Fatal(err)
	}
	selector := semanticWorkspaceSelector(t, module, "hidden")
	object := module.Semantic.Info.ObjectOf(selector.Sel)
	if object == nil {
		t.Fatal("go/types did not identify the private GOX symbol referenced by the invalid selector")
	}
	if object.Exported() {
		t.Fatalf("private GOX symbol reported as exported across module boundary: %#v", object)
	}
	assertSemanticErrorContaining(t, module.Semantic, "not exported by package users")
}

func TestParseProjectModuleSemanticWorkspaceUsesDeclaredGoxModuleName(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "pages", "user.gox"), `module users
func Count() int { return 1 }
`)
	path := filepath.Join(root, "pages", "index.gox")
	source := `module page
import "./user.gox"
func Build() int { return users.Count() }
`

	module, err := ParseProjectModule(root, path, source)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, module.Semantic, "undefined: users")
	selector := semanticWorkspaceSelector(t, module, "Count")
	object := module.Semantic.Info.ObjectOf(selector.Sel)
	if object == nil || object.Pkg() == nil || object.Pkg().Name() != "users" {
		t.Fatalf("users.Count object = %#v", object)
	}
}

func TestParseProjectModuleSemanticWorkspaceResolvesTransitiveGoxModules(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "pages", "users.gox"), `module users

type User struct { Name string }
func New(name string) User { return User{Name: name} }
`)
	writeSemanticWorkspaceFile(t, filepath.Join(root, "pages", "accounts.gox"), `module accounts
import users "./users.gox"
func Current() users.User { return users.New("Milo") }
`)
	path := filepath.Join(root, "pages", "index.gox")
	source := `module page
import accounts "./accounts.gox"
func Build() string { return accounts.Current().Name }
`

	module, err := ParseProjectModule(root, path, source)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, module.Semantic, "could not import")
	name := semanticWorkspaceSelector(t, module, "Name")
	if got := types.TypeString(module.Semantic.Info.TypeOf(name), nil); got != "string" {
		t.Fatalf("accounts.Current().Name type = %q, want string", got)
	}
}

func TestParseProjectModuleSemanticWorkspaceResolvesAncestorGoxAlias(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	menu := filepath.Join(root, "pages", "dashboard", "@components", "menu.gox")
	writeSemanticWorkspaceFile(t, menu, `module menu

type Item struct { Title string }
func New() Item { return Item{Title: "Home"} }
`)
	path := filepath.Join(root, "pages", "dashboard", "reports", "index.gox")
	source := `module page
import Menu "@components/menu.gox"
func Build() string { return Menu.New().Title }
`

	module, err := ParseProjectModule(root, path, source)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, module.Semantic, "could not import")
	selector := semanticWorkspaceSelector(t, module, "New")
	object := module.Semantic.Info.ObjectOf(selector.Sel)
	if object == nil || object.Pkg() == nil {
		t.Fatalf("Menu.New object = %#v", object)
	}
	resolved, ok := SemanticGoxPackageSource(object.Pkg().Path())
	if !ok || filepath.Clean(resolved) != filepath.Clean(menu) {
		t.Fatalf("Menu package source = %q, %v; want %q", resolved, ok, menu)
	}
}

func TestParseProjectModuleSemanticWorkspaceRecordsDependencyChain(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "model", "item.go"), `package model

type Item struct { Title string }
func New() Item { return Item{Title: "Home"} }
`)
	menu := filepath.Join(root, "pages", "@menu.gox")
	writeSemanticWorkspaceFile(t, menu, `module menu
import "example.com/project/model"
func Current() model.Item { return model.New() }
`)
	path := filepath.Join(root, "pages", "index.gox")
	source := `module page
import "@menu.gox"
func Build() string { return menu.Current().Title }
`

	module, err := ParseProjectModule(root, path, source)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSemanticErrorContaining(t, module.Semantic, "could not import")
	if len(module.Semantic.Dependencies) != 2 {
		t.Fatalf("semantic dependency chain = %#v, want 2 edges", module.Semantic.Dependencies)
	}

	rootEdge := module.Semantic.Dependencies[0]
	if filepath.Clean(rootEdge.SourcePath) != filepath.Clean(path) ||
		rootEdge.Kind != SemanticDependencyGoxModule ||
		filepath.Clean(rootEdge.ResolvedPath) != filepath.Clean(menu) {
		t.Fatalf("root dependency edge = %#v", rootEdge)
	}
	menuEdge := module.Semantic.Dependencies[1]
	if filepath.Clean(menuEdge.SourcePath) != filepath.Clean(menu) ||
		menuEdge.Kind != SemanticDependencyGoPackage ||
		menuEdge.PackagePath != "example.com/project/model" {
		t.Fatalf("menu dependency edge = %#v", menuEdge)
	}
}

func semanticWorkspaceSelector(t *testing.T, module *Module, name string) *ast.SelectorExpr {
	t.Helper()
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil {
		t.Fatal("module has no semantic model")
	}
	var found *ast.SelectorExpr
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil || selector.Sel.Name != name {
			return true
		}
		if found == nil {
			found = selector
		}
		return true
	})
	if found == nil {
		t.Fatalf("semantic selector %q not found", name)
	}
	return found
}

func assertSemanticErrorContaining(t *testing.T, semantic *SemanticModel, fragment string) {
	t.Helper()
	if semantic == nil {
		t.Fatal("semantic model is nil")
	}
	for _, err := range semantic.Errors {
		if strings.Contains(err.Error(), fragment) {
			return
		}
	}
	t.Fatalf("missing semantic error containing %q: %v", fragment, semantic.Errors)
}

func assertNoSemanticErrorContaining(t *testing.T, semantic *SemanticModel, fragment string) {
	t.Helper()
	if semantic == nil {
		t.Fatal("semantic model is nil")
	}
	for _, err := range semantic.Errors {
		if strings.Contains(err.Error(), fragment) {
			t.Fatalf("unexpected semantic error containing %q: %v", fragment, semantic.Errors)
		}
	}
}

func writeSemanticWorkspaceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSemanticWorkspaceCacheReusesGoPackageIdentityAcrossProjectAnalyses(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() string { return strings.TrimSpace(" value ") }
`
	cache := NewSemanticWorkspaceCache()
	first, err := ParseProjectModuleCached(root, path, source, cache)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseProjectModuleCached(root, path, source, cache)
	if err != nil {
		t.Fatal(err)
	}
	firstPackage := semanticImportedPackage(first, "strings")
	secondPackage := semanticImportedPackage(second, "strings")
	if firstPackage == nil || secondPackage == nil {
		t.Fatalf("imported package identity missing: first=%p second=%p", firstPackage, secondPackage)
	}
	if firstPackage != secondPackage {
		t.Fatalf("Go package identity was rebuilt across cached analyses: first=%p second=%p", firstPackage, secondPackage)
	}
}

func TestSemanticWorkspaceCacheInvalidatesOnlyOneRoot(t *testing.T) {
	cache := NewSemanticWorkspaceCache()
	one := types.NewPackage("example.com/one", "one")
	two := types.NewPackage("example.com/two", "two")
	cache.StoreGoPackage("/workspace/one", one.Path(), one)
	cache.StoreGoPackage("/workspace/two", two.Path(), two)
	cache.InvalidateRoot("/workspace/one")
	if _, ok := cache.GoPackage("/workspace/one", one.Path()); ok {
		t.Fatal("invalidated root kept cached Go package")
	}
	if cached, ok := cache.GoPackage("/workspace/two", two.Path()); !ok || cached != two {
		t.Fatalf("unrelated root cache = %p/%v, want %p/true", cached, ok, two)
	}
}

func semanticImportedPackage(module *Module, importPath string) *types.Package {
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
