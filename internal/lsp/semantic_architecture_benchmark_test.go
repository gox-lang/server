package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func benchmarkSemanticArchitectureDocument(b *testing.B, source string) (*Server, Document) {
	b.Helper()
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(root, "page.gox")
	document := Document{URI: FileURI(path), Path: path, Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		b.Fatal(err)
	}
	return server, document
}

func benchmarkPosition(b *testing.B, document Document, marker string) Position {
	b.Helper()
	offset := strings.LastIndex(document.Text, marker)
	if offset < 0 {
		b.Fatalf("marker %q not found", marker)
	}
	position, err := document.Position(offset + len(marker))
	if err != nil {
		b.Fatal(err)
	}
	return position
}

func BenchmarkSemanticArchitectureCompletionLocalColdAutoImport(b *testing.B) {
	_, document := benchmarkSemanticArchitectureDocument(b, `module page
import "strings"
func Build() {
    text := "hello"
    _ = strings.NewReplacer("a", "b").Replace(te)
}
`)
	position := benchmarkPosition(b, document, "te")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		server := NewServer()
		if err := server.symbols.UpdateOpen(document); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		_ = server.completions(document, position)
	}
}

func BenchmarkSemanticArchitectureCompletionGlobalExportColdAutoImport(b *testing.B) {
	_, document := benchmarkSemanticArchitectureDocument(b, `module page
func Build() {
    Spl
}
`)
	position := benchmarkPosition(b, document, "Spl")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		server := NewServer()
		if err := server.symbols.UpdateOpen(document); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		_ = server.completions(document, position)
	}
}

func BenchmarkSemanticArchitectureCompletionGlobalExportWarm(b *testing.B) {
	server, document := benchmarkSemanticArchitectureDocument(b, `module page
func Build() {
    Spl
}
`)
	position := benchmarkPosition(b, document, "Spl")

	// Warm both the shallow global export-name index and the detailed metadata
	// for packages that actually match Spl. Timed iterations then measure the
	// steady-state prefix-filtering and lazy-detail cache path.
	_ = server.completions(document, position)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.completions(document, position)
	}
}

func BenchmarkSemanticArchitectureCompletionLocalWarm(b *testing.B) {
	server, document := benchmarkSemanticArchitectureDocument(b, `module page
import "strings"
func Build() {
    text := "hello"
    _ = strings.NewReplacer("a", "b").Replace(te)
}
`)
	position := benchmarkPosition(b, document, "te")

	// The first unqualified lexical completion initializes the Go package and
	// dependency auto-import indexes. Warm that one-time work before timing so
	// this benchmark measures the steady-state editor request rather than cold
	// workspace discovery.
	_ = server.completions(document, position)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.completions(document, position)
	}
}

func BenchmarkSemanticArchitectureCompletionSelectorWarm(b *testing.B) {
	indexedSource := `module page
import "reflect"
func Build() {
    text := "hello"
    _ = reflect.ValueOf(&text).Elem()
}
`
	server, indexed := benchmarkSemanticArchitectureDocument(b, indexedSource)
	document := indexed
	document.Text = strings.Replace(indexedSource, "Elem()", "Elem().", 1)
	position := benchmarkPosition(b, document, "Elem().")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.completions(document, position)
	}
}

func BenchmarkSemanticArchitectureHoverWarm(b *testing.B) {
	server, document := benchmarkSemanticArchitectureDocument(b, `module page
import "strings"
func Build() {
    text := "hello"
    _ = strings.NewReplacer("a", "b").Replace(text)
}
`)
	position := benchmarkPosition(b, document, "Replace")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.hover(document, position)
	}
}

func BenchmarkSemanticArchitectureSignatureWarm(b *testing.B) {
	server, document := benchmarkSemanticArchitectureDocument(b, `module page
import "strings"
func Build() {
    text := "hello"
    _ = strings.NewReplacer("a", "b").Replace(text)
}
`)
	position := benchmarkPosition(b, document, "Replace(")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.signatureHelp(document, position)
	}
}

