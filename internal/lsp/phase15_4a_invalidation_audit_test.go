package lsp

import (
	"go/types"
	"os"
	"path/filepath"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestPhase15_4AGoxDependencyInvalidationIsDocumentGranular(t *testing.T) {
	root := t.TempDir()
	cardPath := filepath.Join(root, "card.gox")
	pagePath := filepath.Join(root, "page.gox")
	unrelatedPath := filepath.Join(root, "unrelated.gox")

	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		module := &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{}}
		if samePath(document.Path, pagePath) {
			module.Semantic.Dependencies = []goxcompiler.SemanticDependency{{
				SourcePath:   pagePath,
				Kind:         goxcompiler.SemanticDependencyGoxModule,
				ImportPath:   "card.gox",
				ResolvedPath: cardPath,
			}}
		}
		return module, nil
	})

	card := Document{URI: FileURI(cardPath), Path: cardPath, Version: 1, Text: "module card\n"}
	page := Document{URI: FileURI(pagePath), Path: pagePath, Version: 1, Text: "module page\n"}
	unrelated := Document{URI: FileURI(unrelatedPath), Path: unrelatedPath, Version: 1, Text: "module unrelated\n"}
	store.Snapshot(root, card)
	store.Snapshot(root, page)
	store.Snapshot(root, unrelated)

	invalidated := store.InvalidateDependencyPathExcept(cardPath, card.URI)
	if len(invalidated) != 1 || invalidated[0] != page.URI {
		t.Fatalf("GOX dependency invalidation = %#v, want only %q", invalidated, page.URI)
	}
	if _, ok := store.Current(card.URI); !ok {
		t.Fatal("changed open GOX document lost its canonical snapshot before its replacement version is analyzed")
	}
	if _, ok := store.Current(page.URI); ok {
		t.Fatal("GOX dependent snapshot survived dependency invalidation")
	}
	if _, ok := store.Current(unrelated.URI); !ok {
		t.Fatal("unrelated GOX snapshot was invalidated")
	}
}

