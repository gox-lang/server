package gox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPhase16_2CProjectGoSafetyCacheDetectsDirectFileChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "worker", "worker.go")
	writePhase16CFile(t, path, safePhase16CWorker("work"))
	InvalidateProjectGoGoroutineSafety(root, "")
	if err := validateProjectGoGoroutineSafety(root); err != nil {
		t.Fatalf("prime safety cache: %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	writePhase16CFile(t, path, unsafePhase16CWorker("work"))
	if err := validateProjectGoGoroutineSafety(root); err == nil {
		t.Fatal("expected direct file change to invalidate cached safety result")
	}
}

func TestPhase16_2CProjectGoSafetyCacheReanalyzesWholeChangedPackage(t *testing.T) {
	root := t.TempDir()
	start := filepath.Join(root, "worker", "start.go")
	helper := filepath.Join(root, "worker", "recover.go")
	writePhase16CFile(t, start, `package worker

func start() { go worker() }
func worker() {
	defer recoverPanic()
	work()
}
func work() {}
`)
	writePhase16CFile(t, helper, `package worker
func recoverPanic() { recover() }
`)
	InvalidateProjectGoGoroutineSafety(root, "")
	if err := validateProjectGoGoroutineSafety(root); err != nil {
		t.Fatalf("prime safety cache: %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	writePhase16CFile(t, helper, `package worker
func recoverPanic() {}
`)
	if err := validateProjectGoGoroutineSafety(root); err == nil {
		t.Fatal("expected helper change to make goroutine unsafe")
	}
}

func TestPhase16_2CProjectGoSafetyExplicitInvalidationHandlesStableMetadata(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "worker", "worker.go")
	safe := safePhase16CWorker("aaaa")
	unsafe := unsafePhase16CWorker("bbbb")
	if len(unsafe) > len(safe) {
		t.Fatalf("unsafe fixture unexpectedly larger: safe=%d unsafe=%d", len(safe), len(unsafe))
	}
	unsafe += strings.Repeat(" ", len(safe)-len(unsafe))
	writePhase16CFile(t, path, safe)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	InvalidateProjectGoGoroutineSafety(root, "")
	if err := validateProjectGoGoroutineSafety(root); err != nil {
		t.Fatalf("prime safety cache: %v", err)
	}

	writePhase16CFile(t, path, unsafe)
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("restore modtime: %v", err)
	}
	InvalidateProjectGoGoroutineSafety(root, path)
	if err := validateProjectGoGoroutineSafety(root); err == nil {
		t.Fatal("expected explicit invalidation to force reanalysis")
	}
}

func TestPhase16_2CProjectGoSafetyCacheDetectsCreateAndDelete(t *testing.T) {
	root := t.TempDir()
	safePath := filepath.Join(root, "worker", "worker.go")
	unsafePath := filepath.Join(root, "worker", "extra.go")
	writePhase16CFile(t, safePath, "package worker\nfunc work() {}\n")
	InvalidateProjectGoGoroutineSafety(root, "")
	if err := validateProjectGoGoroutineSafety(root); err != nil {
		t.Fatalf("prime safety cache: %v", err)
	}

	writePhase16CFile(t, unsafePath, `package worker
func start() { go func() { work() }() }
`)
	if err := validateProjectGoGoroutineSafety(root); err == nil {
		t.Fatal("expected newly created unsafe file to be detected")
	}
	if err := os.Remove(unsafePath); err != nil {
		t.Fatal(err)
	}
	if err := validateProjectGoGoroutineSafety(root); err != nil {
		t.Fatalf("expected deleting unsafe file to restore safety: %v", err)
	}
}

func TestPhase16_2CProjectGoSafetyCacheIgnoresTestFileChanges(t *testing.T) {
	root := t.TempDir()
	writePhase16CFile(t, filepath.Join(root, "worker", "worker.go"), "package worker\nfunc work() {}\n")
	testPath := filepath.Join(root, "worker", "worker_test.go")
	writePhase16CFile(t, testPath, "package worker\n")
	InvalidateProjectGoGoroutineSafety(root, "")
	if err := validateProjectGoGoroutineSafety(root); err != nil {
		t.Fatalf("prime safety cache: %v", err)
	}

	writePhase16CFile(t, testPath, `package worker
func unsafeTestOnly() { go func() {}() }
`)
	if err := validateProjectGoGoroutineSafety(root); err != nil {
		t.Fatalf("test-only Go change affected production safety: %v", err)
	}
}

func TestPhase16_2CInvalidationRejectsStalePublication(t *testing.T) {
	root := normalizeProjectGoSafetyRoot(t.TempDir())
	dir := filepath.Join(root, "worker")
	path := filepath.Join(dir, "worker.go")
	writePhase16CFile(t, path, "package worker\nfunc work() {}\n")
	InvalidateProjectGoGoroutineSafety(root, "")
	if err := validateProjectGoGoroutineSafety(root); err != nil {
		t.Fatalf("prime safety cache: %v", err)
	}

	lease, generation, cached := projectGoSafetyRootSnapshot(root)
	stale := make(map[string]projectGoSafetyDirState, len(cached))
	for key, value := range cached {
		stale[key] = value
	}
	InvalidateProjectGoGoroutineSafety(root, path)
	projectGoSafetyStoreRoot(root, lease, generation, stale)

	_, _, current := projectGoSafetyRootSnapshot(root)
	if _, ok := current[dir]; ok {
		t.Fatal("stale pre-invalidation directory state was republished")
	}
}

func TestPhase16_2CProjectGoSafetyCacheIsRootScoped(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	pathA := filepath.Join(rootA, "worker", "worker.go")
	pathB := filepath.Join(rootB, "worker", "worker.go")
	writePhase16CFile(t, pathA, "package worker\nfunc work() {}\n")
	writePhase16CFile(t, pathB, "package worker\nfunc work() {}\n")
	InvalidateProjectGoGoroutineSafety(rootA, "")
	InvalidateProjectGoGoroutineSafety(rootB, "")
	if err := validateProjectGoGoroutineSafety(rootA); err != nil {
		t.Fatal(err)
	}
	if err := validateProjectGoGoroutineSafety(rootB); err != nil {
		t.Fatal(err)
	}

	InvalidateProjectGoGoroutineSafety(rootA, pathA)
	_, _, cachedA := projectGoSafetyRootSnapshot(normalizeProjectGoSafetyRoot(rootA))
	_, _, cachedB := projectGoSafetyRootSnapshot(normalizeProjectGoSafetyRoot(rootB))
	if _, ok := cachedA[filepath.Dir(pathA)]; ok {
		t.Fatal("root A directory survived its invalidation")
	}
	if _, ok := cachedB[filepath.Dir(pathB)]; !ok {
		t.Fatal("root A invalidation evicted root B")
	}
}

func BenchmarkPhase16_2CProjectGoSafetyCache(b *testing.B) {
	root, changedPath := phase16CProjectFixture(b, 257)

	b.Run("Cold/GoFiles257", func(b *testing.B) {
		b.StopTimer()
		for i := 0; i < b.N; i++ {
			InvalidateProjectGoGoroutineSafety(root, "")
			b.StartTimer()
			if err := validateProjectGoGoroutineSafety(root); err != nil {
				b.Fatal(err)
			}
			b.StopTimer()
		}
	})

	b.Run("Warm/GoFiles257", func(b *testing.B) {
		InvalidateProjectGoGoroutineSafety(root, "")
		if err := validateProjectGoGoroutineSafety(root); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := validateProjectGoGoroutineSafety(root); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("OneDirChanged/GoFiles257", func(b *testing.B) {
		b.StopTimer()
		for i := 0; i < b.N; i++ {
			InvalidateProjectGoGoroutineSafety(root, "")
			if err := validateProjectGoGoroutineSafety(root); err != nil {
				b.Fatal(err)
			}
			InvalidateProjectGoGoroutineSafety(root, changedPath)
			b.StartTimer()
			if err := validateProjectGoGoroutineSafety(root); err != nil {
				b.Fatal(err)
			}
			b.StopTimer()
		}
	})
}

func phase16CProjectFixture(tb testing.TB, count int) (string, string) {
	tb.Helper()
	root := tb.TempDir()
	var changedPath string
	for i := 0; i < count; i++ {
		path := filepath.Join(root, "packages", fmt.Sprintf("p%03d", i), "value.go")
		writePhase16CTBFile(tb, path, fmt.Sprintf("package p%03d\n\nfunc Value() int { return %d }\n", i, i))
		if i == count/2 {
			changedPath = path
		}
	}
	return root, changedPath
}

func safePhase16CWorker(marker string) string {
	return fmt.Sprintf(`package worker
func start() { go func() { defer func() { recover() }(); work%[1]s() }() }
func work%[1]s() {}
`, marker)
}

func unsafePhase16CWorker(marker string) string {
	return fmt.Sprintf(`package worker
func start() { go func() { work%[1]s(); /* no recovery barrier */ }() }
func work%[1]s() {}
`, marker)
}

func writePhase16CFile(t *testing.T, path, contents string) {
	t.Helper()
	writePhase16CTBFile(t, path, contents)
}

func writePhase16CTBFile(tb testing.TB, path, contents string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		tb.Fatal(err)
	}
}