func BenchmarkSemanticArchitectureDiagnosticsWarm(b *testing.B) {
	server, document := benchmarkSemanticArchitectureDocument(b, `module page
import "strings"
func Build() {
    text := "hello"
    _ = strings.NewReplacer("a", "b").Replace(text)
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.diagnostics(document)
	}
}

func BenchmarkContextAtCachedSnapshot(b *testing.B) {
	server, document := benchmarkSemanticArchitectureDocument(b, `module page
import gox "github.com/gox-lang/gox"
func Build(text string) gox.Component {
    return <main><span>{{ &text }}</span></main>
}
`)
	offset := strings.LastIndex(document.Text, "&text") + len("&text")
	_ = server.contextAt(document, offset)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.contextAt(document, offset)
	}
}

func BenchmarkPhase9CompletionImportedGoxSelectorWarm(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	if err := os.WriteFile(usersPath, []byte(`module users
type User struct { Name string }
func Lookup(id int) User { return User{} }
func hidden() string { return "" }
`), 0o644); err != nil {
		b.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import "users.gox"
func Build() { _ = users.Lo }
`
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		b.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	position := benchmarkPosition(b, document, "users.Lo")
	_ = server.completions(document, position)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.completions(document, position)
	}
}

func BenchmarkPhase10HoverImportedGoxMemberWarm(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	if err := os.WriteFile(usersPath, []byte(`module users
type User struct { Name string }
func (user User) GetName() string { return user.Name }
`), 0o644); err != nil {
		b.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build(user users.User) string { return user.GetName() }
`
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		b.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	position := benchmarkPosition(b, document, "GetName")
	_ = server.hover(document, position)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.hover(document, position)
	}
}

func BenchmarkPhase10SignatureImportedGoxCallWarm(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	if err := os.WriteFile(usersPath, []byte(`module users
type User struct { Name string }
func Lookup(id int, strict bool) (User, error) { return User{}, nil }
`), 0o644); err != nil {
		b.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build() { _, _ = users.Lookup(10, false) }
`
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		b.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	position := benchmarkPosition(b, document, "false")
	_ = server.signatureHelp(document, position)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.signatureHelp(document, position)
	}
}

func BenchmarkPhase11DefinitionImportedGoxSymbolWarm(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	if err := os.WriteFile(usersPath, []byte(`module users
func Lookup(id int) string { return "George" }
`), 0o644); err != nil {
		b.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build() { _ = users.Lookup(10) }
`
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		b.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		b.Fatal("page document was not indexed")
	}
	position := benchmarkPosition(b, document, "Lookup")
	_ = server.definitionLocations(document, position)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.definitionLocations(document, position)
	}
}

func BenchmarkPhase11ReferencesImportedGoxSymbolWarm(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	if err := os.WriteFile(usersPath, []byte(`module users
func Lookup(id int) string { return "George" }
`), 0o644); err != nil {
		b.Fatal(err)
	}
	firstPath := filepath.Join(root, "first.gox")
	firstSource := `module first
import users "./users.gox"
func Build() { _ = users.Lookup(10) }
`
	if err := os.WriteFile(firstPath, []byte(firstSource), 0o644); err != nil {
		b.Fatal(err)
	}
	secondPath := filepath.Join(root, "second.gox")
	if err := os.WriteFile(secondPath, []byte(`module second
import users "./users.gox"
func Build() { _ = users.Lookup(20) }
`), 0o644); err != nil {
		b.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(firstPath))
	if !ok {
		b.Fatal("first document was not indexed")
	}
	position := benchmarkPosition(b, document, "Lookup")
	_ = server.references(document, position, true)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.references(document, position, true)
	}
}

func BenchmarkPhase12DiagnosticsSemanticWarm(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	if err := os.WriteFile(usersPath, []byte(`module users
type User struct { Name string }
func Lookup(id int) User { return User{} }
`), 0o644); err != nil {
		b.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build(user users.User) {
	unused := "x"
	_ = users.Lookup("bad")
	view := <span>{{ user.Missing }}</span>
	_ = view
}
`
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		b.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	_ = server.diagnostics(document)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.diagnostics(document)
	}
}

