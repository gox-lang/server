package lsp

import (
	"strings"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestCursorContextAtDistinguishesGoAddressFromComponentSigil(t *testing.T) {
	goSource := `module page

func Build(text string) {
	consume(&text)
}
`
	goOffset := strings.Index(goSource, "&text") + len("&text")
	if got := classifyCursorContext(goSource, goOffset, nil).Kind; got != cursorContextGo {
		t.Fatalf("foo(&text) context = %v, want Go", got)
	}

	componentSource := `module page

func Build() any {
	return <&Menu
}
`
	componentOffset := strings.Index(componentSource, "<&Menu") + len("<&Menu")
	context := classifyCursorContext(componentSource, componentOffset, nil)
	if context.Kind != cursorContextTemplateSigil || !context.HasSigil || context.Sigil.Sigil != '&' {
		t.Fatalf("<&Menu context = %#v, want component template sigil", context)
	}
}

func TestCursorContextAtUsesParsedTemplateExpressionRanges(t *testing.T) {
	source := `module page

import gox "github.com/gox-lang/gox"

func Build(text string) gox.Component {
	return <div class="value">{{ &text }}</div>
}
`
	module, err := goxcompiler.ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	expressionOffset := strings.Index(source, "&text") + len("&text")
	context := classifyCursorContext(source, expressionOffset, module)
	if context.Kind != cursorContextTemplateExpression || !context.goCode() {
		t.Fatalf("template expression context = %#v, want TemplateExpression/Go code", context)
	}

	markupOffset := strings.Index(source, `class="value"`) + len(`class="`)
	if got := classifyCursorContext(source, markupOffset, module).Kind; got != cursorContextTemplateAttribute {
		t.Fatalf("static template value context = %v, want TemplateAttribute", got)
	}
}

func TestCursorContextAtParsedTemplateBeatsClientAndImportHeuristics(t *testing.T) {
	source := `module page

import gox "github.com/gox-lang/gox"

func Build() gox.Component {
	return <pre>client Menu react { src: "menu.ts" } import "net/http"</pre>
}
`
	module, err := goxcompiler.ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"menu.ts", "net/http"} {
		offset := strings.Index(source, marker) + len(marker)
		if got := classifyCursorContext(source, offset, module).Kind; got != cursorContextTemplateMarkup {
			t.Fatalf("template text %q context = %v, want TemplateMarkup", marker, got)
		}
	}
}

func TestCursorContextAtClassifiesSpecializedEditorSurfaces(t *testing.T) {
	tests := []struct {
		name   string
		source string
		marker string
		kind   cursorContextKind
	}{
		{
			name:   "quoted import path",
			source: "module page\n\nimport \"net/ht\"\n",
			marker: "net/ht",
			kind:   cursorContextImportPath,
		},
		{
			name:   "bare import block path",
			source: "module page\n\nimport (\n    fmt\n)\n",
			marker: "fmt",
			kind:   cursorContextImportPath,
		},
		{
			name:   "client source path",
			source: "module page\n\nclient Menu react {\n    src: \"menu.ts\"\n}\n",
			marker: "menu.ts",
			kind:   cursorContextClientSource,
		},
		{
			name:   "directive sigil",
			source: "module page\n\nfunc Build() any {\n    return <:fo\n}\n",
			marker: ":fo",
			kind:   cursorContextTemplateSigil,
		},
		{
			name:   "template attribute",
			source: "module page\n\nfunc Build() any {\n    return <div cla\n}\n",
			marker: "cla",
			kind:   cursorContextTemplateAttribute,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			offset := strings.Index(test.source, test.marker)
			if offset < 0 {
				t.Fatalf("marker %q missing", test.marker)
			}
			offset += len(test.marker)
			if got := classifyCursorContext(test.source, offset, nil).Kind; got != test.kind {
				t.Fatalf("context = %v, want %v", got, test.kind)
			}
		})
	}
}

func TestCursorContextAtIncompleteTemplateExpressionKeepsTemplateOwnership(t *testing.T) {
	source := `module page

func Card() string {
	return <span>{{ 
}
`
	offset := strings.Index(source, "{{ ") + len("{{ ")
	context := classifyCursorContext(source, offset, nil)
	if context.Kind != cursorContextTemplateExpression || !context.goCode() {
		t.Fatalf("incomplete template expression context = %#v, want TemplateExpression", context)
	}
}

func TestCursorContextAtBareComponentSigilInsideMarkupStaysTemplate(t *testing.T) {
	source := `module page

func Build() gox.Component {
	return <main>&Men</main>
}
`
	offset := strings.Index(source, "&Men") + len("&Men")
	context := classifyCursorContext(source, offset, nil)
	if context.Kind != cursorContextTemplateSigil || !context.HasSigil || context.Sigil.Sigil != '&' {
		t.Fatalf("bare component sigil context = %#v, want TemplateSigil", context)
	}
}

func TestCursorContextAtDoesNotTreatGoComparisonAsMarkup(t *testing.T) {
	source := `module page

func Compare(a, b, c int) bool {
	return a < b && b > c
}
`
	offset := strings.Index(source, "b > c") + len("b")
	if got := classifyCursorContext(source, offset, nil).Kind; got != cursorContextGo {
		t.Fatalf("comparison context = %v, want Go", got)
	}
}

func TestCursorContextAtTreatsAddressInsideTemplateExpressionAsGoExpression(t *testing.T) {
	source := `module page

func Build(text string) gox.Component {
	return <div>{{ &text }}</div>
}
`
	offset := strings.Index(source, "&text") + len("&text")
	context := classifyCursorContext(source, offset, nil)
	if context.Kind != cursorContextTemplateExpression {
		t.Fatalf("{{ &text }} context = %v, want TemplateExpression", context.Kind)
	}
	if context.Kind == cursorContextTemplateSigil {
		t.Fatal("template expression address operator was captured as component sigil")
	}
}
