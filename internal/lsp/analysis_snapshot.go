package lsp

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

// AnalysisSnapshot is the immutable semantic analysis owned by one editor
// document version. Consumers must reuse Module instead of reparsing the same
// URI/version independently. LastGood deliberately survives a syntactically
// invalid edit so later phases can use it as a controlled fallback without
// making stale data the authority for the current version.
type AnalysisSnapshot struct {
	Document Document
	Root     string

	Module *goxcompiler.Module
	Err    error

	LastGood        *goxcompiler.Module
	LastGoodVersion int

	Dependencies []goxcompiler.SemanticDependency

	diagnosticsOnce sync.Once
	diagnostics     []Diagnostic
}

func (s *AnalysisSnapshot) semanticReady() bool {
	return s != nil && s.Module != nil && s.Module.Semantic != nil && s.Module.Semantic.Info != nil
}

type analysisKey struct {
	URI     string
	Version int
}

type transientAnalysisKey struct {
	URI     string
	Version int
	Source  string
}

type analysisBuilder func(root string, document Document) (*goxcompiler.Module, error)

type analysisGoPackageDependencyKey struct {
	Root        string
	PackagePath string
}

type analysisBuild struct {
	root     string
	document Document
	done     chan struct{}
}

type transientAnalysis struct {
	root         string
	document     Document
	module       *goxcompiler.Module
	err          error
	dependencies []goxcompiler.SemanticDependency
}

type transientBuild struct {
	root     string
	document Document
	done     chan struct{}
}

// AnalysisStore owns canonical analyses for open document versions. Expensive
// builds happen outside the store lock: equal URI/version requests coalesce on
// one in-flight build while unrelated documents may analyze concurrently.
// Invalidations advance a generation so a build that finishes after its input
// became stale can never repopulate the cache.
type AnalysisStore struct {
	mu sync.RWMutex

	build         analysisBuilder
	semanticCache *goxcompiler.SemanticWorkspaceCache

	snapshots map[analysisKey]*AnalysisSnapshot
	current   map[string]analysisKey
	lastGood  map[string]*AnalysisSnapshot
	inflight  map[analysisKey]*analysisBuild

	// Reverse dependency edges make a .gox change proportional to the number
	// of affected open documents instead of requiring a scan of every snapshot.
	reverseDependencies map[string]map[string]struct{}
	dependenciesByURI   map[string]map[string]struct{}

	// Go package dependencies live in a different namespace from filesystem
	// dependencies. Scope them by project root so equal module/import paths in
	// separate workspace folders cannot invalidate each other.
	goPackageReverseDependencies map[analysisGoPackageDependencyKey]map[string]struct{}
	goPackageDependenciesByURI   map[string]map[analysisGoPackageDependencyKey]struct{}

	// Transient analyses are repaired editor sources (for example `value.` ->
	// `value.__gox_completion`). They are deliberately separate from canonical
	// snapshots/LastGood, but are cached because completion may ask for the same
	// repaired source many times while the document version is unchanged.
	transient         map[transientAnalysisKey]*transientAnalysis
	transientInflight map[transientAnalysisKey]*transientBuild

	// semanticGeneration rejects in-flight builds from an older importer universe.
	// Completed unrelated snapshots remain reusable; generations are root scoped.
	semanticGeneration map[string]uint64
	generation         map[string]uint64
	desired            map[string]analysisKey
	dependencyChanges  map[string]uint64
	dependencyRevision uint64
	epoch              uint64
}

func NewAnalysisStore() *AnalysisStore {
	semanticCache := goxcompiler.NewSemanticWorkspaceCache()
	store := newAnalysisStore(func(root string, document Document) (*goxcompiler.Module, error) {
		if root == "" {
			return goxcompiler.ParseModuleForTooling(document.Path, document.Text)
		}
		return goxcompiler.ParseProjectModuleCachedForTooling(root, document.Path, document.Text, semanticCache)
	})
	store.semanticCache = semanticCache
	return store
}

