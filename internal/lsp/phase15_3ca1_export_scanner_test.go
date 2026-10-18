package lsp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestPhase15_3CA1ScannerExtractsTopLevelExports(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "fixture.go")
	source := `package fixture

func Exported() {}
func private() {}
func Generic[T any](value T) T { return value }

type Receiver struct{}
func (Receiver) ExportedMethod() {}

type (
	ExportedStruct struct{ Field string }
	ExportedInterface interface{ Run() }
	ExportedAlias = string
	ExportedDefined string
	privateType struct{}
)

const (
	ExportedConst = iota
	privateConst
	ExportedInherited
)

var ExportedVar, ExportedVarTwo int
var privateVar int

func container() {
	var HiddenLocal int
	_ = HiddenLocal
	_ = func() { var HiddenNested int; _ = HiddenNested }
}
`
	if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := &packages.Package{Name: "fixture", PkgPath: "fixture", GoFiles: []string{filename}}

	scanned, err := goPackageExportNamesFromScanner(loaded, "fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	got := shallowExportSignature(scanned.Exports)
	want := []string{
		"Exported:func:false",
		"ExportedAlias:type:true",
		"ExportedConst:const:false",
		"ExportedDefined:type:false",
		"ExportedInherited:const:false",
		"ExportedInterface:interface:false",
		"ExportedStruct:struct:false",
		"ExportedVar:var:false",
		"ExportedVarTwo:var:false",
		"Generic:func:false",
		"Receiver:struct:false",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scanner exports = %#v, want %#v", got, want)
	}
}

func TestPhase15_3CA1ScannerPreservesGoxInternalMarkers(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "fixture.go")
	source := `package fixture

// gox:internal
// HiddenFunction is runtime-only.
func HiddenFunction() {}

func VisibleFunction() {}

// gox:internal
type HiddenType struct{}

type VisibleType struct{}

// gox:internal
const (
	HiddenConstA = 1
	HiddenConstB = 2
)

const (
	// gox:internal
	HiddenSpec = 3
	VisibleSpec = 4
)

var (
	HiddenTrailing int // gox:internal
	VisibleVar int
)
`
	if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := &packages.Package{Name: "fixture", PkgPath: "fixture", GoFiles: []string{filename}}

	astResult, err := goPackageExportNamesFromASTForTest(loaded, "fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	scannerResult, err := goPackageExportNamesFromScanner(loaded, "fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := shallowExportSignature(scannerResult.Exports), shallowExportSignature(astResult.Exports); !reflect.DeepEqual(got, want) {
		t.Fatalf("scanner internal-marker projection = %#v, AST = %#v", got, want)
	}
	got := shallowExportSignature(scannerResult.Exports)
	want := []string{
		"VisibleFunction:func:false",
		"VisibleSpec:const:false",
		"VisibleType:struct:false",
		"VisibleVar:var:false",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scanner public exports = %#v, want %#v", got, want)
	}
}

func TestPhase15_3CA1ScannerMatchesASTAcrossStandardLibrary(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{Mode: goGlobalExportFileMode}, "std")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) < 100 {
		t.Fatalf("stdlib package inventory unexpectedly small: %d", len(loaded))
	}

	packagesCompared := 0
	exportsCompared := 0
	for _, pkg := range loaded {
		if pkg == nil || pkg.PkgPath == "" || len(pkg.Errors) > 0 || len(pkg.GoFiles) == 0 {
			continue
		}
		astResult, astErr := goPackageExportNamesFromASTForTest(pkg, pkg.PkgPath, false)
		if astErr != nil {
			t.Fatalf("AST shallow exports for %s: %v", pkg.PkgPath, astErr)
		}
		scannerResult, scannerErr := goPackageExportNamesFromScanner(pkg, pkg.PkgPath, false)
		if scannerErr != nil {
			t.Fatalf("scanner shallow exports for %s: %v", pkg.PkgPath, scannerErr)
		}
		got := shallowExportSignature(scannerResult.Exports)
		want := shallowExportSignature(astResult.Exports)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("scanner/AST mismatch for %s\nscanner: %#v\nAST:     %#v", pkg.PkgPath, got, want)
		}
		packagesCompared++
		exportsCompared += len(want)
	}
	if packagesCompared < 100 {
		t.Fatalf("stdlib packages compared unexpectedly small: %d", packagesCompared)
	}
	if exportsCompared < 1000 {
		t.Fatalf("stdlib exports compared unexpectedly small: %d", exportsCompared)
	}
}

