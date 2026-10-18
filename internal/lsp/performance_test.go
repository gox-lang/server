package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestDependentDiagnosticsRefreshAfterImportedModuleChanges(t *testing.T) {
	root := t.TempDir()
	cardPath := filepath.Join(root, "card.gox")
	pagePath := filepath.Join(root, "page.gox")
	cardSource := "module card\n\nfunc Card() string { return \"card\" }\n"
	pageSource := "module page\n\nimport card \"card.gox\"\n\nfunc Page() string { return card.Card() }\n"
	if err := os.WriteFile(cardPath, []byte(cardSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o600); err != nil {
		t.Fatal(err)
	}
	cardJSON, _ := json.Marshal(cardSource)
	pageJSON, _ := json.Marshal(pageSource)
	panelJSON, _ := json.Marshal("module card\n\nfunc Panel() string { return \"panel\" }\n")
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":"` + FileURI(root) + `"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + FileURI(cardPath) + `","languageId":"gox","version":1,"text":` + string(cardJSON) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + FileURI(pagePath) + `","languageId":"gox","version":1,"text":` + string(pageJSON) + `}}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"` + FileURI(cardPath) + `","version":2},"contentChanges":[{"text":` + string(panelJSON) + `}]}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"` + FileURI(cardPath) + `","version":3},"contentChanges":[{"text":` + string(cardJSON) + `}]}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	// Exercise every intermediate diagnostic state; burst coalescing has its own protocol tests.
	server := NewServer()
	server.diagnosticsDebounce = 0
	if err := server.Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	var pageDiagnostics [][]Diagnostic
	for _, message := range decodeFramedMessages(t, output.String()) {
		if message.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var params PublishDiagnosticsParams
		if json.Unmarshal(message.Params, &params) == nil && params.URI == FileURI(pagePath) {
			pageDiagnostics = append(pageDiagnostics, params.Diagnostics)
		}
	}
	if len(pageDiagnostics) < 3 {
		t.Fatalf("page diagnostic publications = %#v", pageDiagnostics)
	}
	if len(pageDiagnostics[len(pageDiagnostics)-2]) == 0 {
		t.Fatal("renaming Card did not invalidate diagnostics in page.gox")
	}
	if len(pageDiagnostics[len(pageDiagnostics)-1]) != 0 {
		t.Fatalf("restoring Card left stale diagnostics: %#v", pageDiagnostics[len(pageDiagnostics)-1])
	}
}

func TestServerCancelsQueuedRequest(t *testing.T) {
	input := strings.Join([]string{
		framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"workspace/symbol","params":{"query":"Card"}}`),
		framedMessage(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":2}}`),
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
		framedMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}, "")
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	for _, response := range decodeFramedResponses(t, output.String()) {
		if string(response.ID) != "2" {
			continue
		}
		if response.Error == nil || response.Error.Code != -32800 || response.Error.Message != "Request cancelled" {
			t.Fatalf("cancelled response = %#v", response)
		}
		return
	}
	t.Fatal("missing response for cancelled request")
}

func TestGoPackageIndexInvalidatesOnlyChangedRoot(t *testing.T) {
	index := NewGoPackageIndex()
	index.discovery["/workspace/one"] = []GoPackageCandidate{{Path: "fmt", Name: "fmt"}}
	index.discovery["/workspace/two"] = []GoPackageCandidate{{Path: "strings", Name: "strings"}}
	index.cache["/workspace/one\x00fmt"] = goPackageCacheEntry{}
	index.cache["/workspace/two\x00strings"] = goPackageCacheEntry{}

	index.InvalidateRoot("/workspace/one")

	if _, ok := index.discovery["/workspace/one"]; ok {
		t.Fatal("changed root discovery cache was preserved")
	}
	if _, ok := index.cache["/workspace/one\x00fmt"]; ok {
		t.Fatal("changed root package cache was preserved")
	}
	if _, ok := index.discovery["/workspace/two"]; !ok {
		t.Fatal("unrelated discovery cache was invalidated")
	}
	if _, ok := index.cache["/workspace/two\x00strings"]; !ok {
		t.Fatal("unrelated package cache was invalidated")
	}
}