func newAnalysisStore(build analysisBuilder) *AnalysisStore {
	if build == nil {
		build = func(root string, document Document) (*goxcompiler.Module, error) {
			if root == "" {
				return goxcompiler.ParseModuleForTooling(document.Path, document.Text)
			}
			return goxcompiler.ParseProjectModuleForTooling(root, document.Path, document.Text)
		}
	}
	return &AnalysisStore{
		build:                        build,
		snapshots:                    make(map[analysisKey]*AnalysisSnapshot),
		current:                      make(map[string]analysisKey),
		lastGood:                     make(map[string]*AnalysisSnapshot),
		inflight:                     make(map[analysisKey]*analysisBuild),
		reverseDependencies:          make(map[string]map[string]struct{}),
		dependenciesByURI:            make(map[string]map[string]struct{}),
		goPackageReverseDependencies: make(map[analysisGoPackageDependencyKey]map[string]struct{}),
		goPackageDependenciesByURI:   make(map[string]map[analysisGoPackageDependencyKey]struct{}),
		transient:                    make(map[transientAnalysisKey]*transientAnalysis),
		transientInflight:            make(map[transientAnalysisKey]*transientBuild),
		semanticGeneration:           make(map[string]uint64),
		generation:                   make(map[string]uint64),
		desired:                      make(map[string]analysisKey),
		dependencyChanges:            make(map[string]uint64),
	}
}

func (s *AnalysisStore) Snapshot(root string, document Document) *AnalysisSnapshot {
	if s == nil {
		return nil
	}
	root = cleanAnalysisRoot(root, document.Path)
	key := analysisKey{URI: document.URI, Version: document.Version}

	for {
		s.mu.RLock()
		if snapshot := s.snapshots[key]; snapshot != nil && sameAnalysisDocument(snapshot.Document, document) && samePath(snapshot.Root, root) {
			s.mu.RUnlock()
			return snapshot
		}
		if pending := s.inflight[key]; pending != nil {
			done := pending.done
			s.mu.RUnlock()
			<-done
			continue
		}
		s.mu.RUnlock()

		s.mu.Lock()
		if snapshot := s.snapshots[key]; snapshot != nil && sameAnalysisDocument(snapshot.Document, document) && samePath(snapshot.Root, root) {
			s.mu.Unlock()
			return snapshot
		}
		if pending := s.inflight[key]; pending != nil {
			done := pending.done
			s.mu.Unlock()
			<-done
			continue
		}
		cacheable := true
		if desired, ok := s.desired[document.URI]; ok && document.Version > 0 && desired.Version > document.Version {
			cacheable = false
		} else if !ok || desired != key {
			s.generation[document.URI]++
			s.desired[document.URI] = key
		}
		generation, epoch, dependencyRevision := s.generation[document.URI], s.epoch, s.dependencyRevision
		semanticGeneration := s.semanticGeneration[root]
		pending := &analysisBuild{root: root, document: document, done: make(chan struct{})}
		s.inflight[key] = pending
		s.mu.Unlock()

		module, err := s.build(root, document)
		snapshot := newAnalysisSnapshot(root, document, module, err)

		s.mu.Lock()
		if s.inflight[key] == pending {
			delete(s.inflight, key)
			close(pending.done)
		}

		// The build may have overlapped a close, dependency change, go.mod/go.work
		// invalidation, or another explicit cache reset. Its result is still safe
		// for the caller that started it, but it must not become current state.
		if !cacheable || s.semanticGeneration[root] != semanticGeneration || s.epoch != epoch || s.generation[document.URI] != generation || s.dependenciesChangedSinceLocked(snapshot, dependencyRevision) {
			s.attachLastGoodResultLocked(snapshot)
			s.mu.Unlock()
			return snapshot
		}

		// The LSP protocol guarantees monotonically increasing versions, but many
		// focused unit tests construct Version==0 documents directly. If the text
		// or path changed under the same synthetic key, it is a different snapshot.
		if previousKey, ok := s.current[document.URI]; ok && previousKey != key {
			s.removeSnapshotLocked(previousKey)
		} else if existing := s.snapshots[key]; existing != nil {
			s.removeSnapshotLocked(key)
		}
		s.removeTransientURILocked(document.URI)
		s.attachLastGoodLocked(snapshot)
		s.snapshots[key] = snapshot
		s.current[document.URI] = key
		s.addDependenciesLocked(snapshot)
		s.mu.Unlock()
		return snapshot
	}
}

