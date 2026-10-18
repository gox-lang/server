package gox

import (
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"golang.org/x/tools/go/packages"
)

type phase15CB2LoadModeCase struct {
	name string
	mode packages.LoadMode
}

func phase15CB2LoadModes() []phase15CB2LoadModeCase {
	return []phase15CB2LoadModeCase{
		{
			name: "CurrentNeedDeps",
			mode: packages.NeedName | packages.NeedTypes | packages.NeedImports | packages.NeedDeps,
		},
		{
			name: "LoadTypes",
			mode: packages.LoadTypes,
		},
		{
			name: "NeedTypesImports",
			mode: packages.NeedName | packages.NeedTypes | packages.NeedImports,
		},
		{
			name: "NeedTypesOnly",
			mode: packages.NeedName | packages.NeedTypes,
		},
	}
}

// TestPhase15_3CB2LoadModeContracts audits the semantic information returned by
// each candidate LoadMode without changing production. The current mode remains
// the control. Candidate modes are reported rather than asserted so one invalid
// candidate cannot hide the results of the remaining candidates.
func TestPhase15_3CB2LoadModeContracts(t *testing.T) {
	root := t.TempDir()
	phase15CB2WriteFile(t, filepath.Join(root, "go.mod"), `module example.com/project

go 1.24

require example.com/library v0.0.0
replace example.com/library => ./third_party/library
`)
	phase15CB2WriteFile(t, filepath.Join(root, "model", "user.go"), `package model

type User struct { Name string }
func Name(user User) string { return user.Name }
`)
	phase15CB2WriteFile(t, filepath.Join(root, "service", "service.go"), `package service

import "example.com/project/model"

func Current() model.User { return model.User{Name: "George"} }
`)
	phase15CB2WriteFile(t, filepath.Join(root, "third_party", "library", "go.mod"), "module example.com/library\n\ngo 1.24\n")
	phase15CB2WriteFile(t, filepath.Join(root, "third_party", "library", "value.go"), `package toolkit

type Value struct{ Name string }
func New() Value { return Value{Name: "ok"} }
`)

	for _, candidate := range phase15CB2LoadModes() {
		candidate := candidate
		t.Run(candidate.name, func(t *testing.T) {
			result, loadErr := phase15CB2InspectMode(root, candidate.mode)
			contractErr := loadErr
			if contractErr == nil {
				contractErr = result.contractError()
			}
			t.Logf(
				"contractOK=%t loadError=%v directComplete=%t directName=%q directPath=%q imports=%d closure=%d closureComplete=%d modelInClosure=%t resultIdentity=%t replaceComplete=%t replaceName=%q replacePath=%q",
				contractErr == nil,
				loadErr,
				result.directComplete,
				result.directName,
				result.directPath,
				result.directImports,
				result.closurePackages,
				result.completeClosurePackages,
				result.modelInClosure,
				result.resultIdentity,
				result.replaceComplete,
				result.replaceName,
				result.replacePath,
			)

			if candidate.name == "CurrentNeedDeps" && contractErr != nil {
				t.Fatalf("current load mode no longer satisfies B1 contract: %v", contractErr)
			}
		})
	}
}

// BenchmarkPhase15_3CB2LoadModeStrings isolates the go/packages cost that
// primeGoImports currently pays for one direct standard-library import. Run it
// with -benchtime=1x and multiple -count values so each sample represents one
// packages.Load invocation rather than a warm application-level cache.
func BenchmarkPhase15_3CB2LoadModeStrings(b *testing.B) {
	root := b.TempDir()
	phase15CB2WriteBenchmarkFile(b, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")

	for _, candidate := range phase15CB2LoadModes() {
		candidate := candidate
		b.Run(candidate.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				loaded, err := packages.Load(&packages.Config{
					Mode: candidate.mode,
					Dir:  root,
				}, "strings")
				if err != nil {
					b.Fatal(err)
				}
				pkg := phase15CB2FindLoadedPackage(loaded, "strings")
				if pkg == nil || pkg.Types == nil {
					b.Fatalf("strings Types missing: %s", phase15CB2PackageErrors(loaded))
				}
				closure, complete := phase15CB2TypesClosure(pkg.Types)
				b.ReportMetric(float64(len(closure)), "closure-pkgs/op")
				b.ReportMetric(float64(complete), "closure-complete/op")
			}
		})
	}
}

// BenchmarkPhase15_3CB2LoadModeWorkspaceExportIdentity measures the same modes
// on the exact semantic shape protected by B1: service.Current exposes
// model.User, so a cheaper mode must return a complete service package whose
// exported signature points at the model identity available from the types
// import closure.
func BenchmarkPhase15_3CB2LoadModeWorkspaceExportIdentity(b *testing.B) {
	root := b.TempDir()
	phase15CB2WriteBenchmarkFile(b, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	phase15CB2WriteBenchmarkFile(b, filepath.Join(root, "model", "user.go"), `package model

type User struct { Name string }
`)
	phase15CB2WriteBenchmarkFile(b, filepath.Join(root, "service", "service.go"), `package service

import "example.com/project/model"

func Current() model.User { return model.User{} }
`)

	for _, candidate := range phase15CB2LoadModes() {
		candidate := candidate
		b.Run(candidate.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				loaded, err := packages.Load(&packages.Config{
					Mode: candidate.mode,
					Dir:  root,
				}, "example.com/project/service")
				if err != nil {
					b.Fatal(err)
				}
				pkg := phase15CB2FindLoadedPackage(loaded, "example.com/project/service")
				if pkg == nil || pkg.Types == nil {
					b.Fatalf("service Types missing: %s", phase15CB2PackageErrors(loaded))
				}
				closure, complete := phase15CB2TypesClosure(pkg.Types)
				model := closure["example.com/project/model"]
				identityOK := phase15CB2CurrentResultPackage(pkg.Types) == model && model != nil
				b.ReportMetric(float64(len(closure)), "closure-pkgs/op")
				b.ReportMetric(float64(complete), "closure-complete/op")
				if identityOK {
					b.ReportMetric(1, "identity-ok/op")
				} else {
					b.ReportMetric(0, "identity-ok/op")
				}
			}
		})
	}
}

