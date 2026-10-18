package lsp

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestAnalysisStoreReusesCanonicalURIAndVersion(t *testing.T) {
	builds := 0
	module := &goxcompiler.Module{}
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		builds++
		return module, nil
	})
	document := Document{URI: "file:///workspace/helper.test.gox", Path: "/workspace/helper.test.gox", Version: 7, Text: "module helper\n"}

	first := store.Snapshot("/workspace", document)
	second := store.Snapshot("/workspace", document)
	if first == nil || first != second {
		t.Fatalf("snapshots are not canonical: first=%p second=%p", first, second)
	}
	if builds != 1 {
		t.Fatalf("analysis builds = %d, want 1", builds)
	}
	if first.Module != module || first.LastGood != module || first.LastGoodVersion != document.Version {
		t.Fatalf("snapshot = %#v", first)
	}
}

func TestAnalysisStoreRebuildsSyntheticSameVersionWhenTextChanges(t *testing.T) {
	builds := 0
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		builds++
		return &goxcompiler.Module{}, nil
	})
	first := Document{URI: "file:///workspace/helper.test.gox", Path: "/workspace/helper.test.gox", Version: 0, Text: "module first\n"}
	second := first
	second.Text = "module second\n"

	firstSnapshot := store.Snapshot("/workspace", first)
	secondSnapshot := store.Snapshot("/workspace", second)
	if firstSnapshot == secondSnapshot {
		t.Fatal("same synthetic version with different source reused a stale snapshot")
	}
	if builds != 2 {
		t.Fatalf("analysis builds = %d, want 2", builds)
	}
}

func TestAnalysisStoreCarriesLastGoodAcrossInvalidVersion(t *testing.T) {
	goodModule := &goxcompiler.Module{}
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		if document.Text == "broken" {
			return nil, errors.New("broken source")
		}
		return goodModule, nil
	})
	good := Document{URI: "file:///workspace/helper.test.gox", Path: "/workspace/helper.test.gox", Version: 1, Text: "good"}
	broken := good
	broken.Version = 2
	broken.Text = "broken"

	if snapshot := store.Snapshot("/workspace", good); snapshot.Err != nil || snapshot.LastGood != goodModule {
		t.Fatalf("good snapshot = %#v", snapshot)
	}
	brokenSnapshot := store.Snapshot("/workspace", broken)
	if brokenSnapshot.Err == nil || brokenSnapshot.Module != nil {
		t.Fatalf("broken snapshot = %#v", brokenSnapshot)
	}
	if brokenSnapshot.LastGood != goodModule || brokenSnapshot.LastGoodVersion != 1 {
		t.Fatalf("last good = %p v%d, want %p v1", brokenSnapshot.LastGood, brokenSnapshot.LastGoodVersion, goodModule)
	}
}

func TestAnalysisStoreInvalidatesGoxDependencyPath(t *testing.T) {
	root := t.TempDir()
	entryPath := filepath.Join(root, "pages", "index.gox")
	dependencyPath := filepath.Join(root, "components", "menu.gox")
	uri := FileURI(entryPath)
	module := &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
		SourcePath:   entryPath,
		Kind:         goxcompiler.SemanticDependencyGoxModule,
		ImportPath:   "../components/menu.gox",
		ResolvedPath: dependencyPath,
	}}}}
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) { return module, nil })
	document := Document{URI: uri, Path: entryPath, Version: 3, Text: "module main\n"}
	store.Snapshot(root, document)

	invalidated := store.InvalidateDependencyPath(dependencyPath)
	if len(invalidated) != 1 || invalidated[0] != uri {
		t.Fatalf("invalidated = %#v, want [%q]", invalidated, uri)
	}
	if _, ok := store.Current(uri); ok {
		t.Fatal("dependency change left the snapshot cached")
	}
}

