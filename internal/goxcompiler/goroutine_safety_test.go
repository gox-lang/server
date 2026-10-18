package gox

import (
	"errors"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/goxerrors"
)

func TestParseModuleRejectsUnsafeGoroutineAtGoxSource(t *testing.T) {
	source := `module main

func worker() {}

func GET() {
    go worker()
}
`
	_, err := ParseModule("pages/index.gox", source)
	if err == nil {
		t.Fatal("expected unsafe goroutine error")
	}
	var compileErr *goxerrors.GoxError
	if !errors.As(err, &compileErr) {
		t.Fatalf("error type = %T, want *goxerrors.GoxError", err)
	}
	if compileErr.Kind != diagnosticUnsafeGoroutine {
		t.Fatalf("kind = %q, want %q", compileErr.Kind, diagnosticUnsafeGoroutine)
	}
	if compileErr.File != "pages/index.gox" || compileErr.Line != 6 || compileErr.Column != 5 {
		t.Fatalf("location = %s:%d:%d, want pages/index.gox:6:5", compileErr.File, compileErr.Line, compileErr.Column)
	}
	if !strings.Contains(compileErr.Error(), "goroutine has no panic recovery and may terminate GOX Server") {
		t.Fatalf("unexpected error: %v", compileErr)
	}
}

func TestParseModuleAcceptsProtectedGoroutine(t *testing.T) {
	source := `module main

func worker() {
    defer func() { recover() }()
}

func GET() {
    go worker()
}
`
	if _, err := ParseModule("pages/index.gox", source); err != nil {
		t.Fatalf("protected goroutine rejected: %v", err)
	}
}

func TestParseModuleRejectsNestedUnsafeGoroutineIndependently(t *testing.T) {
	source := `module main

func GET() {
    go func() {
        defer func() { recover() }()
        go func() {}()
    }()
}
`
	_, err := ParseModule("pages/index.gox", source)
	if err == nil {
		t.Fatal("expected nested unsafe goroutine error")
	}
	var compileErr *goxerrors.GoxError
	if !errors.As(err, &compileErr) {
		t.Fatalf("error type = %T, want *goxerrors.GoxError", err)
	}
	if compileErr.Kind != diagnosticUnsafeGoroutine || compileErr.Line != 6 {
		t.Fatalf("diagnostic = %#v, want unsafe goroutine on line 6", compileErr)
	}
}