// Transient analyzes repaired editor text without making it authoritative for
// the document version. The result is cached independently from Snapshot so a
// repeated completion on the same dangling selector does not rebuild the full
// semantic workspace on every keystroke/request.
func (s *AnalysisStore) Transient(root string, document Document, source string) (*goxcompiler.Module, error) {
	if s == nil {
		return nil, nil
	}
	if source == document.Text {
		snapshot := s.Snapshot(root, document)
		if snapshot == nil {
			return nil, nil
		}
		return snapshot.Module, snapshot.Err
	}
	root = cleanAnalysisRoot(root, document.Path)
	key := transientAnalysisKey{URI: document.URI, Version: document.Version, Source: source}

	for {
		s.mu.RLock()
		if cached := s.transient[key]; cached != nil && sameAnalysisDocumentExceptText(cached.document, document) && samePath(cached.root, root) {
			s.mu.RUnlock()
			return cached.module, cached.err
		}
		if pending := s.transientInflight[key]; pending != nil {
			done := pending.done
			s.mu.RUnlock()
			<-done
			continue
		}
		s.mu.RUnlock()

		s.mu.Lock()
		if cached := s.transient[key]; cached != nil && sameAnalysisDocumentExceptText(cached.document, document) && samePath(cached.root, root) {
			s.mu.Unlock()
			return cached.module, cached.err
		}
		if pending := s.transientInflight[key]; pending != nil {
			done := pending.done
			s.mu.Unlock()
			<-done
			continue
		}
		generation, epoch, dependencyRevision := s.generation[document.URI], s.epoch, s.dependencyRevision
		semanticGeneration := s.semanticGeneration[root]
		pending := &transientBuild{root: root, document: document, done: make(chan struct{})}
		s.transientInflight[key] = pending
		s.mu.Unlock()

		transientDocument := document
		transientDocument.Text = source
		module, err := s.build(root, transientDocument)
		analysis := &transientAnalysis{root: root, document: document, module: module, err: err}
		if module != nil && module.Semantic != nil {
			analysis.dependencies = append([]goxcompiler.SemanticDependency(nil), module.Semantic.Dependencies...)
		}

		s.mu.Lock()
		if s.transientInflight[key] == pending {
			delete(s.transientInflight, key)
			close(pending.done)
		}
		if s.semanticGeneration[root] == semanticGeneration && s.epoch == epoch && s.generation[document.URI] == generation && !s.transientDependenciesChangedSinceLocked(analysis, dependencyRevision) {
			s.transient[key] = analysis
		}
		s.mu.Unlock()
		return module, err
	}
}

func newAnalysisSnapshot(root string, document Document, module *goxcompiler.Module, err error) *AnalysisSnapshot {
	snapshot := &AnalysisSnapshot{Document: document, Root: root, Module: module, Err: err}
	if module != nil && module.Semantic != nil {
		snapshot.Dependencies = append([]goxcompiler.SemanticDependency(nil), module.Semantic.Dependencies...)
	}
	return snapshot
}

func (s *AnalysisStore) attachLastGoodResultLocked(snapshot *AnalysisSnapshot) {
	if snapshot == nil {
		return
	}
	if snapshot.Err == nil && snapshot.Module != nil {
		snapshot.LastGood = snapshot.Module
		snapshot.LastGoodVersion = snapshot.Document.Version
		return
	}
	if good := s.lastGood[snapshot.Document.URI]; good != nil && good.Module != nil {
		snapshot.LastGood = good.Module
		snapshot.LastGoodVersion = good.Document.Version
	}
}

