package lsp

import (
	"os"
	"strings"
	"testing"
)

// GOX_LSP_BENCH_PAGE allows profiling a real large page without requiring a
// particular sibling repository or modifying the user's document.
func largeBenchmarkDocument(b *testing.B) Document {
	b.Helper()
	path := os.Getenv("GOX_LSP_BENCH_PAGE")
	if path == "" {
		b.Skip("set GOX_LSP_BENCH_PAGE to a large .gox page")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	return Document{URI: FileURI(path), Path: path, Text: string(data)}.withLineIndex()
}
func BenchmarkLargePageAnalysis(b *testing.B) {
	doc := largeBenchmarkDocument(b)
	s := NewServer()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doc.Version = i + 1
		s.analysisSnapshot(doc)
	}
}
func BenchmarkLargePageAdaptCompletion(b *testing.B) {
	doc := largeBenchmarkDocument(b)
	s := NewServer()
	offset := strings.LastIndex(doc.Text, `adapt="`)
	if offset < 0 {
		b.Skip("page has no adapt attribute")
	}
	offset += len(`adapt="`)
	pos, _ := doc.Position(offset)
	if snap := s.analysisSnapshot(doc); snap != nil && snap.Module != nil {
		_ = s.symbols.UpdateOpenModule(doc, snap.Module)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doc.Version = i + 1
		s.completions(doc, pos)
	}
}
func BenchmarkLargePageDiagnostics(b *testing.B) {
	doc := largeBenchmarkDocument(b)
	s := NewServer()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doc.Version = i + 1
		s.diagnostics(doc)
	}
}
