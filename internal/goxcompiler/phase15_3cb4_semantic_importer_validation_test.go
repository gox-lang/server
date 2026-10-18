package gox

import (
	"go/types"
	"path/filepath"
	"sync"
	"testing"
)

func TestPhase15_3CB4ConcurrentSharedImporterKeepsCanonicalIdentity(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")
	writeSemanticWorkspaceFile(t, filepath.Join(root, "model", "user.go"), `package model

type User struct{ Name string }
func Name(user User) string { return user.Name }
`)
	writeSemanticWorkspaceFile(t, filepath.Join(root, "service", "service.go"), `package service

import (
    "example.com/project/model"
    "io"
)

func Current() model.User { return model.User{Name: "George"} }
func Reader() io.Reader { return nil }
`)

	cache := NewSemanticWorkspaceCache()
	const workers = 12
	type result struct {
		service *types.Package
		model   *types.Package
		io      *types.Package
		err     error
	}
	results := make(chan result, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(workers)
	for index := 0; index < workers; index++ {
		index := index
		go func() {
			defer wg.Done()
			<-start
			path := filepath.Join(root, "pages", "concurrent-"+phase15CB4Decimal(index)+".gox")
			module, err := ParseProjectModuleCached(root, path, `module page
import "example.com/project/service"
import "example.com/project/model"
import "io"
func Accept(reader io.Reader) string { return "ok" }
func Build() string { return model.Name(service.Current()) + Accept(service.Reader()) }
`, cache)
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{
				service: semanticImportedPackage(module, "example.com/project/service"),
				model:   semanticImportedPackage(module, "example.com/project/model"),
				io:      semanticImportedPackage(module, "io"),
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var canonical result
	for current := range results {
		if current.err != nil {
			t.Fatal(current.err)
		}
		if current.service == nil || current.model == nil || current.io == nil {
			t.Fatalf("concurrent semantic imports missing: service=%p model=%p io=%p", current.service, current.model, current.io)
		}
		if canonical.service == nil {
			canonical = current
			continue
		}
		if current.service != canonical.service || current.model != canonical.model || current.io != canonical.io {
			t.Fatalf("shared importer identities diverged: service=%p/%p model=%p/%p io=%p/%p",
				current.service, canonical.service,
				current.model, canonical.model,
				current.io, canonical.io,
			)
		}
	}
}

func TestPhase15_3CB4InvalidationPreventsOldWorkspaceFromRepopulatingNewGeneration(t *testing.T) {
	root := t.TempDir()
	writeSemanticWorkspaceFile(t, filepath.Join(root, "go.mod"), "module example.com/project\n\ngo 1.24\n")

	cache := NewSemanticWorkspaceCache()
	oldWorkspace := newSemanticWorkspace(root, cache)
	oldGeneration := oldWorkspace.cacheGeneration
	oldImporter := oldWorkspace.goImporter
	if oldGeneration == 0 || oldImporter == nil {
		t.Fatalf("old workspace lease = generation %d importer %p", oldGeneration, oldImporter)
	}

	cache.InvalidateRoot(root)

	newWorkspace := newSemanticWorkspace(root, cache)
	if newWorkspace.cacheGeneration == 0 || newWorkspace.cacheGeneration == oldGeneration {
		t.Fatalf("root invalidation reused generation: old=%d new=%d", oldGeneration, newWorkspace.cacheGeneration)
	}
	if newWorkspace.goImporter == nil || newWorkspace.goImporter == oldImporter {
		t.Fatalf("root invalidation reused importer: old=%p new=%p", oldImporter, newWorkspace.goImporter)
	}

	newPackage, err := newWorkspace.loadGoPackage("strings")
	if err != nil {
		t.Fatal(err)
	}
	if newPackage == nil || !newPackage.Complete() {
		t.Fatalf("new generation strings package = %#v", newPackage)
	}

	oldPackage, err := oldWorkspace.loadGoPackage("strings")
	if err != nil {
		t.Fatal(err)
	}
	if oldPackage == nil || !oldPackage.Complete() {
		t.Fatalf("old generation strings package = %#v", oldPackage)
	}
	if oldPackage == newPackage {
		t.Fatal("old workspace observed the new generation type universe")
	}

	cached, ok := cache.GoPackage(root, "strings")
	if !ok || cached != newPackage {
		t.Fatalf("late old-generation analysis replaced current cache: cached=%p/%v new=%p old=%p", cached, ok, newPackage, oldPackage)
	}
	if _, ok := cache.goPackageForGeneration(root, "strings", oldGeneration); ok {
		t.Fatal("invalidated generation can still read current shared packages")
	}
}

func phase15CB4Decimal(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}