func (s *AnalysisStore) attachLastGoodLocked(snapshot *AnalysisSnapshot) {
	if snapshot == nil {
		return
	}
	if snapshot.Err == nil && snapshot.Module != nil {
		snapshot.LastGood = snapshot.Module
		snapshot.LastGoodVersion = snapshot.Document.Version
		s.lastGood[snapshot.Document.URI] = snapshot
		return
	}
	if good := s.lastGood[snapshot.Document.URI]; good != nil && good.Module != nil {
		snapshot.LastGood = good.Module
		snapshot.LastGoodVersion = good.Document.Version
	}
}

func (s *AnalysisStore) Current(uri string) (*AnalysisSnapshot, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, ok := s.current[uri]
	if !ok {
		return nil, false
	}
	snapshot := s.snapshots[key]
	return snapshot, snapshot != nil
}

func (s *AnalysisStore) Close(uri string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.invalidateURILocked(uri, true)
	s.mu.Unlock()
}

func (s *AnalysisStore) InvalidateURI(uri string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.invalidateURILocked(uri, true)
	s.mu.Unlock()
}

// InvalidateDependencyPath removes open snapshots whose semantic dependency
// graph reaches the changed .gox path. Reverse edges make this lookup
// proportional to the affected documents rather than the whole workspace.
func (s *AnalysisStore) InvalidateDependencyPath(path string) []string {
	return s.invalidateDependencyPath(path, "")
}

// InvalidateDependencyPathExcept is used when an open document changes. Its
// dependents become stale, but the changed document keeps its previous
// LastGood until the new version has been analyzed.
func (s *AnalysisStore) InvalidateDependencyPathExcept(path, exceptURI string) []string {
	return s.invalidateDependencyPath(path, exceptURI)
}

func (s *AnalysisStore) invalidateDependencyPath(path, exceptURI string) []string {
	if s == nil {
		return nil
	}
	path = analysisDependencyPath(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dependencyRevision++
	s.dependencyChanges[path] = s.dependencyRevision

	dependents := s.reverseDependencies[path]
	invalidated := make([]string, 0, len(dependents))
	for uri := range dependents {
		if uri == exceptURI {
			continue
		}
		if _, ok := s.current[uri]; !ok {
			continue
		}
		invalidated = append(invalidated, uri)
	}
	sort.Strings(invalidated)
	for _, uri := range invalidated {
		s.invalidateURILocked(uri, true)
	}

	// Transient repaired analyses are few and intentionally not part of the
	// canonical reverse graph. Drop only entries whose recorded semantic graph
	// reaches the changed path; this keeps unrelated editor repairs warm.
	for key, cached := range s.transient {
		if key.URI == exceptURI {
			continue
		}
		if transientDependsOnPath(cached, path) {
			delete(s.transient, key)
		}
	}
	return invalidated
}

// InvalidateRoot drops semantic state belonging to one project root while
// preserving warm analyses and Go package identities for unrelated workspace
// folders. It is used for module-metadata changes and conservative fallbacks;
// ordinary production .go changes use InvalidateGoPackages.
func (s *AnalysisStore) InvalidateRoot(root string) []string {
	if s == nil {
		return nil
	}
	root = cleanAnalysisRoot(root, root)
	s.mu.Lock()
	affected := make(map[string]struct{})
	for uri, key := range s.current {
		snapshot := s.snapshots[key]
		if snapshot != nil && samePath(snapshot.Root, root) {
			affected[uri] = struct{}{}
		}
	}
	for _, pending := range s.inflight {
		if pending != nil && samePath(pending.root, root) {
			affected[pending.document.URI] = struct{}{}
		}
	}
	for key, cached := range s.transient {
		if cached != nil && samePath(cached.root, root) {
			affected[key.URI] = struct{}{}
		}
	}
	for _, pending := range s.transientInflight {
		if pending != nil && samePath(pending.root, root) {
			affected[pending.document.URI] = struct{}{}
		}
	}
	invalidated := make([]string, 0, len(affected))
	for uri := range affected {
		invalidated = append(invalidated, uri)
	}
	sort.Strings(invalidated)
	for _, uri := range invalidated {
		s.invalidateURILocked(uri, true)
	}
	s.semanticGeneration[root]++
	if s.semanticCache != nil {
		s.semanticCache.InvalidateRoot(root)
	}
	s.mu.Unlock()
	return invalidated
}

func (s *AnalysisStore) InvalidateAll() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.epoch++
	s.snapshots = make(map[analysisKey]*AnalysisSnapshot)
	s.current = make(map[string]analysisKey)
	s.lastGood = make(map[string]*AnalysisSnapshot)
	s.reverseDependencies = make(map[string]map[string]struct{})
	s.dependenciesByURI = make(map[string]map[string]struct{})
	s.goPackageReverseDependencies = make(map[analysisGoPackageDependencyKey]map[string]struct{})
	s.goPackageDependenciesByURI = make(map[string]map[analysisGoPackageDependencyKey]struct{})
	s.transient = make(map[transientAnalysisKey]*transientAnalysis)
	s.desired = make(map[string]analysisKey)
	s.dependencyChanges = make(map[string]uint64)
	s.dependencyRevision = 0
	if s.semanticCache != nil {
		s.semanticCache.InvalidateAll()
	}
	s.mu.Unlock()
}

