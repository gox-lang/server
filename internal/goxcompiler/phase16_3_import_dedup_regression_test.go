package gox

import (
	"go/types"
	"testing"
)

func TestPhase16_3BundleImportsDeduplicateExplicitNaturalAliasAcrossModules(t *testing.T) {
	first, err := ParseModule("app/first.gox", `module first

import "fmt"

var Value = fmt.Sprintf("%d", 1)
`)
	if err != nil {
		t.Fatal(err)
	}

	second, err := ParseModule("app/second.gox", `module second

import fmt "fmt"

var Value = fmt.Sprintf("%d", 2)
`)
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := BundleModules(&ModuleGraph{
		Entry:   first,
		Modules: []*Module{first, second},
		ByPath:  map[string]*Module{first.Path: first, second.Path: second},
	})
	if err != nil {
		t.Fatal(err)
	}

	imports := bundle.GoImports
	if got := countImportPath(imports, "fmt"); got != 1 {
		t.Fatalf("fmt imports = %d, want 1: %#v", got, imports)
	}
	for _, imp := range imports {
		if imp.Path == "fmt" && imp.Alias != "" {
			t.Fatalf("fmt alias = %q, want canonical empty alias", imp.Alias)
		}
	}
}

func TestPhase16_3BundleImportsPreserveDistinctCustomAlias(t *testing.T) {
	first, err := ParseModule("app/first.gox", `module first

import "fmt"

var Value = fmt.Sprintf("%d", 1)
`)
	if err != nil {
		t.Fatal(err)
	}

	second, err := ParseModule("app/second.gox", `module second

import format "fmt"

var Value = format.Sprintf("%d", 2)
`)
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := BundleModules(&ModuleGraph{
		Entry:   first,
		Modules: []*Module{first, second},
		ByPath:  map[string]*Module{first.Path: first, second.Path: second},
	})
	if err != nil {
		t.Fatal(err)
	}

	imports := bundle.GoImports
	if got := countImportPath(imports, "fmt"); got != 2 {
		t.Fatalf("fmt imports = %d, want 2 distinct bindings: %#v", got, imports)
	}
}

func TestPhase16_3NormalizeRedundantImportAliasUsesDeclaredPackageName(t *testing.T) {
	dependency := types.NewPackage("example.com/server", "runtimecfg")
	host := types.NewPackage("main", "main")
	host.SetImports([]*types.Package{dependency})
	mod := &Module{Semantic: &SemanticModel{Package: host}}

	custom := normalizeRedundantImportAlias(mod, ImportDecl{
		Path:  "example.com/server",
		Alias: "server",
	})
	if custom.Alias != "server" {
		t.Fatalf("custom alias = %q, want server", custom.Alias)
	}

	redundant := normalizeRedundantImportAlias(mod, ImportDecl{
		Path:  "example.com/server",
		Alias: "runtimecfg",
	})
	if redundant.Alias != "" {
		t.Fatalf("natural alias = %q, want empty", redundant.Alias)
	}
}

func countImportPath(imports []ImportDecl, path string) int {
	count := 0
	for _, imp := range imports {
		if imp.Path == path {
			count++
		}
	}
	return count
}
