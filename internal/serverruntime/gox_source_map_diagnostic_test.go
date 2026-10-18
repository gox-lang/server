package serverruntime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"

	"github.com/gox-lang/gox"
)

func TestBuildErrorUsesPersistedSourceMapLineAndColumn(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "pages", "index.gox")
	buildDir := filepath.Join(root, ".build", "pages", "index")
	generatedPath := filepath.Join(buildDir, "current.go")
	sourceMapPath := goxcompiler.BundleSourceMapPath(generatedPath)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "func GET() {\n\tmissingValue()\n}\n"
	generated := "package pages\n\nfunc GET() {\n\tmissingValue()\n}\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generatedPath, []byte(generated), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceStart := len("func GET() {\n\t")
	generatedStart := len("package pages\n\nfunc GET() {\n\t")
	sourceMap, err := goxmap.New(generatedPath, []goxmap.Mapping{{
		Generated: goxmap.Range{Start: generatedStart, End: generatedStart + len("missingValue")},
		Original:  goxmap.SourceRange{URI: "pages/index.gox", Range: goxmap.Range{Start: sourceStart, End: sourceStart + len("missingValue")}},
		Kind:      goxmap.Exact,
	}})
	if err != nil {
		t.Fatal(err)
	}
	mapData, err := goxmap.MarshalJSONDocument(sourceMap, generated, map[string]string{"pages/index.gox": source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceMapPath, mapData, 0o644); err != nil {
		t.Fatal(err)
	}
	route := &GoxRouteRuntime{
		RelPath:       "pages/index.gox",
		GeneratedPath: generatedPath,
		CompileResult: &goxcompiler.CompileResult{SourceMapPath: sourceMapPath},
	}
	cause := errors.New(generatedPath + ":4:2: undefined: missingValue")
	goxErr := newGoxError(gox.GoxErrorBuild, route, nil, "page build failed", cause)
	if goxErr.File != "pages/index.gox" || goxErr.Line != 2 || goxErr.Column != 2 {
		t.Fatalf("mapped location = %s:%d:%d", goxErr.File, goxErr.Line, goxErr.Column)
	}
	if goxErr.Kind != "undefined-identifier" || goxErr.Message != "undefined: missingValue" {
		t.Fatalf("mapped diagnostic = %q %q", goxErr.Kind, goxErr.Message)
	}
	if goxErr.Details["sourceMap.exact"] != "true" {
		t.Fatalf("sourceMap.exact = %q", goxErr.Details["sourceMap.exact"])
	}
}

func TestBuildErrorMapsRenamedGeneratedTemplate(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "pages", "index.gox")
	buildDir := filepath.Join(root, ".build", "pages", "index")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		t.Fatal(err)
	}
	const source = `module index
import "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page {
 return <main>
  <&Panel title={{ 42 }} />
 </main>
}
func Panel(title string) gox.Component { return <h1>{{ title }}</h1> }
`
	if err := os.WriteFile(sourcePath, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	temporary := filepath.Join(buildDir, "temporary.go")
	result, err := goxcompiler.Compile(sourcePath, temporary)
	if err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(buildDir, "hash.go")
	if err := moveGoxGeneratedArtifacts(temporary, final, result.SourceMapPath, result); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{buildDir, root} {
		target := "."
		if dir == root {
			target = "./.build/pages/index"
		}
		cmd := exec.Command("go", "test", "-mod=readonly", "-run", "^$", target)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("Go unexpectedly accepted an int for a string component prop")
		}
		if !strings.Contains(string(output), "cannot use 42") {
			t.Fatalf("unexpected Go failure: %s", output)
		}
		route := &GoxRouteRuntime{RelPath: "pages/index.gox", GeneratedPath: final, CompileResult: result}
		goxErr := newGoxError(gox.GoxErrorBuild, route, nil, "page build failed", errors.New(string(output)))
		// Synthetic calls anchor to the component name, after the <& sigil.
		if goxErr.File != "pages/index.gox" || goxErr.Line != 5 || goxErr.Column != 5 {
			t.Fatalf("location = %s:%d:%d; expected Panel at pages/index.gox:5:5\n%s", goxErr.File, goxErr.Line, goxErr.Column, output)
		}
		if goxErr.Details["sourceMap.exact"] == "" {
			t.Fatal("error did not use persisted sourcemap")
		}
	}
}

func TestRuntimeErrorWithoutColumnMapsRenamedGeneratedFile(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "pages", "index.gox")
	buildDir := filepath.Join(root, ".build", "pages", "index")
	generatedPath := filepath.Join(buildDir, "hash.go")
	temporaryPath := filepath.Join(buildDir, "temporary.go")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		t.Fatal(err)
	}
	source := "module page\nfunc GET() {\n return <html>\n  <head></head>\n </html>\n}\n"
	generated := "package main\nfunc GET() {\n//line " + temporaryPath + ":24:1\n\tctx.Response.ImportMap.Runtime().ApplyCSP(hash)\n}\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generatedPath, []byte(generated), 0600); err != nil {
		t.Fatal(err)
	}
	start := strings.Index(generated, "ctx.Response")
	original := strings.Index(source, "head")
	sm, err := goxmap.New(generatedPath, []goxmap.Mapping{{Generated: goxmap.Range{Start: start, End: start + len("ctx.Response.ImportMap.Runtime().ApplyCSP(hash)")}, Original: goxmap.SourceRange{URI: "pages/index.gox", Range: goxmap.Range{Start: original, End: original + 1}}, Kind: goxmap.Anchor}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := goxmap.MarshalJSONDocument(sm, generated, map[string]string{"pages/index.gox": source})
	if err != nil {
		t.Fatal(err)
	}
	mapPath := goxcompiler.BundleSourceMapPath(generatedPath)
	if err := os.WriteFile(mapPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	route := &GoxRouteRuntime{RelPath: "pages/index.gox", GeneratedPath: generatedPath, CompileResult: &goxcompiler.CompileResult{SourceMapPath: mapPath}}
	runtimeError := newFlowRuntimeError(route, nil, "invalid-response-mutation-after-write", "cannot mutate after write", nil)
	runtimeError.File = temporaryPath
	runtimeError.Line = 24
	runtimeError.Column = 0
	applyGoxSourceMapDiagnostic(runtimeError, route)
	if runtimeError.File != "pages/index.gox" || runtimeError.Line != 4 || runtimeError.Column != 4 {
		t.Fatalf("runtime source location: %s:%d:%d", runtimeError.File, runtimeError.Line, runtimeError.Column)
	}
	if runtimeError.Details["sourceMap.exact"] != "false" {
		t.Fatal("synthetic statement should map to an anchor")
	}
}
