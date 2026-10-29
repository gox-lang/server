package serverruntime

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/toolchain"
)

// Failed entries remain pending: fixing another package must not hide an
// earlier failure. Watcher callbacks can arrive concurrently for different files.
type goSourcePreflight struct {
	buildMu    sync.Mutex
	mu         sync.Mutex
	generation uint64
	pending    map[string]hotreload.ReloadEntry
	validate   func([]hotreload.ReloadEntry) error // injectable compiler for focused tests
}

func (r *Runtime) validateGoSourceRestart(entries []hotreload.ReloadEntry) (validated []hotreload.ReloadEntry, validationErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			validated = nil
			validationErr = fmt.Errorf("restart compilation failed unexpectedly: %v", recovered)
		}
	}()

	state := &r.goPreflight
	state.mu.Lock()
	state.generation++
	if state.pending == nil {
		state.pending = make(map[string]hotreload.ReloadEntry)
	}
	for _, entry := range entries {
		state.pending[entry.EntryAbsPath] = entry
	}
	state.mu.Unlock()

	state.buildMu.Lock()
	defer state.buildMu.Unlock()
	state.mu.Lock()
	generation := state.generation
	candidates := make([]hotreload.ReloadEntry, 0, len(state.pending))
	for _, entry := range state.pending {
		candidates = append(candidates, entry)
	}
	state.mu.Unlock()
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].EntryAbsPath < candidates[j].EntryAbsPath
	})

	if err := goxcompiler.ValidateProjectGoGoroutineSafety(r.projectRoot()); err != nil {
		return nil, err
	}
	before, err := goSourceFingerprint(r.projectRoot())
	if err != nil {
		return nil, err
	}
	validate := state.validate
	if validate == nil {
		validate = r.compileGoSourceCandidates
	}
	if err := validate(candidates); err != nil {
		return nil, err
	}
	after, err := goSourceFingerprint(r.projectRoot())
	if err != nil {
		return nil, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if generation != state.generation || before != after {
		return nil, fmt.Errorf("sources changed during compilation; waiting for the next source event")
	}
	// Do not clear pending until process replacement. Another callback may have
	// been debounced before this validation completed.
	return candidates, nil
}

func (r *Runtime) compileGoSourceCandidates(entries []hotreload.ReloadEntry) error {
	if r.paths.GoExec == "" {
		return fmt.Errorf("Go compiler is unavailable for restart validation")
	}
	buildRoot := filepath.Join(r.projectRoot(), ".build")
	if err := os.MkdirAll(buildRoot, 0755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(buildRoot, "restart-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	for i, entry := range entries {
		if entry.Kind == hotreload.ReloadClient {
			continue
		}
		generated := filepath.Join(dir, fmt.Sprintf("entry-%d.go", i))
		artifact := filepath.Join(dir, fmt.Sprintf("entry-%d.so", i))
		if err := goxcompiler.CompileForValidation(entry.EntryAbsPath, generated); err != nil {
			return fmt.Errorf("%s compile failed: %w", entry.EntryPath, err)
		}
		cmd := toolchain.Command(r.paths, goxPluginBuildArgs(artifact, generated)...)
		cmd.Dir = r.projectRoot()
		var diagnostic bytes.Buffer
		cmd.Stdout, cmd.Stderr = &diagnostic, &diagnostic
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s build failed: %w\n%s", entry.EntryPath, err, diagnostic.String())
		}
	}
	return nil
}

// Hash source contents rather than just mtimes: an editor can replace files
// atomically or preserve timestamps while changing signatures.
func goSourceFingerprint(root string) ([32]byte, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, item fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.IsDir() {
			if path != root {
				switch item.Name() {
				case ".build", ".git", ".toolchain", "node_modules", "vendor":
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := item.Name()
		if !strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".gox") && name != "go.mod" && name != "go.sum" && name != "go.work" && name != "go.work.sum" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		io.WriteString(hash, rel)
		hash.Write([]byte{0})
		sum := sha256.Sum256(data)
		hash.Write(sum[:])
		return nil
	})
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest, err
}

// A GOX edit may repair the consumer of a changed Go signature. Do not load
// that consumer into the old process while a Go restart is pending.
func (r *Runtime) hasPendingGoSourceRestart() bool {
	r.goPreflight.mu.Lock()
	defer r.goPreflight.mu.Unlock()
	return len(r.goPreflight.pending) > 0
}
