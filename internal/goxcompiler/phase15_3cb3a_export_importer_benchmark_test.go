package gox

import (
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestPhase15_3CB3AStdlibPersistentImporterContracts verifies that the standard
// library export-data importer provides exactly the semantic shape that
// semanticWorkspace needs: complete packages, complete imported identities,
// stable package pointers, and exported signatures that reuse those identities.
func TestPhase15_3CB3AStdlibPersistentImporterContracts(t *testing.T) {
	imp := importer.Default()

	stringsPkg, err := imp.Import("strings")
	if err != nil {
		t.Fatal(err)
	}
	if stringsPkg == nil || !stringsPkg.Complete() {
		t.Fatalf("strings package is incomplete: %v", stringsPkg)
	}
	if stringsPkg.Scope().Lookup("Split") == nil || stringsPkg.Scope().Lookup("NewReplacer") == nil {
		t.Fatal("strings export-data package is missing expected exported API")
	}
	closure, complete := phase15CB2TypesClosure(stringsPkg)
	if complete == 0 || len(closure) == 0 {
		t.Fatalf("strings import closure is empty or unusable: complete=%d closure=%d", complete, len(closure))
	}

	// go/importer intentionally creates canonical incomplete placeholders for
	// transitive packages that have not been imported directly yet. That is
	// acceptable for semanticWorkspace as long as importing the dependency later
	// returns the exact same *types.Package and completes it in place.
	if ioPlaceholder := closure["io"]; ioPlaceholder != nil {
		ioPkg, err := imp.Import("io")
		if err != nil {
			t.Fatal(err)
		}
		if ioPkg != ioPlaceholder {
			t.Fatalf("persistent importer changed io identity: placeholder=%p imported=%p", ioPlaceholder, ioPkg)
		}
		if !ioPkg.Complete() {
			t.Fatal("directly imported io package remains incomplete")
		}
	}

	stringsAgain, err := imp.Import("strings")
	if err != nil {
		t.Fatal(err)
	}
	if stringsAgain != stringsPkg {
		t.Fatalf("persistent importer changed strings identity: first=%p second=%p", stringsPkg, stringsAgain)
	}

	httpPkg, err := imp.Import("net/http")
	if err != nil {
		t.Fatal(err)
	}
	ioPkg, err := imp.Import("io")
	if err != nil {
		t.Fatal(err)
	}
	if httpPkg == nil || ioPkg == nil || !httpPkg.Complete() || !ioPkg.Complete() {
		t.Fatalf("export-data stdlib packages incomplete: http=%v io=%v", httpPkg, ioPkg)
	}
	newRequest, _ := httpPkg.Scope().Lookup("NewRequest").(*types.Func)
	if newRequest == nil {
		t.Fatal("net/http.NewRequest is missing")
	}
	signature, _ := newRequest.Type().(*types.Signature)
	if signature == nil || signature.Params() == nil || signature.Params().Len() != 3 {
		t.Fatalf("net/http.NewRequest signature = %v", newRequest.Type())
	}
	reader := namedType(signature.Params().At(2).Type())
	if reader == nil || reader.Obj() == nil || reader.Obj().Pkg() != ioPkg {
		t.Fatalf("net/http.NewRequest io.Reader identity = %p, want io package %p", packageOfNamed(reader), ioPkg)
	}
}

// TestPhase15_3CB3AModuleExportFileImporterContracts verifies the module-aware
// variant of the same idea. go/packages is used only to obtain the import graph
// and compiled ExportFile paths; go/importer then constructs the shared
// *types.Package universe from those export files without NeedTypes.
func TestPhase15_3CB3AModuleExportFileImporterContracts(t *testing.T) {
	root := t.TempDir()
	phase15CB3AWriteFile(t, filepath.Join(root, "go.mod"), `module example.com/project

go 1.24

require example.com/library v0.0.0
replace example.com/library => ./third_party/library
`)
	phase15CB3AWriteFile(t, filepath.Join(root, "model", "user.go"), `package model

type User struct { Name string }
func Name(user User) string { return user.Name }
`)
	phase15CB3AWriteFile(t, filepath.Join(root, "service", "service.go"), `package service

import "example.com/project/model"

func Current() model.User { return model.User{Name: "George"} }
`)
	phase15CB3AWriteFile(t, filepath.Join(root, "third_party", "library", "go.mod"), "module example.com/library\n\ngo 1.24\n")
	phase15CB3AWriteFile(t, filepath.Join(root, "third_party", "library", "value.go"), `package toolkit

type Value struct{ Name string }
func New() Value { return Value{Name: "ok"} }
`)

	graph, err := phase15CB3ALoadExportGraph(root,
		"example.com/project/service",
		"example.com/library",
	)
	if err != nil {
		t.Fatal(err)
	}
	imp := graph.importer()
	if imp == nil {
		t.Fatal("module export-data importer is nil")
	}

	service, err := imp.Import("example.com/project/service")
	if err != nil {
		t.Fatal(err)
	}
	model, err := imp.Import("example.com/project/model")
	if err != nil {
		t.Fatal(err)
	}
	library, err := imp.Import("example.com/library")
	if err != nil {
		t.Fatal(err)
	}
	if service == nil || model == nil || library == nil {
		t.Fatalf("module export-data imports missing: service=%p model=%p library=%p", service, model, library)
	}
	if !service.Complete() || !model.Complete() || !library.Complete() {
		t.Fatalf("module export-data package incomplete: service=%t model=%t library=%t", service.Complete(), model.Complete(), library.Complete())
	}
	if library.Name() != "toolkit" || library.Path() != "example.com/library" {
		t.Fatalf("replace dependency identity = %s %q, want toolkit example.com/library", library.Name(), library.Path())
	}
	if phase15CB2CurrentResultPackage(service) != model {
		t.Fatalf("service.Current result package = %p, want model %p", phase15CB2CurrentResultPackage(service), model)
	}
	serviceAgain, err := imp.Import("example.com/project/service")
	if err != nil {
		t.Fatal(err)
	}
	if serviceAgain != service {
		t.Fatalf("persistent module importer changed service identity: first=%p second=%p", service, serviceAgain)
	}

	closure, complete := phase15CB2TypesClosure(service)
	if len(closure) == 0 || complete == 0 {
		t.Fatalf("service export-data closure is empty or unusable: complete=%d closure=%d", complete, len(closure))
	}
}

// BenchmarkPhase15_3CB3AStdlibStrings compares the current source/type graph
// loading path against two export-data strategies for exactly the expensive
// standard-library case isolated in B2.
func BenchmarkPhase15_3CB3AStdlibStrings(b *testing.B) {
	root := b.TempDir()
	phase15CB3AWriteBenchmarkFile(b, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")

	b.Run("CurrentNeedDeps", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			loaded, err := packages.Load(&packages.Config{
				Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports | packages.NeedDeps,
				Dir:  root,
			}, "strings")
			if err != nil {
				b.Fatal(err)
			}
			pkg := phase15CB2FindLoadedPackage(loaded, "strings")
			if pkg == nil || pkg.Types == nil || !pkg.Types.Complete() {
				b.Fatalf("current strings package incomplete: %s", phase15CB2PackageErrors(loaded))
			}
			closure, complete := phase15CB2TypesClosure(pkg.Types)
			b.ReportMetric(float64(len(closure)), "closure-pkgs/op")
			b.ReportMetric(float64(complete), "closure-complete/op")
		}
	})

	b.Run("DefaultImporterCold", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			imp := importer.Default()
			pkg, err := imp.Import("strings")
			if err != nil {
				b.Fatal(err)
			}
			if pkg == nil || !pkg.Complete() {
				b.Fatalf("default importer strings package incomplete: %v", pkg)
			}
			closure, complete := phase15CB2TypesClosure(pkg)
			b.ReportMetric(float64(len(closure)), "closure-pkgs/op")
			b.ReportMetric(float64(complete), "closure-complete/op")
		}
	})

	b.Run("ExportFileGraphImporterCold", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			graph, err := phase15CB3ALoadExportGraph(root, "strings")
			if err != nil {
				b.Fatal(err)
			}
			pkg, err := graph.importer().Import("strings")
			if err != nil {
				b.Fatal(err)
			}
			if pkg == nil || !pkg.Complete() {
				b.Fatalf("export-file graph strings package incomplete: %v", pkg)
			}
			closure, complete := phase15CB2TypesClosure(pkg)
			b.ReportMetric(float64(len(closure)), "closure-pkgs/op")
			b.ReportMetric(float64(complete), "closure-complete/op")
		}
	})
}

