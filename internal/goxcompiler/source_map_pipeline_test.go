package gox

import (
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/goxmap"
)

func TestParseModuleExposesComposedLexicalSourceMap(t *testing.T) {
	src := `// page module
module main

client Menu react {
	src: "menu.jsx"
	// menú 😀
}

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	name := "George"
	Image := <img alt={{name}}>
	return <main><&Image/><@Menu/></main>
}
`

	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	if mod.ParseSourceMap == nil {
		t.Fatal("parsed module did not expose its source map")
	}
	if got, want := mod.ParseSourceMap.GeneratedURI(), "pages/index.gox#parse"; got != want {
		t.Fatalf("generated URI = %q, want %q", got, want)
	}
	if !strings.HasPrefix(mod.ParseSource, "package main\n") {
		t.Fatalf("parse source is missing generated package declaration:\n%s", mod.ParseSource)
	}
	if _, ok := mod.ParseSourceMap.OriginalPosition(0); ok {
		t.Fatal("generated package declaration unexpectedly maps to GOX source")
	}

	generatedName := strings.Index(mod.ParseSource, `name := "George"`)
	originalName := strings.Index(src, `name := "George"`)
	assertMappedPosition(t, mod.ParseSourceMap, generatedName, originalName, true)

	componentFunc := strings.Index(mod.ParseSource, "func(goxWriter gox.Writer)")
	componentTemplate := strings.Index(src, "<img")
	assertMappedPosition(t, mod.ParseSourceMap, componentFunc, componentTemplate, false)

	placeholder := strings.Index(mod.ParseSource, "__gox_Main_GET_Template_1")
	returnTemplate := strings.Index(src, "<main>")
	assertMappedPosition(t, mod.ParseSourceMap, placeholder, returnTemplate, false)
}

func TestClientMaskPreservesUTF8ByteLengthAndLines(t *testing.T) {
	src := "client Menu react {\n\tsrc: \"menu.jsx\"\n\t// menú 😀\n}\n"
	masked := maskClientDeclaration(src)
	if len(masked) != len(src) {
		t.Fatalf("masked client length = %d, want %d", len(masked), len(src))
	}
	if strings.Count(masked, "\n") != strings.Count(src, "\n") {
		t.Fatalf("masked client changed line count: %q", masked)
	}
	if strings.Contains(masked, "menú") || strings.Contains(masked, "😀") {
		t.Fatalf("masked client retained declaration content: %q", masked)
	}
}

func assertMappedPosition(t *testing.T, sourceMap *goxmap.Map, generatedOffset, originalOffset int, exact bool) {
	t.Helper()
	mapped, ok := sourceMap.OriginalPosition(generatedOffset)
	if !ok || mapped.URI != "pages/index.gox" || mapped.Offset != originalOffset || mapped.Exact != exact {
		t.Fatalf("mapping at %d = %#v, %v; want offset %d exact=%v", generatedOffset, mapped, ok, originalOffset, exact)
	}
}