func TestServerCanonicalAnalysisIsSharedByCurrentDocumentConsumers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "helper.test.gox")
	document := Document{
		URI:     FileURI(path),
		Path:    path,
		Version: 1,
		Text:    "module helper\nfunc Value() int { return 1 }\n",
	}
	builds := 0
	server := NewServer()
	server.analysis = newAnalysisStore(func(root string, document Document) (*goxcompiler.Module, error) {
		builds++
		if root == "" {
			return goxcompiler.ParseModule(document.Path, document.Text)
		}
		return goxcompiler.ParseProjectModule(root, document.Path, document.Text)
	})

	snapshot := server.analysisSnapshot(document)
	if snapshot == nil || snapshot.Err != nil || snapshot.Module == nil {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if err := server.symbols.UpdateOpenModule(document, snapshot.Module); err != nil {
		t.Fatal(err)
	}

	if _, err := server.analysisModule(document); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.parseSemanticModule(document); !ok {
		t.Fatal("semantic module unavailable from canonical snapshot")
	}
	_ = server.diagnostics(document)
	if builds != 1 {
		t.Fatalf("analysis builds after multiple consumers = %d, want 1", builds)
	}

	document.Version = 2
	if snapshot := server.analysisSnapshot(document); snapshot == nil || snapshot.Err != nil {
		t.Fatalf("version 2 snapshot = %#v", snapshot)
	}
	if builds != 2 {
		t.Fatalf("analysis builds after version change = %d, want 2", builds)
	}
}

func TestServerCanonicalAnalysisPreservesStandalonePathClassification(t *testing.T) {
	document := Document{
		URI:     "file:///workspace/websocket/index.gox",
		Path:    "/workspace/websocket/index.gox",
		Version: 1,
		Text: `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {}
`,
	}
	server := NewServer()
	snapshot := server.analysisSnapshot(document)
	if snapshot == nil || snapshot.Err != nil || snapshot.Module == nil {
		t.Fatalf("standalone snapshot = %#v", snapshot)
	}
	if snapshot.Root != "" {
		t.Fatalf("standalone snapshot root = %q, want empty", snapshot.Root)
	}
}

func TestAnalysisStoreCloseDropsCurrentAndLastGood(t *testing.T) {
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		return &goxcompiler.Module{}, nil
	})
	document := Document{URI: "file:///workspace/helper.test.gox", Path: "/workspace/helper.test.gox", Version: 1, Text: "module helper\n"}
	store.Snapshot("/workspace", document)
	store.Close(document.URI)
	if _, ok := store.Current(document.URI); ok {
		t.Fatal("closed document still has a current analysis")
	}
	if _, ok := store.lastGood[document.URI]; ok {
		t.Fatal("closed document still has LastGood state")
	}
}

func BenchmarkAnalysisStoreCachedSnapshot(b *testing.B) {
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		return &goxcompiler.Module{}, nil
	})
	document := Document{URI: "file:///workspace/helper.test.gox", Path: "/workspace/helper.test.gox", Version: 1, Text: "module helper\n"}
	store.Snapshot("/workspace", document)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if snapshot := store.Snapshot("/workspace", document); snapshot == nil {
			b.Fatal("nil snapshot")
		}
	}
}

func TestAnalysisStoreInvalidatesDependentsWithoutDroppingChangedDocumentLastGood(t *testing.T) {
	root := t.TempDir()
	changedPath := filepath.Join(root, "components", "menu.gox")
	changedURI := FileURI(changedPath)
	dependentPath := filepath.Join(root, "pages", "index.gox")
	dependentURI := FileURI(dependentPath)

	changedModule := &goxcompiler.Module{}
	dependentModule := &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
		SourcePath:   dependentPath,
		Kind:         goxcompiler.SemanticDependencyGoxModule,
		ImportPath:   "../components/menu.gox",
		ResolvedPath: changedPath,
	}}}}
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		if document.URI == changedURI {
			return changedModule, nil
		}
		return dependentModule, nil
	})
	changed := Document{URI: changedURI, Path: changedPath, Version: 1, Text: "module menu\n"}
	dependent := Document{URI: dependentURI, Path: dependentPath, Version: 1, Text: "module main\n"}
	store.Snapshot(root, changed)
	store.Snapshot(root, dependent)

	invalidated := store.InvalidateDependencyPathExcept(changedPath, changedURI)
	if len(invalidated) != 1 || invalidated[0] != dependentURI {
		t.Fatalf("invalidated = %#v, want [%q]", invalidated, dependentURI)
	}
	if _, ok := store.Current(dependentURI); ok {
		t.Fatal("dependent snapshot survived source change")
	}
	current, ok := store.Current(changedURI)
	if !ok || current.LastGood != changedModule {
		t.Fatalf("changed document lost LastGood: %#v", current)
	}
}

