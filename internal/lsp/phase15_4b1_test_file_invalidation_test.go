package lsp

import (
	"go/types"
	"os"
	"path/filepath"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestPhase15_4B1TestOnlyGoChangesPreserveProductionCaches(t *testing.T) {
	for _, changeType := range []int{fileChanged, fileCreated, fileDeleted} {
		t.Run(phase15_4B1ChangeTypeName(changeType), func(t *testing.T) {
			root := phase15_4AProjectRoot(t, "test-only")
			server := phase15_4AServerWithSyntheticAnalysis()
			document := phase15_4ASnapshot(t, server, root, "page.gox")
			importPath := "example.com/testonly/pkg"
			phase15_4ASeedGoIndexRoot(server.goPackages, root, importPath, "pkg")

			semanticPackage := types.NewPackage(importPath, "pkg")
			server.analysis.semanticCache.StoreGoPackage(root, importPath, semanticPackage)
			if cached, ok := server.analysis.semanticCache.GoPackage(root, importPath); !ok || cached != semanticPackage {
				t.Fatalf("failed to seed semantic Go package identity: cached=%p ok=%v want=%p", cached, ok, semanticPackage)
			}

			path := filepath.Join(root, "domain", "user_test.go")
			if server.handleWorkspaceFileChange(FileURI(path), changeType) {
				t.Fatal("_test.go change was classified as production Go-package-affecting")
			}

			if current, ok := server.analysis.Current(document.URI); !ok || current == nil {
				t.Fatal("_test.go change invalidated GOX analysis")
			}
			phase15_4B1AssertGoIndexRootPreserved(t, server.goPackages, root, importPath)
			if cached, ok := server.analysis.semanticCache.GoPackage(root, importPath); !ok || cached != semanticPackage {
				t.Fatalf("_test.go change invalidated semantic Go package identity: cached=%p ok=%v want=%p", cached, ok, semanticPackage)
			}
		})
	}
}

func TestPhase15_4B1ProductionGoChangeStillInvalidatesProductionCaches(t *testing.T) {
	root := phase15_4AProjectRoot(t, "production")
	path := filepath.Join(root, "domain", "user.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package domain\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	packagePath := "example.com/production/domain"
	server := NewServer()
	server.analysis = newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
			SourcePath:  document.Path,
			Kind:        goxcompiler.SemanticDependencyGoPackage,
			ImportPath:  packagePath,
			PackagePath: packagePath,
		}}}}, nil
	})
	server.analysis.semanticCache = goxcompiler.NewSemanticWorkspaceCache()
	document := phase15_4ASnapshot(t, server, root, "page.gox")
	// Seed an unrelated package-index entry: B2 keeps unrelated index layers
	// warm while the changed package itself is invalidated separately.
	phase15_4ASeedGoIndexRoot(server.goPackages, root, "example.com/production/pkg", "pkg")

	semanticPackage := types.NewPackage(packagePath, "domain")
	server.analysis.semanticCache.StoreGoPackage(root, packagePath, semanticPackage)
	if cached, ok := server.analysis.semanticCache.GoPackage(root, packagePath); !ok || cached != semanticPackage {
		t.Fatalf("failed to seed semantic Go package identity: cached=%p ok=%v want=%p", cached, ok, semanticPackage)
	}

	if !server.handleWorkspaceFileChange(FileURI(path), fileChanged) {
		t.Fatal("ordinary .go change stopped being classified as Go-package-affecting")
	}
	if _, ok := server.analysis.Current(document.URI); ok {
		t.Fatal("ordinary .go change preserved stale dependent GOX analysis")
	}
	phase15_4AAssertGoIndexRootPreserved(t, server.goPackages, root, "example.com/production/pkg")
	if _, ok := server.analysis.semanticCache.GoPackage(root, packagePath); ok {
		t.Fatal("ordinary .go change preserved stale semantic Go package identity")
	}
}

func TestPhase15_4B1GoDependencyClassificationExcludesOnlyTestGoFiles(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name string
		want bool
	}{
		{name: "plain.go", want: true},
		{name: "UPPER.GO", want: true},
		{name: "plain_test.go", want: false},
		{name: "UPPER_TEST.GO", want: false},
		{name: "go.mod", want: true},
		{name: "go.sum", want: true},
		{name: "go.work", want: true},
		{name: "README.md", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := goDependencyFileURI(FileURI(filepath.Join(root, tc.name)))
			if got != tc.want {
				t.Fatalf("goDependencyFileURI(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func phase15_4B1AssertGoIndexRootPreserved(t *testing.T, index *GoPackageIndex, root, importPath string) {
	t.Helper()
	root = filepath.Clean(root)
	key := root + "\x00" + importPath
	if _, ok := index.discovery[root]; !ok {
		t.Fatal("test-only change invalidated discovery cache")
	}
	if _, ok := index.exports[root]; !ok {
		t.Fatal("test-only change invalidated shallow export cache")
	}
	if _, ok := index.exportSources[root]; !ok {
		t.Fatal("test-only change invalidated export source manifest")
	}
	if _, ok := index.exportDetails[key]; !ok {
		t.Fatal("test-only change invalidated detailed export cache")
	}
	if _, ok := index.cache[key]; !ok {
		t.Fatal("test-only change invalidated typed/source package cache")
	}
	if _, ok := index.packageNames[key]; !ok {
		t.Fatal("test-only change invalidated package-name cache")
	}
}

func phase15_4B1ChangeTypeName(changeType int) string {
	switch changeType {
	case fileChanged:
		return "changed"
	case fileCreated:
		return "created"
	case fileDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}