func (s *AnalysisStore) invalidateURILocked(uri string, dropLastGood bool) {
	s.generation[uri]++
	delete(s.desired, uri)
	if key, ok := s.current[uri]; ok {
		s.removeSnapshotLocked(key)
	}
	delete(s.current, uri)
	s.removeTransientURILocked(uri)
	if dropLastGood {
		delete(s.lastGood, uri)
	}
}

func (s *AnalysisStore) removeSnapshotLocked(key analysisKey) {
	snapshot := s.snapshots[key]
	delete(s.snapshots, key)
	if snapshot != nil {
		s.removeDependenciesLocked(snapshot.Document.URI)
	}
}

func (s *AnalysisStore) addDependenciesLocked(snapshot *AnalysisSnapshot) {
	if snapshot == nil || snapshot.Document.URI == "" {
		return
	}
	uri := snapshot.Document.URI
	s.removeDependenciesLocked(uri)
	paths := make(map[string]struct{})
	goPackages := make(map[analysisGoPackageDependencyKey]struct{})
	add := func(path string) {
		if path == "" {
			return
		}
		path = analysisDependencyPath(path)
		paths[path] = struct{}{}
		dependents := s.reverseDependencies[path]
		if dependents == nil {
			dependents = make(map[string]struct{})
			s.reverseDependencies[path] = dependents
		}
		dependents[uri] = struct{}{}
	}
	addGoPackage := func(packagePath string) {
		key, ok := analysisGoPackageDependency(snapshot.Root, packagePath)
		if !ok {
			return
		}
		goPackages[key] = struct{}{}
		dependents := s.goPackageReverseDependencies[key]
		if dependents == nil {
			dependents = make(map[string]struct{})
			s.goPackageReverseDependencies[key] = dependents
		}
		dependents[uri] = struct{}{}
	}
	add(snapshot.Document.Path)
	dependencies := snapshot.Dependencies
	if len(dependencies) == 0 && snapshot.LastGood != nil && snapshot.LastGood.Semantic != nil {
		dependencies = snapshot.LastGood.Semantic.Dependencies
	}
	for _, dependency := range dependencies {
		add(dependency.SourcePath)
		add(dependency.ResolvedPath)
		for _, candidate := range dependency.ResolutionCandidates {
			add(candidate)
		}
		if dependency.Kind == goxcompiler.SemanticDependencyGoPackage {
			addGoPackage(dependency.PackagePath)
			addGoPackage(dependency.ImportPath)
		}
	}
	s.dependenciesByURI[uri] = paths
	s.goPackageDependenciesByURI[uri] = goPackages
}

