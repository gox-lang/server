package lsp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPhase15_4B2bPersistentGraphPatchesOnlyChangedPackageEdges(t *testing.T) {
	root := phase15_4AProjectRoot(t, "persistent")
	modelFile := filepath.Join(root, "model", "model.go")
	serviceFile := filepath.Join(root, "service", "service.go")
	apiFile := filepath.Join(root, "api", "api.go")
	otherFile := filepath.Join(root, "other", "other.go")
	phase15_4B2WriteGoFile(t, modelFile, "package model\ntype User struct{}\n")
	phase15_4B2WriteGoFile(t, serviceFile, "package service\nimport \"example.com/persistent/model\"\nvar _ model.User\n")
	phase15_4B2WriteGoFile(t, apiFile, "package api\nimport \"example.com/persistent/service\"\nvar _ = service.Value\n")
	phase15_4B2WriteGoFile(t, filepath.Join(root, "service", "value.go"), "package service\nfunc Value() string { return \"ok\" }\n")
	phase15_4B2WriteGoFile(t, otherFile, "package other\nfunc Value() string { return \"ok\" }\n")

	index := NewGoPackageIndex()
	first, ok := index.ProjectGoPackageInvalidationForFile(root, modelFile)
	if !ok {
		t.Fatal("failed to build persistent local import graph")
	}
	wantFirst := []string{"example.com/persistent/api", "example.com/persistent/model", "example.com/persistent/service"}
	if !reflect.DeepEqual(first.Affected, wantFirst) {
		t.Fatalf("initial affected closure = %#v, want %#v", first.Affected, wantFirst)
	}

	root = filepath.Clean(root)
	graph := index.projectGraphs[root]
	if graph == nil {
		t.Fatal("persistent graph was not cached after first planning pass")
	}
	if got := len(graph.packages); got != 4 {
		t.Fatalf("cached package count = %d, want 4", got)
	}

	// Remove service -> model. Planning service must patch just service's
	// outgoing edges; a later model change must no longer invalidate service/api.
	phase15_4B2WriteGoFile(t, serviceFile, "package service\nfunc Current() string { return \"ok\" }\n")
	if _, ok := index.ProjectGoPackageInvalidationForFile(root, serviceFile); !ok {
		t.Fatal("failed to patch service package in persistent graph")
	}
	if index.projectGraphs[root] != graph {
		t.Fatal("incremental planning replaced the root graph instead of patching it")
	}
	modelOnly, ok := index.ProjectGoPackageInvalidationForFile(root, modelFile)
	if !ok {
		t.Fatal("failed to plan model after service import removal")
	}
	if want := []string{"example.com/persistent/model"}; !reflect.DeepEqual(modelOnly.Affected, want) {
		t.Fatalf("affected closure after removing import = %#v, want %#v", modelOnly.Affected, want)
	}

	// Add the edge back. The same cached graph must immediately restore the
	// transitive reverse closure without rescanning unrelated packages.
	phase15_4B2WriteGoFile(t, serviceFile, "package service\nimport \"example.com/persistent/model\"\nvar _ model.User\n")
	if _, ok := index.ProjectGoPackageInvalidationForFile(root, serviceFile); !ok {
		t.Fatal("failed to restore service import in persistent graph")
	}
	restored, ok := index.ProjectGoPackageInvalidationForFile(root, modelFile)
	if !ok {
		t.Fatal("failed to plan model after restoring import")
	}
	if !reflect.DeepEqual(restored.Affected, wantFirst) {
		t.Fatalf("restored affected closure = %#v, want %#v", restored.Affected, wantFirst)
	}
}

func TestPhase15_4B2bCreateAndDeletePatchPersistentGraphWithExistingImporters(t *testing.T) {
	root := phase15_4AProjectRoot(t, "create-delete")
	serviceFile := filepath.Join(root, "service", "service.go")
	apiFile := filepath.Join(root, "api", "api.go")
	freshFile := filepath.Join(root, "fresh", "fresh.go")
	phase15_4B2WriteGoFile(t, serviceFile, "package service\nimport \"example.com/create-delete/fresh\"\nvar _ fresh.Value\n")
	phase15_4B2WriteGoFile(t, apiFile, "package api\nimport \"example.com/create-delete/service\"\nvar _ = service.Value\n")

	index := NewGoPackageIndex()
	if _, ok := index.ProjectGoPackageInvalidationForFile(root, serviceFile); !ok {
		t.Fatal("failed to build graph containing unresolved local import")
	}

	phase15_4B2WriteGoFile(t, freshFile, "package fresh\ntype Value struct{}\n")
	created, ok := index.ProjectGoPackageInvalidationForFile(root, freshFile)
	if !ok {
		t.Fatal("failed to patch newly created package")
	}
	want := []string{"example.com/create-delete/api", "example.com/create-delete/fresh", "example.com/create-delete/service"}
	if !reflect.DeepEqual(created.Affected, want) {
		t.Fatalf("created package closure = %#v, want %#v", created.Affected, want)
	}
	if created.Candidate == nil || created.Candidate.Path != "example.com/create-delete/fresh" || created.Candidate.Name != "fresh" {
		t.Fatalf("created candidate = %#v", created.Candidate)
	}

	if err := os.Remove(freshFile); err != nil {
		t.Fatal(err)
	}
	deleted, ok := index.ProjectGoPackageInvalidationForFile(root, freshFile)
	if !ok {
		t.Fatal("failed to patch deleted package")
	}
	if !reflect.DeepEqual(deleted.Affected, want) {
		t.Fatalf("deleted package closure = %#v, want %#v", deleted.Affected, want)
	}
	if deleted.Candidate != nil {
		t.Fatalf("deleted package candidate = %#v, want nil", deleted.Candidate)
	}
	graph := index.projectGraphs[filepath.Clean(root)]
	if graph == nil {
		t.Fatal("persistent graph disappeared after package deletion")
	}
	graph.mu.RLock()
	_, packageStillPresent := graph.packages["example.com/create-delete/fresh"]
	_, incomingStillPresent := graph.reverse["example.com/create-delete/fresh"]
	graph.mu.RUnlock()
	if packageStillPresent {
		t.Fatal("deleted package node remained in persistent graph")
	}
	if !incomingStillPresent {
		t.Fatal("reverse importers of deleted package were lost; importer caches would not be invalidated")
	}
}

