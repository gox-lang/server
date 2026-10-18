package gox

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/goxerrors"
)

func compileActionContractSource(t *testing.T, body string) string {
	t.Helper()

	src := `module main

import (
	"errors"
	"math"
	"math/big"
	"strings"
	"github.com/gox-lang/gox"
)

func MyAction(ctx *gox.Context) gox.Action {
` + body + `
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
	return buf.String()
}

func TestActionGeneratedSignatureReturnsVoid(t *testing.T) {
	out := compileActionContractSource(t, `return "ok"`)

	if !strings.Contains(out, `func (m *GoxModuleMain) GoxActionMainMyAction(ctx *gox.Context)`) {
		t.Fatalf("action generated signature should not return a value:\n%s", out)
	}
	if strings.Contains(out, `) any`) || strings.Contains(out, `(any, error)`) {
		t.Fatalf("action generated output must not return values:\n%s", out)
	}
}

func TestActionGoxHTMLReturnPreservesHTMLContentType(t *testing.T) {
	out := compileActionContractSource(t, `return gox.HTML("<article>GOX</article>")`)
	if !strings.Contains(out, `ctx.Response.Headers.Set("Content-Type", "text/html; charset=utf-8")`) {
		t.Fatalf("gox.HTML action response is missing HTML content type:\n%s", out)
	}
	if !strings.Contains(out, `ctx.Writer.WriteRawString(string(gox.HTML("<article>GOX</article>")))`) {
		t.Fatalf("gox.HTML action response was not written as raw HTML:\n%s", out)
	}
}

func TestActionWrapperCallsVoidActionInsideBenchmark(t *testing.T) {
	out := compileActionContractSource(t, `return errors.New("boom")`)

	if !strings.Contains(out, `gox.BenchmarkHandlerSection(func()`) {
		t.Fatalf("action wrapper should benchmark user action:\n%s", out)
	}
	if strings.Contains(out, `WriteActionResponse`) || strings.Contains(out, `result =`) || strings.Contains(out, `result any`) {
		t.Fatalf("action wrapper should not use result or WriteActionResponse:\n%s", out)
	}
	if !strings.Contains(out, `GoxApp.Main.GoxActionMainMyAction(ctx)`) {
		t.Fatalf("action wrapper should call generated action directly:\n%s", out)
	}
}

func TestActionRejectsMultipleReturnValues(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func MyAction(ctx *gox.Context) gox.Action {
	return "ok", nil
}
`
	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected action with multiple return values to fail")
	}
	if !strings.Contains(err.Error(), "action return must be compiled into a response") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestActionInvalidReturnUsesOriginalActionNameAndUserLine(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return nil
}