func (s *AnalysisStore) removeDependenciesLocked(uri string) {
	for path := range s.dependenciesByURI[uri] {
		dependents := s.reverseDependencies[path]
		delete(dependents, uri)
		if len(dependents) == 0 {
			delete(s.reverseDependencies, path)
		}
	}
	delete(s.dependenciesByURI, uri)
	for key := range s.goPackageDependenciesByURI[uri] {
		dependents := s.goPackageReverseDependencies[key]
		delete(dependents, uri)
		if len(dependents) == 0 {
			delete(s.goPackageReverseDependencies, key)
		}
	}
	delete(s.goPackageDependenciesByURI, uri)
}

// goPackageDependentURIs returns the open snapshots in root that currently
// depend on any of packagePaths. C2a deliberately exposes this lookup without
// changing invalidation behavior; the next phase can consume the same index.
// InvalidateGoPackages removes only snapshots whose recorded semantic Go
// package dependencies intersect packagePaths. The semantic importer itself is
// still one canonical go/types universe per root, so every production .go
// change rotates that root-wide universe even when unrelated immutable
// snapshots remain cached.
func (s *AnalysisStore) InvalidateGoPackages(root string, packagePaths []string) []string {
	if s == nil {
		return nil
	}
	root = cleanAnalysisRoot(root, root)
	packageSet := analysisGoPackageDependencySet(packagePaths)

	s.mu.Lock()
	seen := make(map[string]struct{})
	for packagePath := range packageSet {
		key, ok := analysisGoPackageDependency(root, packagePath)
		if !ok {
			continue
		}
		for uri := range s.goPackageReverseDependencies[key] {
			if _, current := s.current[uri]; current {
				seen[uri] = struct{}{}
			}
		}
	}

	invalidated := make([]string, 0, len(seen))
	for uri := range seen {
		invalidated = append(invalidated, uri)
	}
	sort.Strings(invalidated)
	for _, uri := range invalidated {
		s.invalidateURILocked(uri, true)
	}

	for key, cached := range s.transient {
		if cached == nil || !samePath(cached.root, root) {
			continue
		}
		if transientDependsOnGoPackage(cached, packageSet) {
			delete(s.transient, key)
		}
	}

	s.semanticGeneration[root]++
	if s.semanticCache != nil {
		s.semanticCache.InvalidateRoot(root)
	}
	s.mu.Unlock()
	return invalidated
}