func TestPhase15_4AGoChangeInvalidatesDependentSnapshotsButRotatesSemanticRoot(t *testing.T) {
	rootOne := phase15_4AProjectRoot(t, "one")
	rootTwo := phase15_4AProjectRoot(t, "two")
	changed := filepath.Join(rootOne, "domain", "user.go")
	if err := os.MkdirAll(filepath.Dir(changed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(changed, []byte("package domain\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	packagesByURI := map[string]string{}
	server := NewServer()
	server.analysis = newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		packagePath := packagesByURI[document.URI]
		module := &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{}}
		if packagePath != "" {
			module.Semantic.Dependencies = []goxcompiler.SemanticDependency{{
				SourcePath:  document.Path,
				Kind:        goxcompiler.SemanticDependencyGoPackage,
				ImportPath:  packagePath,
				PackagePath: packagePath,
			}}
		}
		return module, nil
	})
	server.analysis.semanticCache = goxcompiler.NewSemanticWorkspaceCache()

	oneA := phase15_4ASnapshot(t, server, rootOne, "a.gox")
	oneB := phase15_4ASnapshot(t, server, rootOne, "b.gox")
	two := phase15_4ASnapshot(t, server, rootTwo, "c.gox")
	packagesByURI[oneA.URI] = "example.com/one/domain"
	packagesByURI[oneB.URI] = "example.com/one/other"
	packagesByURI[two.URI] = "example.com/two/domain"
	// Rebuild snapshots now that the fixture dependency map is populated.
	server.analysis.InvalidateURI(oneA.URI)
	server.analysis.InvalidateURI(oneB.URI)
	server.analysis.InvalidateURI(two.URI)
	server.analysis.Snapshot(rootOne, oneA)
	server.analysis.Snapshot(rootOne, oneB)
	server.analysis.Snapshot(rootTwo, two)

	phase15_4ASeedGoIndexRoot(server.goPackages, rootOne, "example.com/one/pkg", "pkg")
	phase15_4ASeedGoIndexRoot(server.goPackages, rootTwo, "example.com/two/pkg", "pkg")
	semanticPackage := types.NewPackage("example.com/one/domain", "domain")
	server.analysis.semanticCache.StoreGoPackage(rootOne, "example.com/one/domain", semanticPackage)
	if cached, ok := server.analysis.semanticCache.GoPackage(rootOne, "example.com/one/domain"); !ok || cached != semanticPackage {
		t.Fatalf("failed to seed semantic Go package identity: cached=%p ok=%v want=%p", cached, ok, semanticPackage)
	}

	if !server.handleWorkspaceFileChange(FileURI(changed), fileChanged) {
		t.Fatal(".go change was not classified as Go-package-affecting")
	}

	if _, ok := server.analysis.Current(oneA.URI); ok {
		t.Fatal("snapshot depending on changed Go package survived invalidation")
	}
	if _, ok := server.analysis.Current(oneB.URI); !ok {
		t.Fatal("snapshot with disjoint Go package dependency was invalidated")
	}
	if _, ok := server.analysis.Current(two.URI); !ok {
		t.Fatal("snapshot in unrelated root was invalidated by .go change")
	}
	phase15_4AAssertGoIndexRootPreserved(t, server.goPackages, rootOne, "example.com/one/pkg")
	phase15_4AAssertGoIndexRootPreserved(t, server.goPackages, rootTwo, "example.com/two/pkg")
	if _, ok := server.analysis.semanticCache.GoPackage(rootOne, "example.com/one/domain"); ok {
		t.Fatal("semantic Go package identity survived root-wide importer rotation")
	}

	t.Log("audit update: local .go changes now invalidate only dependent GOX snapshots while the semantic importer universe still rotates for the entire project root")
}

func TestPhase15_4AModuleMetadataInvalidationScopes(t *testing.T) {
	for _, name := range []string{"go.mod", "go.sum"} {
		t.Run(name, func(t *testing.T) {
			rootOne := phase15_4AProjectRoot(t, "one")
			rootTwo := phase15_4AProjectRoot(t, "two")
			server := phase15_4AServerWithSyntheticAnalysis()
			one := phase15_4ASnapshot(t, server, rootOne, "one.gox")
			two := phase15_4ASnapshot(t, server, rootTwo, "two.gox")
			phase15_4ASeedGoIndexRoot(server.goPackages, rootOne, "example.com/one/pkg", "pkg")
			phase15_4ASeedGoIndexRoot(server.goPackages, rootTwo, "example.com/two/pkg", "pkg")

			if !server.handleWorkspaceFileChange(FileURI(filepath.Join(rootOne, name)), fileChanged) {
				t.Fatalf("%s change was not classified as Go-package-affecting", name)
			}
			if _, ok := server.analysis.Current(one.URI); ok {
				t.Fatalf("affected root analysis survived %s change", name)
			}
			if _, ok := server.analysis.Current(two.URI); !ok {
				t.Fatalf("unrelated root analysis was invalidated by %s change", name)
			}
			phase15_4AAssertGoIndexRootDropped(t, server.goPackages, rootOne, "example.com/one/pkg")
			phase15_4AAssertGoIndexRootPreserved(t, server.goPackages, rootTwo, "example.com/two/pkg")
		})
	}
}

func TestPhase15_4AGoWorkChangeInvalidatesAllRoots(t *testing.T) {
	rootOne := phase15_4AProjectRoot(t, "one")
	rootTwo := phase15_4AProjectRoot(t, "two")
	server := phase15_4AServerWithSyntheticAnalysis()
	one := phase15_4ASnapshot(t, server, rootOne, "one.gox")
	two := phase15_4ASnapshot(t, server, rootTwo, "two.gox")
	phase15_4ASeedGoIndexRoot(server.goPackages, rootOne, "example.com/one/pkg", "pkg")
	phase15_4ASeedGoIndexRoot(server.goPackages, rootTwo, "example.com/two/pkg", "pkg")

	if !server.handleWorkspaceFileChange(FileURI(filepath.Join(t.TempDir(), "go.work")), fileChanged) {
		t.Fatal("go.work change was not classified as Go-package-affecting")
	}
	if _, ok := server.analysis.Current(one.URI); ok {
		t.Fatal("first root analysis survived go.work invalidation")
	}
	if _, ok := server.analysis.Current(two.URI); ok {
		t.Fatal("second root analysis survived go.work invalidation")
	}
	if len(server.goPackages.discovery) != 0 || len(server.goPackages.exports) != 0 || len(server.goPackages.exportSources) != 0 || len(server.goPackages.exportDetails) != 0 || len(server.goPackages.cache) != 0 || len(server.goPackages.packageNames) != 0 {
		t.Fatal("go.work invalidation preserved one or more Go package index layers")
	}
}

func TestPhase15_4ANonGoWorkspaceFileDoesNotInvalidateGoState(t *testing.T) {
	root := phase15_4AProjectRoot(t, "plain")
	server := phase15_4AServerWithSyntheticAnalysis()
	document := phase15_4ASnapshot(t, server, root, "page.gox")
	phase15_4ASeedGoIndexRoot(server.goPackages, root, "example.com/plain/pkg", "pkg")

	if server.handleWorkspaceFileChange(FileURI(filepath.Join(root, "README.md")), fileChanged) {
		t.Fatal("non-Go workspace file was classified as Go-package-affecting")
	}
	if _, ok := server.analysis.Current(document.URI); !ok {
		t.Fatal("non-Go workspace file invalidated semantic analysis")
	}
	phase15_4AAssertGoIndexRootPreserved(t, server.goPackages, root, "example.com/plain/pkg")
}

func phase15_4AProjectRoot(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/"+name+"\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func phase15_4AServerWithSyntheticAnalysis() *Server {
	server := NewServer()
	server.analysis = newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{}}, nil
	})
	// newAnalysisStore intentionally leaves semanticCache nil because it is the
	// low-level constructor used by unit tests. The real server store owns a
	// SemanticWorkspaceCache, so the Phase 15 invalidation fixture must install
	// one as well or semantic-cache preservation/invalidation assertions are
	// vacuous.
	server.analysis.semanticCache = goxcompiler.NewSemanticWorkspaceCache()
	return server
}