func MyAction(ctx *gox.Context) gox.Action {
	return "ok", nil
}
`
	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected invalid action return error")
	}

	core, ok := err.(interface{ GetLine() int })
	_ = core
	_ = ok

	goxErr, ok := err.(*goxerrors.GoxError)
	if !ok {
		t.Fatalf("expected *GoxError, got %T: %v", err, err)
	}
	if !strings.Contains(goxErr.Message, "invalid Action MyAction") {
		t.Fatalf("message should use original action name, got %q", goxErr.Message)
	}
	if goxErr.Line != 10 {
		t.Fatalf("line = %d, want 10", goxErr.Line)
	}
}

func TestActionAllowsNoParamsAndCompilerAppendsContext(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func MyAction() gox.Action {
	return "ok"
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
	if !strings.Contains(out, `func (m *GoxModuleMain) GoxActionMainMyAction(ctx *gox.Context)`) {
		t.Fatalf("action without params should compile with ctx appended:\n%s", out)
	}
	if strings.Contains(out, `GoxActionParamsMainMyAction`) {
		t.Fatalf("action without user params should not generate params struct:\n%s", out)
	}
}

func TestActionParamsUseMultipartForm(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func MyAction(name string, ctx *gox.Context) gox.Action {
	return name
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
	if !strings.Contains(out, `gox.ValidateActionParams(ctx, &__goxActionSpecMyAction`) {
		t.Fatalf("action wrapper should validate params through SDK helper:\n%s", out)
	}
	if strings.Contains(out, `json.NewDecoder(ctx.Request.Body)`) {
		t.Fatalf("action wrapper must not treat gox.Body as io.Reader:\n%s", out)
	}
}

func TestActionFilesParamGeneratesActionFilesSpec(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func Upload(files []gox.File, ctx *gox.Context) gox.Action {
	return nil
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
	if !strings.Contains(out, `Type: gox.ActionFiles`) {
		t.Fatalf("[]gox.File should generate gox.ActionFiles:\n%s", out)
	}
	if !strings.Contains(out, `JSName: "File[]"`) {
		t.Fatalf("[]gox.File should generate JSName File[]:\n%s", out)
	}
	if !strings.Contains(out, `gox.ValidateActionParams(ctx, &__goxActionSpecUpload, &__goxActionParamFiles)`) {
		t.Fatalf("action wrapper should validate []gox.File through SDK helper:\n%s", out)
	}
}

func TestActionMaxUint64ConstantUsesUint64WriterAndBigIntType(t *testing.T) {
	out := compileActionContractSource(t, `return math.MaxUint64`)

	if !strings.Contains(out, `__goxActionResult := uint64(math.MaxUint64)`) {
		t.Fatalf("math.MaxUint64 should be explicitly typed as uint64:\n%s", out)
	}
	if !strings.Contains(out, `gox.ActionUnsignedIntegerType(uint64(__goxActionResult))`) {
		t.Fatalf("math.MaxUint64 should use runtime number/bigint classification:\n%s", out)
	}
	if !strings.Contains(out, `ctx.Writer.WriteTextUint64(__goxActionResult)`) {
		t.Fatalf("math.MaxUint64 should use the uint64 writer:\n%s", out)
	}
	if strings.Contains(out, `WriteRawAny`) {
		t.Fatalf("math.MaxUint64 must not use WriteRawAny:\n%s", out)
	}
}

func TestActionMaxUint64LiteralUsesUint64Writer(t *testing.T) {
	out := compileActionContractSource(t, `return 18446744073709551615`)

	if !strings.Contains(out, `__goxActionResult := uint64(18446744073709551615)`) {
		t.Fatalf("large untyped integer literal should be explicitly typed as uint64:\n%s", out)
	}
	if !strings.Contains(out, `ctx.Writer.WriteTextUint64(__goxActionResult)`) {
		t.Fatalf("large untyped integer literal should use the uint64 writer:\n%s", out)
	}
}

func TestActionNewBigIntUsesBigIntWriter(t *testing.T) {
	out := compileActionContractSource(t, `
	n := new(big.Int)
	n.SetUint64(math.MaxUint64)
	return n`)

	if !strings.Contains(out, `ctx.Response.Headers.Set("Action-Type", "bigint")`) {
		t.Fatalf("*big.Int should emit Action-Type bigint:\n%s", out)
	}
	if !strings.Contains(out, `ctx.Writer.WriteTextBigInt(n)`) {
		t.Fatalf("*big.Int should use WriteTextBigInt:\n%s", out)
	}
	if strings.Contains(out, `ctx.Writer.WriteRawAny(n)`) {
		t.Fatalf("*big.Int must not use WriteRawAny:\n%s", out)
	}
}

func TestActionByteReturnPropagatesWriterError(t *testing.T) {
	out := compileActionContractSource(t, "return byte(65)")
	if !strings.Contains(out, "if __goxWriteErr := ctx.Writer.WriteByte(") ||
		!strings.Contains(out, "__goxWriteErr != nil") ||
		!strings.Contains(out, "panic(__goxWriteErr)") {
		t.Fatalf("byte action must propagate the writer failure:\n%s", out)
	}
}
