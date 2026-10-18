package lsp

import (
	"os"
	"path/filepath"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) analysisSnapshot(document Document) *AnalysisSnapshot {
	if s == nil {
		return nil
	}
	if s.analysis == nil {
		s.analysis = NewAnalysisStore()
	}
	return s.analysis.Snapshot(s.analysisProjectRoot(document.Path), document)
}

// analysisProjectRoot returns a structural root only when the LSP actually
// knows one. A directory inferred solely from the active file is not a project
// root: using it with ParseProjectModule can change websocket/pages
// classification (for example websocket/index.gox -> index.gox). Explicit
// workspace folders remain authoritative even when a focused test/project has
// no go.mod yet.
func (s *Server) analysisProjectRoot(path string) string {
	for index := len(s.workspaceRoots) - 1; index >= 0; index-- {
		root := s.workspaceRoots[index]
		if pathInside(root, path) || samePath(root, filepath.Dir(path)) {
			return root
		}
	}

	directory := filepath.Dir(path)
	for {
		if analysisRootMarkerExists(directory) {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return ""
}

func analysisRootMarkerExists(directory string) bool {
	for _, name := range []string{"go.mod", "go.work"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func (s *Server) analysisModule(document Document) (*goxcompiler.Module, error) {
	snapshot := s.analysisSnapshot(document)
	if snapshot == nil {
		return nil, nil
	}
	return snapshot.Module, snapshot.Err
}

func (s *Server) semanticModule(document Document) (*goxcompiler.Module, bool) {
	snapshot := s.analysisSnapshot(document)
	if snapshot == nil || snapshot.Err != nil || !snapshot.semanticReady() {
		return nil, false
	}
	return snapshot.Module, true
}

// parseTransientModule analyzes editor text that does not represent the
// canonical document version (for example a repaired dangling selector used by
// completion). Transient analyses are never inserted into AnalysisStore.
func (s *Server) parseTransientModule(document Document, source string) (*goxcompiler.Module, error) {
	if s == nil {
		return nil, nil
	}
	if s.analysis == nil {
		s.analysis = NewAnalysisStore()
	}
	return s.analysis.Transient(s.analysisProjectRoot(document.Path), document, source)
}
