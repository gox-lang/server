package gox

import (
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestAddUserStatementLineMarkersClosesEveryUserRegion(t *testing.T) {
	const source = `package main

func Upload() {
	value := make([]byte, 16)
	_ = value
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "pages/index.gox", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	AddUserStatementLineMarkers(file, fset, "pages/index.gox")

	var out strings.Builder
	if err := format.Node(&out, token.NewFileSet(), file); err != nil {
		t.Fatal(err)
	}

	generated := out.String()
	if got, want := strings.Count(generated, "__goxLineReset()"), 2; got != want {
		t.Fatalf("reset markers = %d, want %d:\n%s", got, want, generated)
	}
	if strings.Contains(generated, "__goxLineReset()\n\t__goxLineReset()") {
		t.Fatalf("nested marker processing emitted duplicate adjacent resets:\n%s", generated)
	}
}

func TestRewriteGoxLineMarkersResetsSyntheticClosureToGeneratedFile(t *testing.T) {
	const source = `package main

func Upload() {
	__goxLine("pages/index.gox", 54, 1)
	value := make([]byte, 16)
	_ = value
	__goxLineReset()
}

var handler = func() {
	make([]byte, 32)
}
`

	generatedPath := "/project/.build/pages/index.go"
	generated := rewriteGoxLineMarkers(source, generatedPath, nil)

	userDirective := "//line pages/index.gox:54:1"
	resetDirective := "//line /project/.build/pages/index.go:8:1"
	if !strings.Contains(generated, userDirective) {
		t.Fatalf("generated source missing user directive %q:\n%s", userDirective, generated)
	}
	if !strings.Contains(generated, resetDirective) {
		t.Fatalf("generated source missing reset directive %q:\n%s", resetDirective, generated)
	}
	if strings.Index(generated, resetDirective) > strings.Index(generated, "var handler") {
		t.Fatalf("synthetic closure appears before generated-file reset:\n%s", generated)
	}
}