// BenchmarkPhase15_3CB3AModuleService compares the current module-aware
// NeedTypes+NeedDeps path with a graph that requests only compiled ExportFile
// paths, then imports the same package universe from those artifacts.
func BenchmarkPhase15_3CB3AModuleService(b *testing.B) {
	root := b.TempDir()
	phase15CB3AWriteBenchmarkFile(b, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	phase15CB3AWriteBenchmarkFile(b, filepath.Join(root, "model", "user.go"), `package model

type User struct { Name string }
`)
	phase15CB3AWriteBenchmarkFile(b, filepath.Join(root, "service", "service.go"), `package service

import "example.com/project/model"

func Current() model.User { return model.User{} }
`)

	b.Run("CurrentNeedDeps", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			loaded, err := packages.Load(&packages.Config{
				Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports | packages.NeedDeps,
				Dir:  root,
			}, "example.com/project/service")
			if err != nil {
				b.Fatal(err)
			}
			pkg := phase15CB2FindLoadedPackage(loaded, "example.com/project/service")
			if pkg == nil || pkg.Types == nil || !pkg.Types.Complete() {
				b.Fatalf("current service package incomplete: %s", phase15CB2PackageErrors(loaded))
			}
			closure, complete := phase15CB2TypesClosure(pkg.Types)
			identityOK := phase15CB2CurrentResultPackage(pkg.Types) == closure["example.com/project/model"]
			b.ReportMetric(float64(len(closure)), "closure-pkgs/op")
			b.ReportMetric(float64(complete), "closure-complete/op")
			if identityOK {
				b.ReportMetric(1, "identity-ok/op")
			} else {
				b.ReportMetric(0, "identity-ok/op")
			}
		}
	})

	b.Run("ExportFileGraphOnly", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			graph, err := phase15CB3ALoadExportGraph(root, "example.com/project/service")
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(len(graph.files)), "export-files/op")
		}
	})

	b.Run("ExportFileGraphImporter", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			graph, err := phase15CB3ALoadExportGraph(root, "example.com/project/service")
			if err != nil {
				b.Fatal(err)
			}
			imp := graph.importer()
			service, err := imp.Import("example.com/project/service")
			if err != nil {
				b.Fatal(err)
			}
			model, err := imp.Import("example.com/project/model")
			if err != nil {
				b.Fatal(err)
			}
			identityOK := phase15CB2CurrentResultPackage(service) == model
			closure, complete := phase15CB2TypesClosure(service)
			b.ReportMetric(float64(len(graph.files)), "export-files/op")
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

// BenchmarkPhase15_3CB3APersistentImporterWarm measures the cost of repeated
// imports from one already-populated importer. This models the shared importer
// that B3 would keep at workspace/toolchain scope if the cold contracts pass.
func BenchmarkPhase15_3CB3APersistentImporterWarm(b *testing.B) {
	imp := importer.Default()
	first, err := imp.Import("strings")
	if err != nil {
		b.Fatal(err)
	}
	if first == nil || !first.Complete() {
		b.Fatalf("strings package incomplete: %v", first)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pkg, err := imp.Import("strings")
		if err != nil {
			b.Fatal(err)
		}
		if pkg != first {
			b.Fatalf("persistent importer identity changed: first=%p current=%p", first, pkg)
		}
	}
}

type phase15CB3AExportGraph struct {
	files map[string]string
}

func phase15CB3ALoadExportGraph(root string, patterns ...string) (*phase15CB3AExportGraph, error) {
	loaded, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps | packages.NeedExportFile,
		Dir:  root,
	}, patterns...)
	if err != nil {
		return nil, err
	}
	graph := &phase15CB3AExportGraph{files: make(map[string]string)}
	seen := make(map[*packages.Package]bool)
	var visit func(*packages.Package) error
	visit = func(pkg *packages.Package) error {
		if pkg == nil || seen[pkg] {
			return nil
		}
		seen[pkg] = true
		if pkg.PkgPath != "" && pkg.PkgPath != "unsafe" {
			if len(pkg.Errors) > 0 {
				return fmt.Errorf("package %s: %s", pkg.PkgPath, pkg.Errors[0].Msg)
			}
			if pkg.ExportFile == "" {
				return fmt.Errorf("package %s has no ExportFile", pkg.PkgPath)
			}
			graph.files[pkg.PkgPath] = pkg.ExportFile
		}
		for _, dependency := range pkg.Imports {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	for _, pkg := range loaded {
		if err := visit(pkg); err != nil {
			return nil, err
		}
	}
	if len(graph.files) == 0 {
		return nil, fmt.Errorf("no export files loaded for %v", patterns)
	}
	return graph, nil
}

func (g *phase15CB3AExportGraph) lookup(path string) (io.ReadCloser, error) {
	if path == "unsafe" {
		return nil, fmt.Errorf("unsafe does not have export data")
	}
	filename := g.files[path]
	if filename == "" {
		return nil, fmt.Errorf("export file for %q is not in graph", path)
	}
	return os.Open(filename)
}

func (g *phase15CB3AExportGraph) importer() types.Importer {
	if g == nil {
		return nil
	}
	return importer.ForCompiler(token.NewFileSet(), "gc", g.lookup)
}

func phase15CB3AWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func phase15CB3AWriteBenchmarkFile(b *testing.B, path, content string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
}