func TestServerBuildsCanonicalAnalysisOncePerOpenDocumentVersion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "helper.test.gox")
	uri := FileURI(path)
	versionOne := "module helper\nfunc Value() int { return 1 }\n"
	versionTwo := "module helper\nfunc Value() int { return 2 }\n"

	builds := 0
	server := NewServer()
	server.analysis = newAnalysisStore(func(root string, document Document) (*goxcompiler.Module, error) {
		builds++
		if root == "" {
			return goxcompiler.ParseModule(document.Path, document.Text)
		}
		return goxcompiler.ParseProjectModule(root, document.Path, document.Text)
	})

	input := framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":`+quoteJSON(uri)+`,"languageId":"gox","version":1,"text":`+quoteJSON(versionOne)+`}}}`) +
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":`+quoteJSON(uri)+`,"version":2},"contentChanges":[{"text":`+quoteJSON(versionTwo)+`}]}}`) +
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`) +
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`)

	if err := server.Serve(strings.NewReader(input), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if builds != 2 {
		t.Fatalf("analysis builds = %d, want exactly one for each of two versions", builds)
	}
}

func quoteJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestPhase14AnalysisStoreCoalescesConcurrentBuilds(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	builds := 0
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		mu.Lock()
		builds++
		if builds == 1 {
			close(started)
		}
		mu.Unlock()
		<-release
		return &goxcompiler.Module{}, nil
	})
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Version: 1, Text: "module page\n"}

	const callers = 16
	results := make(chan *AnalysisSnapshot, callers)
	for i := 0; i < callers; i++ {
		go func() { results <- store.Snapshot("/workspace", document) }()
	}
	<-started
	close(release)

	var first *AnalysisSnapshot
	for i := 0; i < callers; i++ {
		snapshot := <-results
		if snapshot == nil {
			t.Fatal("nil concurrent snapshot")
		}
		if first == nil {
			first = snapshot
		} else if snapshot != first {
			t.Fatalf("concurrent snapshots are not canonical: first=%p current=%p", first, snapshot)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if builds != 1 {
		t.Fatalf("concurrent analysis builds = %d, want 1", builds)
	}
}

func TestPhase14AnalysisStoreBuildsUnrelatedDocumentsConcurrently(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		started <- document.URI
		<-release
		return &goxcompiler.Module{}, nil
	})
	one := Document{URI: "file:///workspace/one.gox", Path: "/workspace/one.gox", Version: 1, Text: "module one\n"}
	two := Document{URI: "file:///workspace/two.gox", Path: "/workspace/two.gox", Version: 1, Text: "module two\n"}
	done := make(chan struct{}, 2)
	go func() { store.Snapshot("/workspace", one); done <- struct{}{} }()
	go func() { store.Snapshot("/workspace", two); done <- struct{}{} }()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first analysis did not start")
	}
	select {
	case <-started:
		// Both builds reached the builder before either was released. Holding the
		// store lock across build would deadlock this assertion.
	case <-time.After(time.Second):
		t.Fatal("unrelated analysis was serialized behind the first build")
	}
	close(release)
	<-done
	<-done
}

func TestPhase14InvalidatedInflightBuildCannotRepopulateCache(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		close(started)
		<-release
		return &goxcompiler.Module{}, nil
	})
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Version: 1, Text: "module page\n"}
	done := make(chan *AnalysisSnapshot, 1)
	go func() { done <- store.Snapshot("/workspace", document) }()
	<-started
	store.Close(document.URI)
	close(release)
	if snapshot := <-done; snapshot == nil {
		t.Fatal("caller lost completed analysis result")
	}
	if _, ok := store.Current(document.URI); ok {
		t.Fatal("invalidated in-flight build repopulated current cache")
	}
	store.mu.RLock()
	_, keptLastGood := store.lastGood[document.URI]
	store.mu.RUnlock()
	if keptLastGood {
		t.Fatal("invalidated in-flight build repopulated LastGood")
	}
}