func TestWorkspaceGoChangesInvalidateTheAffectedPackageIndex(t *testing.T) {
	for _, test := range []struct {
		name       string
		changeType int
		deleteFile bool
		wantLocal  bool
	}{
		{name: "created", changeType: fileCreated, wantLocal: true},
		{name: "changed", changeType: fileChanged, wantLocal: true},
		{name: "deleted", changeType: fileDeleted, deleteFile: true, wantLocal: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			packagePath := filepath.Join(root, "billing", "register.go")
			if err := os.MkdirAll(filepath.Dir(packagePath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(packagePath, []byte("package register\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			otherRoot := t.TempDir()
			server := NewServer()
			root = filepath.Clean(root)
			otherRoot = filepath.Clean(otherRoot)
			localPath := "example/billing"
			otherPath := "example/other"
			server.goPackages.discovery[root] = []GoPackageCandidate{
				{Path: "strings", Name: "strings"},
				{Path: localPath, Name: "register"},
			}
			server.goPackages.discovery[otherRoot] = []GoPackageCandidate{{Path: otherPath, Name: "other"}}
			server.goPackages.cache[root+"\x00"+localPath] = goPackageCacheEntry{}
			server.goPackages.packageNames[root+"\x00"+localPath] = "register"
			server.goPackages.cache[otherRoot+"\x00"+otherPath] = goPackageCacheEntry{}
			server.goPackages.packageNames[otherRoot+"\x00"+otherPath] = "other"

			if test.deleteFile {
				if err := os.Remove(packagePath); err != nil {
					t.Fatal(err)
				}
			}
			if !server.handleWorkspaceFileChange(FileURI(packagePath), test.changeType) {
				t.Fatalf("Go package event type %d was not recognized", test.changeType)
			}

			candidates, ok := server.goPackages.discovery[root]
			if !ok {
				t.Fatalf("Go package discovery cache was discarded for event type %d", test.changeType)
			}
			localFound := false
			stdlibFound := false
			for _, candidate := range candidates {
				switch candidate.Path {
				case localPath:
					localFound = candidate.Name == "register"
				case "strings":
					stdlibFound = candidate.Name == "strings"
				}
			}
			if localFound != test.wantLocal {
				t.Fatalf("local discovery presence after event type %d = %v, want %v", test.changeType, localFound, test.wantLocal)
			}
			if !stdlibFound {
				t.Fatal("patching local discovery disturbed stdlib candidate")
			}
			if _, ok := server.goPackages.cache[root+"\x00"+localPath]; ok {
				t.Fatalf("Go package cache survived event type %d", test.changeType)
			}
			if _, ok := server.goPackages.packageNames[root+"\x00"+localPath]; ok {
				t.Fatalf("Go package-name cache survived event type %d", test.changeType)
			}
			if _, ok := server.goPackages.discovery[otherRoot]; !ok {
				t.Fatal("unrelated workspace package discovery was invalidated")
			}
			if _, ok := server.goPackages.cache[otherRoot+"\x00"+otherPath]; !ok {
				t.Fatal("unrelated workspace package cache was invalidated")
			}
			if _, ok := server.goPackages.packageNames[otherRoot+"\x00"+otherPath]; !ok {
				t.Fatal("unrelated workspace package-name cache was invalidated")
			}
		})
	}
}

func TestWorkspaceGoWorkChangeInvalidatesAllPackageIndexes(t *testing.T) {
	server := NewServer()
	server.goPackages.discovery["/workspace/one"] = []GoPackageCandidate{{Path: "example/one", Name: "one"}}
	server.goPackages.discovery["/workspace/two"] = []GoPackageCandidate{{Path: "example/two", Name: "two"}}
	server.goPackages.cache["/workspace/one\x00example/one"] = goPackageCacheEntry{}
	server.goPackages.cache["/workspace/two\x00example/two"] = goPackageCacheEntry{}

	if !server.handleWorkspaceFileChange(FileURI(filepath.Join(t.TempDir(), "go.work")), fileChanged) {
		t.Fatal("go.work change was not recognized")
	}

	if len(server.goPackages.discovery) != 0 || len(server.goPackages.cache) != 0 {
		t.Fatalf("go.work change preserved package indexes: %#v %#v", server.goPackages.discovery, server.goPackages.cache)
	}
}

func TestWorkspaceGoModChangeInvalidatesItsRootAfterDeletion(t *testing.T) {
	root := t.TempDir()
	server := NewServer()
	server.goPackages.discovery[root] = []GoPackageCandidate{{Path: "example/billing", Name: "register"}}
	server.goPackages.cache[root+"\x00example/billing"] = goPackageCacheEntry{}

	if !server.handleWorkspaceFileChange(FileURI(filepath.Join(root, "go.mod")), fileDeleted) {
		t.Fatal("go.mod deletion was not recognized")
	}
	if _, ok := server.goPackages.discovery[root]; ok {
		t.Fatal("go.mod deletion preserved package discovery")
	}
	if _, ok := server.goPackages.cache[root+"\x00example/billing"]; ok {
		t.Fatal("go.mod deletion preserved package cache")
	}
}

func BenchmarkWorkspaceIndexIncrementalUpdate(b *testing.B) {
	index := NewWorkspaceIndex()
	for documentIndex := 0; documentIndex < 500; documentIndex++ {
		name := string(rune('A' + documentIndex%26))
		document := Document{
			URI:  "file:///workspace/component" + name + ".gox",
			Path: "/workspace/component" + name + ".gox",
			Text: "module component\n\nfunc Component" + name + "() string { return \"ok\" }\n",
		}
		_ = index.UpdateDisk(document)
	}
	document := Document{
		URI:  "file:///workspace/current.gox",
		Path: "/workspace/current.gox",
		Text: "module current\n\nfunc Current() string { return \"ok\" }\n",
	}
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if err := index.UpdateOpen(document); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPhase14WorkspaceGoChangeInvalidatesOnlyAffectedSemanticRoot(t *testing.T) {
	rootOne := t.TempDir()
	rootTwo := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootOne, "go.mod"), []byte("module example.com/one\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootTwo, "go.mod"), []byte("module example.com/two\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedGo := filepath.Join(rootOne, "domain", "user.go")
	if err := os.MkdirAll(filepath.Dir(changedGo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(changedGo, []byte("package domain\ntype User struct{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	onePath := filepath.Join(rootOne, "page.gox")
	twoPath := filepath.Join(rootTwo, "page.gox")
	one := Document{URI: FileURI(onePath), Path: onePath, Version: 1, Text: "module one\n"}
	two := Document{URI: FileURI(twoPath), Path: twoPath, Version: 1, Text: "module two\n"}
	server := NewServer()
	server.analysis = newAnalysisStore(func(root string, document Document) (*goxcompiler.Module, error) {
		module := &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{}}
		if samePath(root, rootOne) && document.URI == one.URI {
			module.Semantic.Dependencies = []goxcompiler.SemanticDependency{{
				SourcePath:  document.Path,
				Kind:        goxcompiler.SemanticDependencyGoPackage,
				ImportPath:  "example.com/one/domain",
				PackagePath: "example.com/one/domain",
			}}
		}
		return module, nil
	})
	server.analysis.Snapshot(rootOne, one)
	server.analysis.Snapshot(rootTwo, two)

	if !server.handleWorkspaceFileChange(FileURI(changedGo), fileChanged) {
		t.Fatal("Go file change was not recognized")
	}
	if _, ok := server.analysis.Current(one.URI); ok {
		t.Fatal("snapshot depending on changed Go package survived invalidation")
	}
	if _, ok := server.analysis.Current(two.URI); !ok {
		t.Fatal("unrelated semantic root was invalidated by Go file change")
	}
}