func shallowExportSignature(exports []GoPackageExport) []string {
	result := make([]string, 0, len(exports))
	for _, exported := range exports {
		result = append(result, exported.Name+":"+exported.Kind+":"+boolText(exported.Alias))
	}
	sort.Strings(result)
	return result
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// goPackageExportNamesFromASTForTest is the former production shallow AST
// extractor retained only as an oracle for scanner equivalence tests and
// benchmarks. Production uses goPackageExportNamesFromScanner.
func goPackageExportNamesFromASTForTest(loaded *packages.Package, importPath string, preserveInternalMarkers bool) (GoPackage, error) {
	if loaded == nil {
		return GoPackage{}, fmt.Errorf("Go package %q could not be loaded", importPath)
	}
	if len(loaded.GoFiles) == 0 {
		return GoPackage{}, fmt.Errorf("Go package %q has no source files", importPath)
	}

	fset := token.NewFileSet()
	exports := make(map[string]GoPackageExport)
	var firstParseErr error
	for _, filename := range loaded.GoFiles {
		mode := parser.SkipObjectResolution
		if preserveInternalMarkers {
			mode |= parser.ParseComments
		}
		file, err := parser.ParseFile(fset, filename, nil, mode)
		if err != nil {
			if firstParseErr == nil {
				firstParseErr = err
			}
			continue
		}
		var internal map[token.Pos]struct{}
		if preserveInternalMarkers {
			internal = goInternalAPIObjects([]*ast.File{file})
		}
		goSourceFileExportNamesForTest(file, internal, exports)
	}
	if len(exports) == 0 && firstParseErr != nil {
		return GoPackage{}, firstParseErr
	}

	result := make([]GoPackageExport, 0, len(exports))
	for _, exported := range exports {
		result = append(result, exported)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return GoPackage{Path: importPath, Name: loaded.Name, Exports: result}, nil
}

func goSourceFileExportNamesForTest(file *ast.File, internal map[token.Pos]struct{}, exports map[string]GoPackageExport) {
	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			if value.Recv == nil && value.Name != nil && token.IsExported(value.Name.Name) && !goSourcePositionIsInternal(value.Name.Pos(), internal) {
				exports[value.Name.Name] = GoPackageExport{Name: value.Name.Name, Kind: "func"}
			}
		case *ast.GenDecl:
			for _, raw := range value.Specs {
				switch specification := raw.(type) {
				case *ast.TypeSpec:
					if specification.Name == nil || !token.IsExported(specification.Name.Name) || goSourcePositionIsInternal(specification.Name.Pos(), internal) {
						continue
					}
					kind := "type"
					switch specification.Type.(type) {
					case *ast.StructType:
						kind = "struct"
					case *ast.InterfaceType:
						kind = "interface"
					}
					exports[specification.Name.Name] = GoPackageExport{Name: specification.Name.Name, Kind: kind, Alias: specification.Assign.IsValid()}
				case *ast.ValueSpec:
					kind := "var"
					if value.Tok == token.CONST {
						kind = "const"
					}
					for _, name := range specification.Names {
						if name != nil && token.IsExported(name.Name) && !goSourcePositionIsInternal(name.Pos(), internal) {
							exports[name.Name] = GoPackageExport{Name: name.Name, Kind: kind}
						}
					}
				}
			}
		}
	}
}