func TestPhase14DependencyInvalidationUsesReverseEdges(t *testing.T) {
	root := t.TempDir()
	changedPath := filepath.Join(root, "shared.gox")
	unrelatedPath := filepath.Join(root, "other.gox")
	dependentPath := filepath.Join(root, "page.gox")
	unrelatedPagePath := filepath.Join(root, "other-page.gox")
	dependentURI := FileURI(dependentPath)
	unrelatedURI := FileURI(unrelatedPagePath)

	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		resolved := unrelatedPath
		if document.URI == dependentURI {
			resolved = changedPath
		}
		return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
			SourcePath: document.Path, ResolvedPath: resolved, Kind: goxcompiler.SemanticDependencyGoxModule,
		}}}}, nil
	})
	store.Snapshot(root, Document{URI: dependentURI, Path: dependentPath, Version: 1, Text: "module page\n"})
	store.Snapshot(root, Document{URI: unrelatedURI, Path: unrelatedPagePath, Version: 1, Text: "module other\n"})

	invalidated := store.InvalidateDependencyPath(changedPath)
	if len(invalidated) != 1 || invalidated[0] != dependentURI {
		t.Fatalf("invalidated = %#v, want [%q]", invalidated, dependentURI)
	}
	if _, ok := store.Current(dependentURI); ok {
		t.Fatal("dependent snapshot survived reverse-edge invalidation")
	}
	if _, ok := store.Current(unrelatedURI); !ok {
		t.Fatal("unrelated snapshot was invalidated")
	}
}

func TestPhase14TransientAnalysisIsCachedButNeverBecomesCanonical(t *testing.T) {
	builds := 0
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		builds++
		return &goxcompiler.Module{Source: document.Text}, nil
	})
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Version: 4, Text: "module page\nfunc Build() { value. }\n"}
	repaired := strings.Replace(document.Text, "value.", "value.__gox_completion", 1)

	first, firstErr := store.Transient("/workspace", document, repaired)
	second, secondErr := store.Transient("/workspace", document, repaired)
	if firstErr != nil || secondErr != nil || first == nil || first != second {
		t.Fatalf("transient cache = first:%p/%v second:%p/%v", first, firstErr, second, secondErr)
	}
	if builds != 1 {
		t.Fatalf("transient builds = %d, want 1", builds)
	}
	if _, ok := store.Current(document.URI); ok {
		t.Fatal("transient analysis became the canonical current snapshot")
	}
	if _, ok := store.lastGood[document.URI]; ok {
		t.Fatal("transient analysis polluted LastGood")
	}
}

func TestPhase14TransientDependencyInvalidationKeepsUnrelatedRepairsWarm(t *testing.T) {
	root := t.TempDir()
	sharedPath := filepath.Join(root, "shared.gox")
	otherPath := filepath.Join(root, "other.gox")
	builds := make(map[string]int)
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		builds[document.URI]++
		dependency := otherPath
		if document.URI == FileURI(filepath.Join(root, "page.gox")) {
			dependency = sharedPath
		}
		return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
			SourcePath: document.Path, ResolvedPath: dependency, Kind: goxcompiler.SemanticDependencyGoxModule,
		}}}}, nil
	})
	page := Document{URI: FileURI(filepath.Join(root, "page.gox")), Path: filepath.Join(root, "page.gox"), Version: 1, Text: "page"}
	other := Document{URI: FileURI(filepath.Join(root, "second.gox")), Path: filepath.Join(root, "second.gox"), Version: 1, Text: "second"}
	if _, err := store.Transient(root, page, "page repaired"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transient(root, other, "second repaired"); err != nil {
		t.Fatal(err)
	}
	store.InvalidateDependencyPath(sharedPath)
	if _, err := store.Transient(root, page, "page repaired"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transient(root, other, "second repaired"); err != nil {
		t.Fatal(err)
	}
	if builds[page.URI] != 2 {
		t.Fatalf("dependent transient builds = %d, want 2", builds[page.URI])
	}
	if builds[other.URI] != 1 {
		t.Fatalf("unrelated transient builds = %d, want 1", builds[other.URI])
	}
}

func TestPhase14AnalysisStoreInvalidatesOnlyChangedProjectRoot(t *testing.T) {
	rootOne := t.TempDir()
	rootTwo := t.TempDir()
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		return &goxcompiler.Module{}, nil
	})
	onePath := filepath.Join(rootOne, "page.gox")
	twoPath := filepath.Join(rootTwo, "page.gox")
	one := Document{URI: FileURI(onePath), Path: onePath, Version: 1, Text: "module one\n"}
	two := Document{URI: FileURI(twoPath), Path: twoPath, Version: 1, Text: "module two\n"}
	store.Snapshot(rootOne, one)
	store.Snapshot(rootTwo, two)

	invalidated := store.InvalidateRoot(rootOne)
	if len(invalidated) != 1 || invalidated[0] != one.URI {
		t.Fatalf("invalidated = %#v, want [%q]", invalidated, one.URI)
	}
	if _, ok := store.Current(one.URI); ok {
		t.Fatal("changed project root snapshot survived invalidation")
	}
	if _, ok := store.Current(two.URI); !ok {
		t.Fatal("unrelated project root snapshot was invalidated")
	}
}