func phase15_4ASnapshot(t *testing.T, server *Server, root, name string) Document {
	t.Helper()
	path := filepath.Join(root, name)
	document := Document{URI: FileURI(path), Path: path, Version: 1, Text: "module page\n"}
	if snapshot := server.analysis.Snapshot(root, document); snapshot == nil {
		t.Fatalf("failed to seed analysis snapshot for %s", path)
	}
	return document
}

func phase15_4ASeedGoIndexRoot(index *GoPackageIndex, root, importPath, packageName string) {
	key := filepath.Clean(root) + "\x00" + importPath
	index.discovery[filepath.Clean(root)] = []GoPackageCandidate{{Path: importPath, Name: packageName}}
	index.exports[filepath.Clean(root)] = []GoPackage{{Path: importPath, Name: packageName}}
	index.exportSources[filepath.Clean(root)] = map[string]goGlobalPackageSource{
		importPath: {Name: packageName, Files: []string{filepath.Join(root, "pkg.go")}},
	}
	index.exportDetails[key] = GoPackage{Path: importPath, Name: packageName}
	index.cache[key] = goPackageCacheEntry{packageInfo: GoPackage{Path: importPath, Name: packageName}}
	index.packageNames[key] = packageName
}

func phase15_4AAssertGoIndexRootDropped(t *testing.T, index *GoPackageIndex, root, importPath string) {
	t.Helper()
	root = filepath.Clean(root)
	key := root + "\x00" + importPath
	if _, ok := index.discovery[root]; ok {
		t.Fatal("discovery cache survived root invalidation")
	}
	if _, ok := index.exports[root]; ok {
		t.Fatal("shallow export cache survived root invalidation")
	}
	if _, ok := index.exportSources[root]; ok {
		t.Fatal("export source manifest survived root invalidation")
	}
	if _, ok := index.exportDetails[key]; ok {
		t.Fatal("detailed export cache survived root invalidation")
	}
	if _, ok := index.cache[key]; ok {
		t.Fatal("typed/source package cache survived root invalidation")
	}
	if _, ok := index.packageNames[key]; ok {
		t.Fatal("package-name cache survived root invalidation")
	}
}

func phase15_4AAssertGoIndexRootPreserved(t *testing.T, index *GoPackageIndex, root, importPath string) {
	t.Helper()
	root = filepath.Clean(root)
	key := root + "\x00" + importPath
	if _, ok := index.discovery[root]; !ok {
		t.Fatal("discovery cache for unrelated root was invalidated")
	}
	if _, ok := index.exports[root]; !ok {
		t.Fatal("shallow export cache for unrelated root was invalidated")
	}
	if _, ok := index.exportSources[root]; !ok {
		t.Fatal("export source manifest for unrelated root was invalidated")
	}
	if _, ok := index.exportDetails[key]; !ok {
		t.Fatal("detailed export cache for unrelated root was invalidated")
	}
	if _, ok := index.cache[key]; !ok {
		t.Fatal("typed/source package cache for unrelated root was invalidated")
	}
	if _, ok := index.packageNames[key]; !ok {
		t.Fatal("package-name cache for unrelated root was invalidated")
	}
}
