package gox

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAnalyzeResolvesDirectAndTransitiveProjectGoPackages(t *testing.T) {
	root := t.TempDir()
	writeGoPackageGraphTestFile(t, filepath.Join(root, "go.mod"), "module noti-milo\n\ngo 1.24\n")
	entry := filepath.Join(root, "pages", "index.gox")
	writeGoPackageGraphTestFile(t, entry, `module index

import "noti-milo/billing"

func Value() string { return billing.Value() }
`)
	writeGoPackageGraphTestFile(t, filepath.Join(root, "billing", "billing.go"), `package billing

import "noti-milo/database"

func Value() string { return database.Value }
`)
	writeGoPackageGraphTestFile(t, filepath.Join(root, "billing", "billing_test.go"), "package billing\n")
	writeGoPackageGraphTestFile(t, filepath.Join(root, "database", "database.go"), "package database\n\nconst Value = \"ok\"\n")

	result, err := Analyze(entry)
	if err != nil {
		t.Fatal(err)
	}

	want := []CompileGoPackage{
		{
			ImportPath: "noti-milo/billing",
			Dir:        filepath.Join(root, "billing"),
			Files:      []string{filepath.Join(root, "billing", "billing.go")},
			Imports:    []string{"noti-milo/database"},
			ImportedBy: []string{entry},
			Direct:     true,
		},
		{
			ImportPath: "noti-milo/database",
			Dir:        filepath.Join(root, "database"),
			Files:      []string{filepath.Join(root, "database", "database.go")},
			Imports:    []string{},
			ImportedBy: []string{"noti-milo/billing"},
			Direct:     false,
		},
	}
	if !reflect.DeepEqual(result.GoPackages, want) {
		t.Fatalf("GoPackages mismatch:\n got: %#v\nwant: %#v", result.GoPackages, want)
	}
}

func TestAnalyzeResolvesCyclicProjectGoPackageGraphOnce(t *testing.T) {
	root := t.TempDir()
	writeGoPackageGraphTestFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n")
	entry := filepath.Join(root, "pages", "index.gox")
	writeGoPackageGraphTestFile(t, entry, "module index\n\nimport \"example.com/project/first\"\n")
	writeGoPackageGraphTestFile(t, filepath.Join(root, "first", "first.go"), "package first\n\nimport \"example.com/project/second\"\n")
	writeGoPackageGraphTestFile(t, filepath.Join(root, "second", "second.go"), "package second\n\nimport \"example.com/project/first\"\n")

	result, err := Analyze(entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.GoPackages) != 2 {
		t.Fatalf("GoPackages count = %d, want 2: %#v", len(result.GoPackages), result.GoPackages)
	}
	if !result.GoPackages[0].Direct || result.GoPackages[1].Direct {
		t.Fatalf("direct package markers are incorrect: %#v", result.GoPackages)
	}
	if !reflect.DeepEqual(result.GoPackages[0].ImportedBy, []string{entry, "example.com/project/second"}) {
		t.Fatalf("first ImportedBy = %#v", result.GoPackages[0].ImportedBy)
	}
}

func writeGoPackageGraphTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
