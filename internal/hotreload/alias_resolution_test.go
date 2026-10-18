package hotreload

import (
	"path/filepath"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestRegisterCompileResultIndexesMissingAliasCandidates(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "pages", "dashboard", "index.gox")
	candidate := filepath.Join(root, "pages", "dashboard", "@layout.gox")
	idx := NewIndex()
	idx.RegisterCompileResult(ReloadPage, &goxcompiler.CompileResult{
		EntryPath: entry, EntryAbsPath: entry,
		Dependencies:           []goxcompiler.CompileDependency{{AbsPath: entry, IsEntry: true}},
		ResolutionDependencies: []string{candidate},
	})
	deps, ok := idx.Dependents(candidate)
	if !ok || len(deps.Entries) != 1 || deps.Entries[0].EntryAbsPath != entry {
		t.Fatalf("dependents = %#v, %v", deps, ok)
	}
}