func BenchmarkPhase13CompletionInferredLocalWarm(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	if err := os.WriteFile(usersPath, []byte(`module users
type User struct { Name string }
func Lookup(id int) User { return User{} }
func (user User) GetName() string { return user.Name }
`), 0o644); err != nil {
		b.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build() {
	user := users.Lookup(1)
	_ = user.Name
}
`
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		b.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	edited := strings.Replace(pageSource, "user.Name", "user.", 1)
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: edited}
	position := benchmarkPosition(b, document, "user.")
	_ = server.completions(document, position)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.completions(document, position)
	}
}

func BenchmarkPhase14CompletionInferredLocalTransientCacheWarm(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	if err := os.WriteFile(usersPath, []byte(`module users
type User struct { Name string }
func Lookup(id int) User { return User{} }
func (user User) GetName() string { return user.Name }
`), 0o644); err != nil {
		b.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build() {
	user := users.Lookup(1)
	_ = user.Name
}
`
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		b.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	edited := strings.Replace(pageSource, "user.Name", "user.", 1)
	document := Document{URI: FileURI(pagePath), Path: pagePath, Version: 1, Text: edited}
	position := benchmarkPosition(b, document, "user.")
	_ = server.completions(document, position)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.completions(document, position)
	}
}

func BenchmarkPhase14ReverseDependencyInvalidation(b *testing.B) {
	root := b.TempDir()
	sharedPath := filepath.Join(root, "shared.gox")
	store := newAnalysisStore(func(_ string, document Document) (*goxcompiler.Module, error) {
		dependencies := []goxcompiler.SemanticDependency{{
			SourcePath:   document.Path,
			Kind:         goxcompiler.SemanticDependencyGoxModule,
			ResolvedPath: filepath.Join(root, "unrelated", filepath.Base(document.Path)),
		}}
		if strings.HasSuffix(document.Path, "dependent.gox") {
			dependencies[0].ResolvedPath = sharedPath
		}
		return &goxcompiler.Module{Semantic: &goxcompiler.SemanticModel{Dependencies: dependencies}}, nil
	})
	seed := func() {
		for i := 0; i < 500; i++ {
			name := fmt.Sprintf("page-%03d.gox", i)
			if i == 499 {
				name = "dependent.gox"
			}
			path := filepath.Join(root, name)
			store.Snapshot(root, Document{URI: FileURI(path), Path: path, Version: 1, Text: "module page\n"})
		}
	}
	seed()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.InvalidateDependencyPath(sharedPath)
		b.StopTimer()
		path := filepath.Join(root, "dependent.gox")
		store.Snapshot(root, Document{URI: FileURI(path), Path: path, Version: 1, Text: "module page\n"})
		b.StartTimer()
	}
}

func BenchmarkPhase14DiagnosticsCachedWarm(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	usersPath := filepath.Join(root, "users.gox")
	if err := os.WriteFile(usersPath, []byte(`module users
type User struct { Name string }
func Lookup(id int) User { return User{} }
`), 0o644); err != nil {
		b.Fatal(err)
	}
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import users "./users.gox"
func Build(user users.User) {
	unused := "x"
	_ = users.Lookup("bad")
	view := <span>{{ user.Missing }}</span>
	_ = view
}
`
	if err := os.WriteFile(pagePath, []byte(pageSource), 0o644); err != nil {
		b.Fatal(err)
	}
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Version: 1, Text: pageSource}
	_ = server.diagnostics(document)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.diagnostics(document)
	}
}

func BenchmarkPhase14CanonicalVersionRebuildSharedGoPackages(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n\ngo 1.24\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build(value string) string { return strings.TrimSpace(value) }
`
	server := NewServer()
	server.workspaceRoots = []string{root}
	document := Document{URI: FileURI(path), Path: path, Version: 1, Text: source}
	if snapshot := server.analysisSnapshot(document); snapshot == nil || snapshot.Err != nil {
		b.Fatalf("warm snapshot = %#v", snapshot)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		document.Version++
		if snapshot := server.analysisSnapshot(document); snapshot == nil || snapshot.Err != nil {
			b.Fatalf("snapshot = %#v", snapshot)
		}
	}
}
