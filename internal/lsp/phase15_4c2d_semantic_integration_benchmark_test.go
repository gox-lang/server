package lsp

import "testing"

func BenchmarkPhase15_4C2DGoEditToRebuiltSemanticSnapshot(b *testing.B) {
	root := phase15_4C2DProject(b, "example.com/benchmark")
	modelFile := phase15_4C2DWriteGoFile(b, root, "model/user.go", "package model\ntype User struct { Name string }\n")
	phase15_4C2DWriteGoFile(b, root, "service/service.go", `package service

import "example.com/benchmark/model"

func Current() model.User { return model.User{Name: "George"} }
`)
	server := NewServer()
	server.analysis = NewAnalysisStore()
	document := phase15_4C2DDocument(root, "index.gox", `module page
import "example.com/benchmark/service"
func Build() string { return service.Current().Name }
`)
	initial := phase15_4C2DRequireSnapshot(b, server.analysis.Snapshot(root, document))
	previous := phase15_4C2DImportedPackage(initial.Module, "example.com/benchmark/service")
	if previous == nil {
		b.Fatal("initial semantic service package is nil")
	}

	sources := []string{
		"package model\ntype User struct { Name string; Age int }\n",
		"package model\ntype User struct { Name string; Active bool }\n",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		b.StopTimer()
		phase15_4C2DWriteGoFile(b, root, "model/user.go", sources[index%len(sources)])
		b.StartTimer()

		if !server.handleWorkspaceFileChange(FileURI(modelFile), fileChanged) {
			b.Fatal("production .go change was not classified as Go-package-affecting")
		}
		snapshot := server.analysis.Snapshot(root, document)

		b.StopTimer()
		snapshot = phase15_4C2DRequireSnapshot(b, snapshot)
		current := phase15_4C2DImportedPackage(snapshot.Module, "example.com/benchmark/service")
		if current == nil || current == previous {
			b.Fatalf("semantic service identity did not rotate: previous=%p current=%p", previous, current)
		}
		previous = current
		b.StartTimer()
	}
}