func TestPhase15_4B2bRootAndGlobalInvalidationDropPersistentGraphs(t *testing.T) {
	rootOne := phase15_4AProjectRoot(t, "one")
	rootTwo := phase15_4AProjectRoot(t, "two")
	fileOne := filepath.Join(rootOne, "pkg", "pkg.go")
	fileTwo := filepath.Join(rootTwo, "pkg", "pkg.go")
	phase15_4B2WriteGoFile(t, fileOne, "package pkg\n")
	phase15_4B2WriteGoFile(t, fileTwo, "package pkg\n")

	index := NewGoPackageIndex()
	if _, ok := index.ProjectGoPackageInvalidationForFile(rootOne, fileOne); !ok {
		t.Fatal("failed to seed root one graph")
	}
	if _, ok := index.ProjectGoPackageInvalidationForFile(rootTwo, fileTwo); !ok {
		t.Fatal("failed to seed root two graph")
	}
	rootOne = filepath.Clean(rootOne)
	rootTwo = filepath.Clean(rootTwo)
	if index.projectGraphs[rootOne] == nil || index.projectGraphs[rootTwo] == nil {
		t.Fatal("failed to seed both persistent graphs")
	}

	index.InvalidateRoot(rootOne)
	if index.projectGraphs[rootOne] != nil {
		t.Fatal("InvalidateRoot preserved stale local import graph")
	}
	if index.projectGraphs[rootTwo] == nil {
		t.Fatal("InvalidateRoot disturbed unrelated root graph")
	}

	index.Invalidate()
	if len(index.projectGraphs) != 0 {
		t.Fatalf("global invalidation preserved %d project graphs", len(index.projectGraphs))
	}
}

func TestPhase15_4B2bInvalidPackageFallsBackInsteadOfMutatingPersistentGraph(t *testing.T) {
	root := phase15_4AProjectRoot(t, "fallback")
	file := filepath.Join(root, "pkg", "one.go")
	phase15_4B2WriteGoFile(t, file, "package pkg\n")
	index := NewGoPackageIndex()
	if _, ok := index.ProjectGoPackageInvalidationForFile(root, file); !ok {
		t.Fatal("failed to seed persistent graph")
	}
	graph := index.projectGraphs[filepath.Clean(root)]
	if graph == nil {
		t.Fatal("persistent graph was not cached")
	}

	phase15_4B2WriteGoFile(t, filepath.Join(root, "pkg", "two.go"), "package conflict\n")
	if _, ok := index.ProjectGoPackageInvalidationForFile(root, file); ok {
		t.Fatal("conflicting active package clauses should force conservative root invalidation")
	}
	if index.projectGraphs[filepath.Clean(root)] != graph {
		t.Fatal("failed incremental scan mutated/replaced graph before conservative fallback")
	}
}

func TestPhase15_4B2bWarmPlanningDoesNotRescanUnrelatedWorkspacePackages(t *testing.T) {
	root := phase15_4AProjectRoot(t, "no-rescan")
	modelFile := filepath.Join(root, "model", "model.go")
	unrelatedFile := filepath.Join(root, "unrelated", "unrelated.go")
	phase15_4B2WriteGoFile(t, modelFile, "package model\ntype User struct{}\n")
	phase15_4B2WriteGoFile(t, unrelatedFile, "package unrelated\n")

	index := NewGoPackageIndex()
	if _, ok := index.ProjectGoPackageInvalidationForFile(root, modelFile); !ok {
		t.Fatal("failed to seed persistent graph")
	}

	// Make an unrelated directory impossible to rescan through build.MatchFile.
	// The next model event must still succeed because B2b only rereads model.
	phase15_4B2WriteGoFile(t, unrelatedFile, "//go:build (\n\npackage unrelated\n")
	phase15_4B2WriteGoFile(t, modelFile, "package model\ntype User struct{ Name string }\n")
	if _, ok := index.ProjectGoPackageInvalidationForFile(root, modelFile); !ok {
		t.Fatal("warm planning rescanned an unrelated package instead of patching only the changed directory")
	}
}
