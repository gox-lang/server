package lsp

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestPhase15_3LightweightExportProjectionPreservesCompletionMetadata(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{Mode: goGlobalExportFileMode}, "strings")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("strings packages = %d", len(loaded))
	}
	if len(loaded[0].Errors) > 0 {
		t.Fatalf("strings package errors = %#v", loaded[0].Errors)
	}
	packageInfo, err := goPackageExportsFromSource(loaded[0], "strings", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, exported := range packageInfo.Exports {
		if exported.Name != "Split" {
			continue
		}
		if exported.Kind != "func" || exported.Detail == "" || exported.Type != "[]string" || len(exported.Results) != 1 || len(exported.Parameters) != 2 {
			t.Fatalf("lightweight strings.Split metadata = %#v", exported)
		}
		if exported.Parameters[0].Name != "s" || exported.Parameters[0].Type != "string" || exported.Parameters[1].Name != "sep" || exported.Parameters[1].Type != "string" {
			t.Fatalf("lightweight strings.Split parameters = %#v", exported.Parameters)
		}
		if exported.Definition != (GoDefinition{}) || len(exported.Members) != 0 {
			t.Fatalf("lightweight export retained syntax-only data = %#v", exported)
		}
		return
	}
	t.Fatal("lightweight strings export index is missing Split")
}

func TestPhase15_3LightweightExportProjectionPreservesSourceKinds(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture")
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(fixture, "fixture.go"), `package fixture

type Alias = string
type Record struct{ Value string }
type Reader interface{ Read() error }

const Answer = 42
var Enabled = true

func Join(parts ...string) string { return "" }
`)

	loaded, err := packages.Load(&packages.Config{Mode: goGlobalExportFileMode, Dir: root}, "./fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Errors) > 0 {
		t.Fatalf("fixture packages = %#v", loaded)
	}
	packageInfo, err := goPackageExportsFromSource(loaded[0], "example/fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	exports := make(map[string]GoPackageExport, len(packageInfo.Exports))
	for _, exported := range packageInfo.Exports {
		exports[exported.Name] = exported
	}
	if alias := exports["Alias"]; alias.Kind != "type" || !alias.Alias || alias.Underlying != "string" {
		t.Fatalf("Alias export = %#v", alias)
	}
	if record := exports["Record"]; record.Kind != "struct" || record.Underlying == "" {
		t.Fatalf("Record export = %#v", record)
	}
	if reader := exports["Reader"]; reader.Kind != "interface" || reader.Underlying == "" {
		t.Fatalf("Reader export = %#v", reader)
	}
	if answer := exports["Answer"]; answer.Kind != "const" || answer.Value != "42" || answer.Type != "untyped int" {
		t.Fatalf("Answer export = %#v", answer)
	}
	if enabled := exports["Enabled"]; enabled.Kind != "var" || enabled.Type != "untyped bool" {
		t.Fatalf("Enabled export = %#v", enabled)
	}
	if join := exports["Join"]; join.Kind != "func" || join.Type != "string" || len(join.Results) != 1 || len(join.Parameters) != 1 || join.Parameters[0].Name != "parts" || join.Parameters[0].Type != "...string" {
		t.Fatalf("Join export = %#v", join)
	}
}

func TestPhase15_3GlobalExportIndexKeepsGoxInternalMarkers(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), `module example

go 1.24

require github.com/gox-lang/gox v0.0.0
replace github.com/gox-lang/gox => ./sdk
`)
	writeGoPackageHoverFile(t, filepath.Join(sdk, "go.mod"), "module github.com/gox-lang/gox\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(sdk, "gox.go"), `package gox

type Component interface{}

// gox:internal
type RuntimeBridge interface{}

func Render() Component { return nil }

// gox:internal
func BindRuntime() {}
`)
	index := NewGoPackageIndex()
	packages, err := index.ExportedPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	exports := map[string]bool{}
	for _, packageInfo := range packages {
		if packageInfo.Path != goxSDKImportPath {
			continue
		}
		for _, exported := range packageInfo.Exports {
			exports[exported.Name] = true
		}
		break
	}
	for _, public := range []string{"Component", "Render"} {
		if !exports[public] {
			t.Errorf("GOX SDK global export index missing %q", public)
		}
	}
	for _, internal := range []string{"RuntimeBridge", "BindRuntime"} {
		if exports[internal] {
			t.Errorf("GOX SDK global export index exposed // gox:internal declaration %q", internal)
		}
	}
}
