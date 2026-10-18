package lsp

import (
	"path/filepath"
	"sync"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestPhase15_4C2CSemanticRotationRejectsInflightSnapshotPublication(t *testing.T) {
	root := t.TempDir()
	packagePath := "example.com/app/model"
	pagePath := filepath.Join(root, "pages", "index.gox")
	document := Document{URI: FileURI(pagePath), Path: pagePath, Version: 1, Text: "module page\n"}

	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	builds := 0
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		mu.Lock()
		builds++
		build := builds
		mu.Unlock()
		if build == 1 {
			close(started)
			<-release
		}
		return phase15_4C2CModuleWithGoPackageDependency(packagePath), nil
	})
	store.semanticCache = goxcompiler.NewSemanticWorkspaceCache()

	done := make(chan *AnalysisSnapshot, 1)
	go func() { done <- store.Snapshot(root, document) }()
	<-started

	if invalidated := store.InvalidateGoPackages(root, []string{packagePath}); len(invalidated) != 0 {
		t.Fatalf("in-flight snapshot unexpectedly appeared in reverse index: invalidated=%v", invalidated)
	}
	close(release)
	if snapshot := <-done; snapshot == nil || snapshot.Module == nil {
		t.Fatalf("caller lost completed pre-rotation result: %#v", snapshot)
	}
	if _, ok := store.Current(document.URI); ok {
		t.Fatal("pre-rotation in-flight snapshot repopulated current cache")
	}
	store.mu.RLock()
	_, keptLastGood := store.lastGood[document.URI]
	store.mu.RUnlock()
	if keptLastGood {
		t.Fatal("pre-rotation in-flight snapshot repopulated LastGood")
	}

	fresh := store.Snapshot(root, document)
	if fresh == nil || fresh.Module == nil {
		t.Fatalf("fresh post-rotation snapshot = %#v", fresh)
	}
	if current, ok := store.Current(document.URI); !ok || current != fresh {
		t.Fatalf("fresh post-rotation snapshot was not cached: current=%p ok=%v fresh=%p", current, ok, fresh)
	}
	mu.Lock()
	defer mu.Unlock()
	if builds != 2 {
		t.Fatalf("analysis builds = %d, want 2 (stale + fresh)", builds)
	}
}

func TestPhase15_4C2CSemanticRotationRejectsInflightTransientPublication(t *testing.T) {
	root := t.TempDir()
	packagePath := "example.com/app/model"
	pagePath := filepath.Join(root, "pages", "index.gox")
	document := Document{URI: FileURI(pagePath), Path: pagePath, Version: 1, Text: "module page\n"}
	source := "module repaired\n"
	key := transientAnalysisKey{URI: document.URI, Version: document.Version, Source: source}

	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	builds := 0
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		mu.Lock()
		builds++
		build := builds
		mu.Unlock()
		if build == 1 {
			close(started)
			<-release
		}
		return phase15_4C2CModuleWithGoPackageDependency(packagePath), nil
	})
	store.semanticCache = goxcompiler.NewSemanticWorkspaceCache()

	done := make(chan *goxcompiler.Module, 1)
	go func() {
		module, _ := store.Transient(root, document, source)
		done <- module
	}()
	<-started
	store.InvalidateGoPackages(root, []string{packagePath})
	close(release)
	if module := <-done; module == nil {
		t.Fatal("caller lost completed pre-rotation transient result")
	}

	store.mu.RLock()
	_, cached := store.transient[key]
	store.mu.RUnlock()
	if cached {
		t.Fatal("pre-rotation in-flight transient repopulated transient cache")
	}

	module, err := store.Transient(root, document, source)
	if err != nil || module == nil {
		t.Fatalf("fresh post-rotation transient = %#v, %v", module, err)
	}
	store.mu.RLock()
	_, cached = store.transient[key]
	store.mu.RUnlock()
	if !cached {
		t.Fatal("fresh post-rotation transient was not cached")
	}
	mu.Lock()
	defer mu.Unlock()
	if builds != 2 {
		t.Fatalf("transient builds = %d, want 2 (stale + fresh)", builds)
	}
}

