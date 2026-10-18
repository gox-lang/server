package gox

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"strings"
	"testing"
)

func TestActionWrapperMeasuresOnlyUserActionSection(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func Sum(a int, b int, ctx *gox.Context) gox.Action {
	msg := "sum"
	return map[string]any{msg: a + b}
}
`

	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	if err != nil {
		t.Fatal(err)
	}

	file := &ast.File{Name: ast.NewIdent("main"), Decls: bundle.Decls}
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), file); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, `gox.BenchmarkHandlerSection(func()`) {
		t.Fatalf("compiled action wrapper does not isolate user action benchmark section:\n%s", out)
	}
	decode := strings.Index(out, `gox.ValidateActionParams(ctx, &__goxActionSpecSum`)
	section := strings.Index(out, `gox.BenchmarkHandlerSection(func()`)
	if decode < 0 || section < 0 {
		t.Fatalf("compiled action wrapper missing action param validation or benchmark section:\n%s", out)
	}
	if !(decode < section) {
		t.Fatalf("compiled action wrapper should validate params before benchmark section:\n%s", out)
	}
	if strings.Contains(out, `WriteActionResponse`) {
		t.Fatalf("compiled action wrapper must not use WriteActionResponse:\n%s", out)
	}
	actionCall := strings.Index(out, `GoxActionMainSum(__goxActionParamA, __goxActionParamB, ctx)`)
	if actionCall < 0 {
		t.Fatalf("compiled action wrapper missing user action call:\n%s", out)
	}
	sectionEnd := strings.Index(out[section:], `})`)
	if sectionEnd < 0 {
		t.Fatalf("compiled action wrapper has malformed benchmark section:\n%s", out)
	}
	sectionEnd += section
	if !(section < actionCall && actionCall < sectionEnd) {
		t.Fatalf("user action call must be inside benchmark section:\n%s", out)
	}
}
