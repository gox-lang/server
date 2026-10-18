package gox

import (
	"reflect"
	"testing"
)

func TestCompileResultRepresentsLocalGoPackageGraph(t *testing.T) {
	result := CompileResult{
		GoPackages: []CompileGoPackage{
			{
				ImportPath: "noti-milo/billing",
				Dir:        "/project/billing",
				Files:      []string{"/project/billing/billing.go"},
				Imports:    []string{"noti-milo/database"},
				ImportedBy: []string{"/project/pages/index.gox"},
				Direct:     true,
			},
			{
				ImportPath: "noti-milo/database",
				Dir:        "/project/database",
				Files:      []string{"/project/database/database.go", "/project/database/query.go"},
				ImportedBy: []string{"noti-milo/billing"},
				Direct:     false,
			},
		},
	}

	want := []CompileGoPackage{
		{
			ImportPath: "noti-milo/billing",
			Dir:        "/project/billing",
			Files:      []string{"/project/billing/billing.go"},
			Imports:    []string{"noti-milo/database"},
			ImportedBy: []string{"/project/pages/index.gox"},
			Direct:     true,
		},
		{
			ImportPath: "noti-milo/database",
			Dir:        "/project/database",
			Files:      []string{"/project/database/database.go", "/project/database/query.go"},
			ImportedBy: []string{"noti-milo/billing"},
			Direct:     false,
		},
	}

	if !reflect.DeepEqual(result.GoPackages, want) {
		t.Fatalf("GoPackages mismatch:\n got: %#v\nwant: %#v", result.GoPackages, want)
	}
	if result.GoPackages[0].ImportPath == result.GoPackages[0].Files[0] {
		t.Fatal("package import path must remain distinct from its physical Go files")
	}
}
