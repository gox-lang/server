package hotreload

import (
	"path/filepath"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestRegisterCompileResultIndexesGoPackageFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "pages", "index.gox")
	billingDir := filepath.Join(root, "billing")
	databaseDir := filepath.Join(root, "database")
	index := NewIndex()
	index.RegisterCompileResult(ReloadPage, &goxcompiler.CompileResult{
		EntryPath:    entry,
		EntryAbsPath: entry,
		Dependencies: []goxcompiler.CompileDependency{{AbsPath: entry, IsEntry: true}},
		GoPackages: []goxcompiler.CompileGoPackage{
			{
				ImportPath: "noti-milo/billing",
				Dir:        billingDir,
				Files:      []string{filepath.Join(billingDir, "billing.go")},
				Direct:     true,
			},
			{
				ImportPath: "noti-milo/database",
				Dir:        databaseDir,
				Files:      []string{filepath.Join(databaseDir, "database.go")},
			},
		},
	})

	paths := []string{
		filepath.Join(billingDir, "billing.go"),
		filepath.Join(databaseDir, "database.go"),
		filepath.Join(billingDir, "new_invoice.go"),
		filepath.Join(databaseDir, "new_query.go"),
	}
	for _, path := range paths {
		dependents, ok := index.Dependents(path)
		if !ok || len(dependents.Entries) != 1 || dependents.Entries[0].EntryAbsPath != entry {
			t.Fatalf("Dependents(%q) = %#v, %v", path, dependents, ok)
		}
		if dependents.FileAbsPath != path {
			t.Fatalf("Dependents(%q).FileAbsPath = %q", path, dependents.FileAbsPath)
		}
	}
}

func TestGoPackageIndexDoesNotReturnUnrelatedEntries(t *testing.T) {
	root := t.TempDir()
	index := NewIndex()
	register := func(page, packageName string) {
		entry := filepath.Join(root, "pages", page+".gox")
		packageDir := filepath.Join(root, packageName)
		index.RegisterCompileResult(ReloadPage, &goxcompiler.CompileResult{
			EntryPath:    entry,
			EntryAbsPath: entry,
			Dependencies: []goxcompiler.CompileDependency{{AbsPath: entry, IsEntry: true}},
			GoPackages: []goxcompiler.CompileGoPackage{{
				ImportPath: "noti-milo/" + packageName,
				Dir:        packageDir,
				Files:      []string{filepath.Join(packageDir, packageName+".go")},
			}},
		})
	}
	register("index", "billing")
	register("admin", "admin")

	dependents, ok := index.Dependents(filepath.Join(root, "billing", "invoice.go"))
	if !ok || len(dependents.Entries) != 1 {
		t.Fatalf("dependents = %#v, %v", dependents, ok)
	}
	if filepath.Base(dependents.Entries[0].EntryAbsPath) != "index.gox" {
		t.Fatalf("unrelated entry returned: %#v", dependents.Entries)
	}
}

func TestRegisterCompileResultReplacesPreviousGoPackageIndex(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "pages", "index.gox")
	billingDir := filepath.Join(root, "billing")
	profileDir := filepath.Join(root, "profile")
	index := NewIndex()
	register := func(importPath, dir string) {
		index.RegisterCompileResult(ReloadPage, &goxcompiler.CompileResult{
			EntryPath:    entry,
			EntryAbsPath: entry,
			Dependencies: []goxcompiler.CompileDependency{{AbsPath: entry, IsEntry: true}},
			GoPackages: []goxcompiler.CompileGoPackage{{
				ImportPath: importPath,
				Dir:        dir,
				Files:      []string{filepath.Join(dir, "package.go")},
			}},
		})
	}

	register("noti-milo/billing", billingDir)
	register("noti-milo/profile", profileDir)

	if dependents, ok := index.Dependents(filepath.Join(billingDir, "new.go")); ok {
		t.Fatalf("old package remained indexed: %#v", dependents)
	}
	dependents, ok := index.Dependents(filepath.Join(profileDir, "new.go"))
	if !ok || len(dependents.Entries) != 1 || dependents.Entries[0].EntryAbsPath != entry {
		t.Fatalf("replacement package was not indexed: %#v, %v", dependents, ok)
	}
}
