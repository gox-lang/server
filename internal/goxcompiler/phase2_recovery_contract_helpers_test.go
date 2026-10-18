package gox

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/goxerrors"
)

const phase2RecoveryContractsEnv = "GOX_RECOVERY_CONTRACTS"

// phase2RequireRecoveryContracts keeps the known-broken recovery contracts out
// of the default green suite until their target implementation phase begins.
// The tests still compile on every go test ./..., so they cannot silently rot.
func phase2RequireRecoveryContracts(t *testing.T) {
	t.Helper()
	if os.Getenv(phase2RecoveryContractsEnv) != "1" {
		t.Skip("recovery contract is locked; set GOX_RECOVERY_CONTRACTS=1 to execute known-broken contracts")
	}
}

func phase2RequireError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected contract validation error, got nil")
	}
}

func phase2RequireErrorKind(t *testing.T, err error, kind string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %q error, got nil", kind)
	}
	goxErr, ok := err.(*goxerrors.GoxError)
	if !ok {
		t.Fatalf("error = %T %v, want *goxerrors.GoxError kind %q", err, err, kind)
	}
	if goxErr.Kind != kind {
		t.Fatalf("error kind = %q, want %q: %v", goxErr.Kind, kind, err)
	}
}

func phase2ModuleFuncKind(t *testing.T, mod *Module, name string) FuncKind {
	t.Helper()
	for _, fn := range mod.Funcs {
		if fn.Name == name {
			return fn.Kind
		}
	}
	t.Fatalf("function %q not found in module", name)
	return FuncNormal
}

// phase2BundleSingleModuleAndGoTest exercises the lowering/bundling path without
// invoking the JavaScript client build pipeline. This is useful for contracts
// such as Component -> Client, where the Go generator is the subject under
// test and Node/framework availability is intentionally irrelevant.
func phase2BundleSingleModuleAndGoTest(t *testing.T, modulePath, source string) string {
	t.Helper()

	mod, err := ParseModule(modulePath, source)
	if err != nil {
		t.Fatalf("ParseModule(%s): %v", modulePath, err)
	}
	bundle, err := BundleModules(&ModuleGraph{
		Entry:   mod,
		Modules: []*Module{mod},
		ByPath:  map[string]*Module{mod.Path: mod},
	})
	if err != nil {
		t.Fatalf("BundleModules(%s): %v", modulePath, err)
	}

	root := t.TempDir()
	phase1CopyModuleFiles(t, root)
	outDir := filepath.Join(root, ".build", "phase2")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create bundle output directory: %v", err)
	}
	outPath := filepath.Join(outDir, "generated.go")
	if err := WriteBundle(bundle, outPath); err != nil {
		t.Fatalf("WriteBundle(%s): %v", modulePath, err)
	}
	generatedBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read generated Go: %v", err)
	}
	generated := string(generatedBytes)
	phase1GoTestGeneratedPackage(t, outDir, generated)
	return generated
}

func phase2CompileExpectError(t *testing.T, fixture phase1GeneratedGoFixture) error {
	t.Helper()

	root := t.TempDir()
	phase1CopyModuleFiles(t, root)
	for rel, contents := range fixture.files {
		phase1WriteFile(t, filepath.Join(root, filepath.FromSlash(rel)), contents)
	}
	entry := filepath.Join(root, filepath.FromSlash(fixture.entryRel))
	phase1WriteFile(t, entry, fixture.source)
	outDir := filepath.Join(root, ".build", "phase2")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create compile output directory: %v", err)
	}
	_, err := Compile(entry, filepath.Join(outDir, "generated.go"))
	return err
}

func phase2FormattedModule(t *testing.T, mod *Module) string {
	t.Helper()
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), mod.File); err != nil {
		t.Fatalf("format module: %v", err)
	}
	return out.String()
}

func phase2CountCallsByName(node ast.Node, name string) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == name {
				count++
			}
		case *ast.SelectorExpr:
			if fn.Sel != nil && fn.Sel.Name == name {
				count++
			}
		}
		return true
	})
	return count
}

func phase2ContainsCallByName(node ast.Node, name string) bool {
	return phase2CountCallsByName(node, name) > 0
}

func phase2RequireGeneratedContains(t *testing.T, generated string, want string) {
	t.Helper()
	if !strings.Contains(generated, want) {
		t.Fatalf("generated Go missing %q:\n%s", want, phase1Truncate(generated, 20000))
	}
}