func TestPhase15_4C2CSemanticGenerationIsRootScoped(t *testing.T) {
	rootOne := filepath.Join(t.TempDir(), "one")
	rootTwo := filepath.Join(t.TempDir(), "two")
	packagePath := "example.com/shared/model"
	one := Document{URI: FileURI(filepath.Join(rootOne, "one.gox")), Path: filepath.Join(rootOne, "one.gox"), Version: 1, Text: "module one\n"}
	two := Document{URI: FileURI(filepath.Join(rootTwo, "two.gox")), Path: filepath.Join(rootTwo, "two.gox"), Version: 1, Text: "module two\n"}

	started := make(chan string, 2)
	release := make(chan struct{})
	store := newAnalysisStore(func(root string, _ Document) (*goxcompiler.Module, error) {
		started <- root
		<-release
		return phase15_4C2CModuleWithGoPackageDependency(packagePath), nil
	})
	store.semanticCache = goxcompiler.NewSemanticWorkspaceCache()

	done := make(chan *AnalysisSnapshot, 2)
	go func() { done <- store.Snapshot(rootOne, one) }()
	go func() { done <- store.Snapshot(rootTwo, two) }()
	<-started
	<-started

	store.InvalidateGoPackages(rootOne, []string{packagePath})
	close(release)
	<-done
	<-done

	if _, ok := store.Current(one.URI); ok {
		t.Fatal("rotated root accepted an in-flight snapshot from the old semantic generation")
	}
	if current, ok := store.Current(two.URI); !ok || current == nil {
		t.Fatal("unrelated root in-flight snapshot was rejected by another root's semantic rotation")
	}
}

func TestPhase15_4C2CConcurrentSemanticRotationRejectsAllOldRootBuilds(t *testing.T) {
	root := t.TempDir()
	packagePath := "example.com/app/model"
	const documents = 12
	started := make(chan struct{}, documents)
	release := make(chan struct{})
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		started <- struct{}{}
		<-release
		return phase15_4C2CModuleWithGoPackageDependency(packagePath), nil
	})
	store.semanticCache = goxcompiler.NewSemanticWorkspaceCache()

	docs := make([]Document, 0, documents)
	done := make(chan *AnalysisSnapshot, documents)
	for i := 0; i < documents; i++ {
		path := filepath.Join(root, "pages", "page"+string(rune('a'+i))+".gox")
		document := Document{URI: FileURI(path), Path: path, Version: 1, Text: "module page\n"}
		docs = append(docs, document)
		go func(document Document) { done <- store.Snapshot(root, document) }(document)
	}
	for i := 0; i < documents; i++ {
		<-started
	}
	store.InvalidateGoPackages(root, []string{packagePath})
	close(release)
	for i := 0; i < documents; i++ {
		if snapshot := <-done; snapshot == nil {
			t.Fatal("caller lost completed pre-rotation concurrent analysis")
		}
	}
	for _, document := range docs {
		if _, ok := store.Current(document.URI); ok {
			t.Fatalf("pre-rotation concurrent snapshot %q repopulated cache", document.URI)
		}
	}
}

func TestPhase15_4C2CModuleAndWorkspaceInvalidationStillRejectInflightBuilds(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		root := t.TempDir()
		document := Document{URI: FileURI(filepath.Join(root, "page.gox")), Path: filepath.Join(root, "page.gox"), Version: 1, Text: "module page\n"}
		started := make(chan struct{})
		release := make(chan struct{})
		store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
			close(started)
			<-release
			return &goxcompiler.Module{}, nil
		})
		store.semanticCache = goxcompiler.NewSemanticWorkspaceCache()
		done := make(chan *AnalysisSnapshot, 1)
		go func() { done <- store.Snapshot(root, document) }()
		<-started
		store.InvalidateRoot(root)
		close(release)
		<-done
		if _, ok := store.Current(document.URI); ok {
			t.Fatal("root/module invalidation allowed old in-flight snapshot to repopulate cache")
		}
	})

	t.Run("all", func(t *testing.T) {
		root := t.TempDir()
		document := Document{URI: FileURI(filepath.Join(root, "page.gox")), Path: filepath.Join(root, "page.gox"), Version: 1, Text: "module page\n"}
		started := make(chan struct{})
		release := make(chan struct{})
		store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
			close(started)
			<-release
			return &goxcompiler.Module{}, nil
		})
		store.semanticCache = goxcompiler.NewSemanticWorkspaceCache()
		done := make(chan *AnalysisSnapshot, 1)
		go func() { done <- store.Snapshot(root, document) }()
		<-started
		store.InvalidateAll()
		close(release)
		<-done
		if _, ok := store.Current(document.URI); ok {
			t.Fatal("workspace-wide invalidation allowed old in-flight snapshot to repopulate cache")
		}
	})
}

func phase15_4C2CModuleWithGoPackageDependency(packagePath string) *goxcompiler.Module {
	return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
		Kind:        goxcompiler.SemanticDependencyGoPackage,
		PackagePath: packagePath,
		ImportPath:  packagePath,
	}}}}
}
