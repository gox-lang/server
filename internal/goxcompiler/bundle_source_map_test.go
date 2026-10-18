package gox

import (
	"strings"
	"testing"
)

func TestFormatBundleMappedBuildsMapFromASTOrigins(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	name := "George"
	return <h1>{{name}}</h1>
}
`
	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BundleModules(&ModuleGraph{
		Entry:   mod,
		Modules: []*Module{mod},
		ByPath:  map[string]*Module{mod.Path: mod},
	})
	if err != nil {
		t.Fatal(err)
	}
	generated, sourceMap, err := FormatBundleMapped(bundle, ".build/pages/index.go#formatted")
	if err != nil {
		t.Fatal(err)
	}
	if sourceMap == nil || sourceMap.GeneratedURI() != ".build/pages/index.go#formatted" {
		t.Fatalf("formatted source map = %#v", sourceMap)
	}

	generatedLiteral := strings.Index(generated, `"George"`)
	originalLiteral := strings.Index(src, `"George"`)
	mapped, ok := sourceMap.OriginalPosition(generatedLiteral + 2)
	if !ok || !mapped.Exact || mapped.URI != "pages/index.gox" || mapped.Offset != originalLiteral+2 {
		t.Fatalf("literal mapping = %#v, %v", mapped, ok)
	}

	generatedWrite := strings.Index(generated, "WriteTextString")
	// The generated writer call belongs to this interpolation, not the
	// beginning of the enclosing template (which may be many lines earlier).
	templateOffset := strings.Index(src, "{{name}}")
	mapped, ok = sourceMap.OriginalPosition(generatedWrite)
	if !ok || mapped.Exact || mapped.URI != "pages/index.gox" || mapped.Offset != templateOffset {
		t.Fatalf("generated template call mapping = %#v, %v", mapped, ok)
	}

	packageOffset := strings.Index(generated, "package main")
	if _, ok := sourceMap.OriginalPosition(packageOffset); ok {
		t.Fatal("synthetic package declaration unexpectedly has a GOX origin")
	}
}