type phase15CB2ModeInspection struct {
	directComplete          bool
	directName              string
	directPath              string
	directImports           int
	closurePackages         int
	completeClosurePackages int
	modelInClosure          bool
	resultIdentity          bool
	replaceComplete         bool
	replaceName             string
	replacePath             string
}

func (r phase15CB2ModeInspection) contractError() error {
	if !r.directComplete {
		return fmt.Errorf("direct service package is incomplete")
	}
	if r.directName != "service" {
		return fmt.Errorf("direct package name = %q, want service", r.directName)
	}
	if r.directPath != "example.com/project/service" {
		return fmt.Errorf("direct package path = %q", r.directPath)
	}
	if !r.modelInClosure {
		return fmt.Errorf("model package is absent from types import closure")
	}
	if !r.resultIdentity {
		return fmt.Errorf("service.Current result does not reuse model closure identity")
	}
	if !r.replaceComplete {
		return fmt.Errorf("replace dependency package is incomplete")
	}
	if r.replaceName != "toolkit" {
		return fmt.Errorf("replace dependency package name = %q, want toolkit", r.replaceName)
	}
	if r.replacePath != "example.com/library" {
		return fmt.Errorf("replace dependency package path = %q, want example.com/library", r.replacePath)
	}
	return nil
}

func phase15CB2InspectMode(root string, mode packages.LoadMode) (phase15CB2ModeInspection, error) {
	loaded, err := packages.Load(&packages.Config{Mode: mode, Dir: root}, "example.com/project/service")
	if err != nil {
		return phase15CB2ModeInspection{}, fmt.Errorf("load service: %w", err)
	}
	pkg := phase15CB2FindLoadedPackage(loaded, "example.com/project/service")
	if pkg == nil || pkg.Types == nil {
		return phase15CB2ModeInspection{}, fmt.Errorf("service Types missing: %s", phase15CB2PackageErrors(loaded))
	}
	closure, complete := phase15CB2TypesClosure(pkg.Types)
	model := closure["example.com/project/model"]

	replaced, err := packages.Load(&packages.Config{Mode: mode, Dir: root}, "example.com/library")
	if err != nil {
		return phase15CB2ModeInspection{}, fmt.Errorf("load replace dependency: %w", err)
	}
	replacePkg := phase15CB2FindLoadedPackage(replaced, "example.com/library")
	if replacePkg == nil || replacePkg.Types == nil {
		return phase15CB2ModeInspection{}, fmt.Errorf("replace dependency Types missing: %s", phase15CB2PackageErrors(replaced))
	}

	return phase15CB2ModeInspection{
		directComplete:          pkg.Types.Complete(),
		directName:              pkg.Types.Name(),
		directPath:              pkg.Types.Path(),
		directImports:           len(pkg.Types.Imports()),
		closurePackages:         len(closure),
		completeClosurePackages: complete,
		modelInClosure:          model != nil,
		resultIdentity:          model != nil && phase15CB2CurrentResultPackage(pkg.Types) == model,
		replaceComplete:         replacePkg.Types.Complete(),
		replaceName:             replacePkg.Types.Name(),
		replacePath:             replacePkg.Types.Path(),
	}, nil
}

func phase15CB2TypesClosure(root *types.Package) (map[string]*types.Package, int) {
	closure := make(map[string]*types.Package)
	var visit func(*types.Package)
	visit = func(pkg *types.Package) {
		if pkg == nil || pkg.Path() == "" {
			return
		}
		if _, ok := closure[pkg.Path()]; ok {
			return
		}
		closure[pkg.Path()] = pkg
		for _, imported := range pkg.Imports() {
			visit(imported)
		}
	}
	visit(root)
	complete := 0
	for _, pkg := range closure {
		if pkg != nil && pkg.Complete() {
			complete++
		}
	}
	return closure, complete
}

func phase15CB2CurrentResultPackage(service *types.Package) *types.Package {
	if service == nil || service.Scope() == nil {
		return nil
	}
	current, _ := service.Scope().Lookup("Current").(*types.Func)
	if current == nil {
		return nil
	}
	signature, _ := current.Type().(*types.Signature)
	if signature == nil || signature.Results() == nil || signature.Results().Len() != 1 {
		return nil
	}
	named := namedType(signature.Results().At(0).Type())
	if named == nil || named.Obj() == nil {
		return nil
	}
	return named.Obj().Pkg()
}

func phase15CB2FindLoadedPackage(loaded []*packages.Package, path string) *packages.Package {
	for _, pkg := range loaded {
		if pkg == nil {
			continue
		}
		if pkg.PkgPath == path {
			return pkg
		}
		if pkg.Types != nil && pkg.Types.Path() == path {
			return pkg
		}
	}
	if len(loaded) == 1 {
		return loaded[0]
	}
	return nil
}

func phase15CB2PackageErrors(loaded []*packages.Package) string {
	messages := make([]string, 0)
	for _, pkg := range loaded {
		if pkg == nil {
			continue
		}
		for _, err := range pkg.Errors {
			messages = append(messages, err.Error())
		}
	}
	sort.Strings(messages)
	return fmt.Sprint(messages)
}

func phase15CB2WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func phase15CB2WriteBenchmarkFile(b *testing.B, path, content string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
}