func TestPhase14NewerVersionSupersedesOlderInflightBuild(t *testing.T) {
	startedV1 := make(chan struct{})
	releaseV1 := make(chan struct{})
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		if document.Version == 1 {
			close(startedV1)
			<-releaseV1
		}
		return &goxcompiler.Module{Source: document.Text}, nil
	})
	v1 := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Version: 1, Text: "module one\n"}
	v2 := v1
	v2.Version = 2
	v2.Text = "module two\n"
	oldDone := make(chan *AnalysisSnapshot, 1)
	go func() { oldDone <- store.Snapshot("/workspace", v1) }()
	<-startedV1
	newSnapshot := store.Snapshot("/workspace", v2)
	if newSnapshot == nil || newSnapshot.Document.Version != 2 {
		t.Fatalf("new snapshot = %#v", newSnapshot)
	}
	close(releaseV1)
	<-oldDone
	current, ok := store.Current(v1.URI)
	if !ok || current.Document.Version != 2 || current.Document.Text != v2.Text {
		t.Fatalf("current after stale build completed = %#v, want version 2", current)
	}
}

func TestPhase14DependencyChangeSupersedesFirstInflightBuild(t *testing.T) {
	root := t.TempDir()
	dependencyPath := filepath.Join(root, "shared.gox")
	pagePath := filepath.Join(root, "page.gox")
	document := Document{URI: FileURI(pagePath), Path: pagePath, Version: 1, Text: "module page\n"}
	started := make(chan struct{})
	release := make(chan struct{})
	store := newAnalysisStore(func(_ string, _ Document) (*goxcompiler.Module, error) {
		close(started)
		<-release
		return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
			SourcePath: pagePath, ResolvedPath: dependencyPath, Kind: goxcompiler.SemanticDependencyGoxModule,
		}}}}, nil
	})
	done := make(chan *AnalysisSnapshot, 1)
	go func() { done <- store.Snapshot(root, document) }()
	<-started
	store.InvalidateDependencyPath(dependencyPath)
	close(release)
	if snapshot := <-done; snapshot == nil {
		t.Fatal("caller lost superseded analysis result")
	}
	if _, ok := store.Current(document.URI); ok {
		t.Fatal("dependency change allowed first in-flight build to become current")
	}
}

func TestPhase14BrokenSnapshotKeepsLastGoodDependencyEdges(t *testing.T) {
	root := t.TempDir()
	dependencyPath := filepath.Join(root, "shared.gox")
	pagePath := filepath.Join(root, "page.gox")
	uri := FileURI(pagePath)
	goodModule := &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: []goxcompiler.SemanticDependency{{
		SourcePath: pagePath, ResolvedPath: dependencyPath, Kind: goxcompiler.SemanticDependencyGoxModule,
	}}}}
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		if document.Version == 2 {
			return nil, errors.New("broken")
		}
		return goodModule, nil
	})
	good := Document{URI: uri, Path: pagePath, Version: 1, Text: "module page\n"}
	broken := good
	broken.Version = 2
	broken.Text = "broken"
	store.Snapshot(root, good)
	brokenSnapshot := store.Snapshot(root, broken)
	if brokenSnapshot.LastGood != goodModule {
		t.Fatalf("broken LastGood = %p, want %p", brokenSnapshot.LastGood, goodModule)
	}
	invalidated := store.InvalidateDependencyPath(dependencyPath)
	if len(invalidated) != 1 || invalidated[0] != uri {
		t.Fatalf("invalidated broken dependent = %#v, want [%q]", invalidated, uri)
	}
}