func (s *AnalysisStore) goPackageDependentURIs(root string, packagePaths []string) []string {
	if s == nil || len(packagePaths) == 0 {
		return nil
	}
	root = cleanAnalysisRoot(root, root)
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, packagePath := range packagePaths {
		key, ok := analysisGoPackageDependency(root, packagePath)
		if !ok {
			continue
		}
		for uri := range s.goPackageReverseDependencies[key] {
			if _, current := s.current[uri]; current {
				seen[uri] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for uri := range seen {
		result = append(result, uri)
	}
	sort.Strings(result)
	return result
}

func analysisGoPackageDependency(root, packagePath string) (analysisGoPackageDependencyKey, bool) {
	packagePath = strings.TrimSpace(packagePath)
	if packagePath == "" {
		return analysisGoPackageDependencyKey{}, false
	}
	return analysisGoPackageDependencyKey{
		Root:        cleanAnalysisRoot(root, root),
		PackagePath: packagePath,
	}, true
}

func (s *AnalysisStore) removeTransientURILocked(uri string) {
	for key := range s.transient {
		if key.URI == uri {
			delete(s.transient, key)
		}
	}
}

func (s *AnalysisStore) dependenciesChangedSinceLocked(snapshot *AnalysisSnapshot, revision uint64) bool {
	if snapshot == nil || s.dependencyRevision == revision {
		return false
	}
	for path := range analysisSnapshotDependencyPaths(snapshot) {
		if s.dependencyChanges[path] > revision {
			return true
		}
	}
	return false
}

func (s *AnalysisStore) transientDependenciesChangedSinceLocked(analysis *transientAnalysis, revision uint64) bool {
	if analysis == nil || s.dependencyRevision == revision {
		return false
	}
	snapshot := &AnalysisSnapshot{Document: analysis.document, Dependencies: analysis.dependencies}
	return s.dependenciesChangedSinceLocked(snapshot, revision)
}

func analysisSnapshotDependencyPaths(snapshot *AnalysisSnapshot) map[string]struct{} {
	paths := make(map[string]struct{})
	if snapshot == nil {
		return paths
	}
	add := func(path string) {
		if path != "" {
			paths[analysisDependencyPath(path)] = struct{}{}
		}
	}
	add(snapshot.Document.Path)
	for _, dependency := range snapshot.Dependencies {
		add(dependency.SourcePath)
		add(dependency.ResolvedPath)
		for _, candidate := range dependency.ResolutionCandidates {
			add(candidate)
		}
	}
	return paths
}

func sameAnalysisDocument(left, right Document) bool {
	return left.URI == right.URI && left.Path == right.Path && left.Version == right.Version && left.Text == right.Text
}

func sameAnalysisDocumentExceptText(left, right Document) bool {
	return left.URI == right.URI && left.Path == right.Path && left.LanguageID == right.LanguageID && left.Version == right.Version
}

func cleanAnalysisRoot(root, path string) string {
	if root == "" {
		return ""
	}
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	return filepath.Clean(root)
}

func analysisDependencyPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return filepath.Clean(path)
}

func snapshotDependsOnPath(snapshot *AnalysisSnapshot, path string) bool {
	if snapshot == nil {
		return false
	}
	path = analysisDependencyPath(path)
	if analysisDependencyPath(snapshot.Document.Path) == path {
		return true
	}
	for _, dependency := range snapshot.Dependencies {
		if dependency.ResolvedPath != "" && analysisDependencyPath(dependency.ResolvedPath) == path {
			return true
		}
		if dependency.SourcePath != "" && analysisDependencyPath(dependency.SourcePath) == path {
			return true
		}
		for _, candidate := range dependency.ResolutionCandidates {
			if candidate != "" && analysisDependencyPath(candidate) == path {
				return true
			}
		}
	}
	return false
}

func analysisGoPackageDependencySet(packagePaths []string) map[string]struct{} {
	packages := make(map[string]struct{}, len(packagePaths))
	for _, packagePath := range packagePaths {
		packagePath = strings.TrimSpace(packagePath)
		if packagePath != "" {
			packages[packagePath] = struct{}{}
		}
	}
	return packages
}

func transientDependsOnGoPackage(analysis *transientAnalysis, packagePaths map[string]struct{}) bool {
	if analysis == nil || len(packagePaths) == 0 {
		return false
	}
	for _, dependency := range analysis.dependencies {
		if dependency.Kind != goxcompiler.SemanticDependencyGoPackage {
			continue
		}
		if _, ok := packagePaths[strings.TrimSpace(dependency.PackagePath)]; ok {
			return true
		}
		if _, ok := packagePaths[strings.TrimSpace(dependency.ImportPath)]; ok {
			return true
		}
	}
	return false
}

func transientDependsOnPath(analysis *transientAnalysis, path string) bool {
	if analysis == nil {
		return false
	}
	snapshot := &AnalysisSnapshot{Document: analysis.document, Dependencies: analysis.dependencies}
	return snapshotDependsOnPath(snapshot, path)
}
