package gox

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/goxsource"
)

func TestPhase16_2BLoadGraphSharesGoPackageIdentityAcrossModules(t *testing.T) {
	root := t.TempDir()
	phase16_2BWrite(t, filepath.Join(root, "go.mod"), "module example.com/phase16b\n\ngo 1.24\n")

	entry := filepath.Join(root, "pages", "index.gox")
	child := filepath.Join(root, "pages", "child.gox")
	phase16_2BWrite(t, entry, `module index

import "strings"
import Child "./child.gox"

func Value() string { return strings.TrimSpace(" entry ") }
`)
	phase16_2BWrite(t, child, `module child

import "strings"

func Value() string { return strings.TrimSpace(" child ") }
`)

	graph, err := LoadGraph(entry)
	if err != nil {
		t.Fatal(err)
	}
	entryModule := graph.ByPath[phase16_2BAbs(t, entry)]
	childModule := graph.ByPath[phase16_2BAbs(t, child)]
	if entryModule == nil || childModule == nil {
		t.Fatalf("graph modules missing: entry=%p child=%p", entryModule, childModule)
	}

	entryStrings := phase16_2BSelectorPackage(t, entryModule, "TrimSpace")
	childStrings := phase16_2BSelectorPackage(t, childModule, "TrimSpace")
	if entryStrings != childStrings {
		t.Fatalf("strings package identity differs inside one LoadGraph: entry=%p child=%p", entryStrings, childStrings)
	}
	if entryStrings.Path() != "strings" {
		t.Fatalf("imported package path = %q, want strings", entryStrings.Path())
	}
}

func TestPhase16_2BParseModuleDefaultPathStillUsesIndependentImporter(t *testing.T) {
	source := `module sample

import "strings"

func Value() string { return strings.TrimSpace(" value ") }
`
	first, err := parseModuleWithContext("first.gox", source, goxsource.ModuleContext{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseModuleWithContext("second.gox", source, goxsource.ModuleContext{})
	if err != nil {
		t.Fatal(err)
	}
	if phase16_2BSelectorPackage(t, first, "TrimSpace") == phase16_2BSelectorPackage(t, second, "TrimSpace") {
		t.Fatal("parseModuleWithContext unexpectedly shares package identity across independent parses")
	}
}

func BenchmarkPhase16_2BCompileScopedImporterScaling(b *testing.B) {
	for _, modules := range []int{1, 4, 8, 16} {
		modules := modules
		sources := make([]string, modules)
		paths := make([]string, modules)
		for i := 0; i < modules; i++ {
			paths[i] = fmt.Sprintf("module-%02d.gox", i)
			sources[i] = fmt.Sprintf(`module m%02d

import "strings"

func Value%02d() string { return strings.TrimSpace(" value ") }
`, i, i)
		}

		b.Run(fmt.Sprintf("Shared/N%d", modules), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				semanticImporter := importer.Default()
				for i := 0; i < modules; i++ {
					if _, err := parseModuleWithImporter(paths[i], sources[i], goxsource.ModuleContext{}, semanticImporter); err != nil {
						b.Fatal(err)
					}
				}
			}
		})

		b.Run(fmt.Sprintf("SeparateReference/N%d", modules), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				for i := 0; i < modules; i++ {
					if _, err := parseModuleWithContext(paths[i], sources[i], goxsource.ModuleContext{}); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func phase16_2BSelectorPackage(tb testing.TB, module *Module, selector string) *types.Package {
	tb.Helper()
	if module == nil || module.Semantic == nil || module.Semantic.File == nil || module.Semantic.Info == nil {
		tb.Fatalf("module has no semantic model: %#v", module)
	}
	var result *types.Package
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		selection, ok := node.(*ast.SelectorExpr)
		if !ok || selection.Sel == nil || selection.Sel.Name != selector {
			return true
		}
		object := module.Semantic.Info.ObjectOf(selection.Sel)
		if object != nil {
			result = object.Pkg()
		}
		return result == nil
	})
	if result == nil {
		tb.Fatalf("selector %q has no imported package", selector)
	}
	return result
}

func phase16_2BWrite(tb testing.TB, path, contents string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		tb.Fatal(err)
	}
}

func phase16_2BAbs(tb testing.TB, path string) string {
	tb.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		tb.Fatal(err)
	}
	return filepath.Clean(absolute)
}
