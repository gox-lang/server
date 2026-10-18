package gox

import (
	"errors"
	"testing"

	"github.com/gox-lang/server/internal/goxerrors"
)

func TestParseModuleMapsGoSyntaxErrorToGoxSource(t *testing.T) {
	source := `module main

func Broken() {
	name := @
}
`
	_, err := ParseModule("pages/index.gox", source)
	if err == nil {
		t.Fatal("expected syntax error")
	}
	var compileErr *goxerrors.GoxError
	if !errors.As(err, &compileErr) {
		t.Fatalf("error type = %T, want *goxerrors.GoxError", err)
	}
	if compileErr.Kind != "go-syntax-error" || compileErr.File != "pages/index.gox" {
		t.Fatalf("diagnostic = %#v", compileErr)
	}
	if compileErr.Line != 4 || compileErr.Column == 0 {
		t.Fatalf("location = %d:%d, want line 4", compileErr.Line, compileErr.Column)
	}
}
