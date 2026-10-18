package lsp

import "testing"

func TestPhase15DiagnosticsCacheIsOwnedByCanonicalSnapshot(t *testing.T) {
	document := Document{
		URI:     "file:///workspace/page.gox",
		Path:    "/workspace/page.gox",
		Version: 1,
		Text: `module page
func Build() string {
	return 42
}
`,
	}
	server := NewServer()
	snapshot := server.analysisSnapshot(document)
	if snapshot == nil || snapshot.Err != nil || snapshot.Module == nil {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if err := server.symbols.UpdateOpenModule(document, snapshot.Module); err != nil {
		t.Fatal(err)
	}

	first := server.diagnostics(document)
	if len(first) == 0 {
		t.Fatal("expected semantic diagnostic")
	}
	current, ok := server.analysis.Current(document.URI)
	if !ok || current != snapshot {
		t.Fatalf("current snapshot = %p, want %p", current, snapshot)
	}
	if len(current.diagnostics) != len(first) {
		t.Fatalf("cached diagnostics = %#v, returned = %#v", current.diagnostics, first)
	}

	cachedMessage := current.diagnostics[0].Message
	first[0].Message = "mutated by caller"
	if current.diagnostics[0].Message != cachedMessage {
		t.Fatalf("caller mutated canonical diagnostics cache: %#v", current.diagnostics)
	}
	second := server.diagnostics(document)
	if len(second) == 0 || second[0].Message != cachedMessage {
		t.Fatalf("second diagnostics = %#v, want cached message %q", second, cachedMessage)
	}
}

func TestPhase15DiagnosticsCacheIsVersionScopedAndPreservesEmptySlice(t *testing.T) {
	server := NewServer()
	invalid := Document{
		URI:     "file:///workspace/page.gox",
		Path:    "/workspace/page.gox",
		Version: 1,
		Text: `module page
func Build() string {
	return 42
}
`,
	}
	invalidSnapshot := server.analysisSnapshot(invalid)
	if invalidSnapshot == nil || invalidSnapshot.Err != nil || invalidSnapshot.Module == nil {
		t.Fatalf("invalid snapshot = %#v", invalidSnapshot)
	}
	if err := server.symbols.UpdateOpenModule(invalid, invalidSnapshot.Module); err != nil {
		t.Fatal(err)
	}
	if diagnostics := server.diagnostics(invalid); len(diagnostics) == 0 {
		t.Fatal("expected diagnostic for version 1")
	}

	valid := invalid
	valid.Version = 2
	valid.Text = `module page
func Build() string {
	return "ok"
}
`
	validSnapshot := server.analysisSnapshot(valid)
	if validSnapshot == nil || validSnapshot == invalidSnapshot || validSnapshot.Err != nil || validSnapshot.Module == nil {
		t.Fatalf("valid snapshot = %#v", validSnapshot)
	}
	if err := server.symbols.UpdateOpenModule(valid, validSnapshot.Module); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(valid)
	if diagnostics == nil || len(diagnostics) != 0 {
		t.Fatalf("version 2 diagnostics = %#v, want non-nil empty slice", diagnostics)
	}
	if validSnapshot.diagnostics == nil || len(validSnapshot.diagnostics) != 0 {
		t.Fatalf("version 2 cached diagnostics = %#v, want non-nil empty slice", validSnapshot.diagnostics)
	}
}
