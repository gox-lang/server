package lsp

import (
	"strings"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestCursorContextTreatsStyleAndScriptContentAsPassiveEmbedded(t *testing.T) {
	source := `module page
import gox "github.com/gox-lang/gox"
func Card(value string) gox.Component {
	return <main>
		<style>.item { color: red; } {{ value }}</style>
		<script>const value = "ok"; {{ value }}</script>
		<div>{{ value }}</div>
	</main>
}
`
	module, err := goxcompiler.ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	for marker, want := range map[string]cursorContextKind{
		"color: red":         cursorContextEmbeddedStyle,
		"value }}</style>":   cursorContextEmbeddedStyle,
		`const value = "ok"`: cursorContextEmbeddedScript,
		"value }}</script>":  cursorContextEmbeddedScript,
	} {
		offset := strings.Index(source, marker)
		if offset < 0 {
			t.Fatalf("marker %q missing", marker)
		}
		offset++
		context := classifyCursorContext(source, offset, module)
		if context.Kind != want || context.goCode() {
			t.Fatalf("context at %q = %#v, want passive %v", marker, context, want)
		}
	}

	outside := strings.LastIndex(source, "value }}</div>")
	if outside < 0 {
		t.Fatal("outside template expression missing")
	}
	outside += len("value")
	if got := classifyCursorContext(source, outside, module).Kind; got != cursorContextTemplateExpression {
		t.Fatalf("ordinary template expression context = %v, want TemplateExpression", got)
	}
}

func TestCursorContextTreatsIncompleteStyleAndScriptAsPassiveEmbedded(t *testing.T) {
	for _, test := range []struct {
		tag  string
		kind cursorContextKind
	}{
		{tag: "style", kind: cursorContextEmbeddedStyle},
		{tag: "script", kind: cursorContextEmbeddedScript},
	} {
		source := "module page\nfunc Card() any {\n\treturn <" + test.tag + ">di"
		for _, offset := range []int{len(source) - 1, len(source)} {
			context := classifyCursorContext(source, offset, nil)
			if context.Kind != test.kind || context.goCode() {
				t.Fatalf("incomplete <%s> context at %d = %#v, want passive %v", test.tag, offset, context, test.kind)
			}
		}
	}
}

func TestCompletionIsDisabledInsideStyleAndScriptContent(t *testing.T) {
	source := `module page
import gox "github.com/gox-lang/gox"
func Card(value string) gox.Component {
	return <main>
		<style>di/*style*/ {{ val/*styleexpr*/ }}</style>
		<script>di/*script*/ {{ val/*scriptexpr*/ }}</script>
		<div>di/*html*/ {{ val/*go*/ }}</div>
	</main>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"/*style*/", "/*styleexpr*/", "/*script*/", "/*scriptexpr*/"} {
		if items := lexicalItemsAtMarker(t, server, document, marker); len(items) != 0 {
			t.Fatalf("completion at %s = %#v, want no GOX intelligence", marker, items)
		}
	}
	assertCompletionContainsSnippet(t, lexicalItemsAtMarker(t, server, document, "/*html*/"), "div", "<div>${1}</div>")
	assertCompletionLabels(t, lexicalItemsAtMarker(t, server, document, "/*go*/"), "value")
}

func TestLanguageIntelligenceIsDisabledInsideStyleAndScriptContent(t *testing.T) {
	source := `module page
import gox "github.com/gox-lang/gox"
func Card(value string) gox.Component {
	return <main>
		<style>{{ value }}</style>
		<script>{{ value }}</script>
		<div>{{ value }}</div>
	</main>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"value }}</style>", "value }}</script>"} {
		offset := strings.Index(source, needle)
		if offset < 0 {
			t.Fatalf("marker %q missing", needle)
		}
		position := referencePosition(t, document, offset+1)
		if hover := server.hover(document, position); hover != nil {
			t.Fatalf("hover inside embedded content = %#v, want nil", hover)
		}
		if definitions := server.definitionLocations(document, position); len(definitions) != 0 {
			t.Fatalf("definitions inside embedded content = %#v, want none", definitions)
		}
		if references := server.references(document, position, true); len(references) != 0 {
			t.Fatalf("references inside embedded content = %#v, want none", references)
		}
		if prepared := server.prepareRename(document, position); prepared != nil {
			t.Fatalf("prepare rename inside embedded content = %#v, want nil", prepared)
		}
		if linked := linkedEditingRanges(document, position); linked != nil {
			t.Fatalf("linked editing inside embedded content = %#v, want nil", linked)
		}
	}

	outside := strings.LastIndex(source, "value }}</div>")
	position := referencePosition(t, document, outside+1)
	if hover := server.hover(document, position); hover == nil {
		t.Fatal("ordinary template expression unexpectedly lost hover")
	}
}

func TestDiagnosticsIgnorePassiveStyleAndScriptContent(t *testing.T) {
	source := `module page
import gox "github.com/gox-lang/gox"
func Card() gox.Component {
	return <main>
		<style>{{ missingStyle }}</style>
		<script>{{ missingScript }}</script>
		<div>{{ missingHTML }}</div>
	</main>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, "missingStyle") || strings.Contains(diagnostic.Message, "missingScript") {
			t.Fatalf("passive embedded diagnostic leaked: %#v", diagnostic)
		}
	}
	assertDiagnosticAt(t, document, diagnostics, "undefined-identifier", "undefined: missingHTML", strings.Index(source, "missingHTML"))
}

func TestLinkedEditingIsDisabledInsideScriptAndStyleContent(t *testing.T) {
	source := `module page
func Card() any {
	return <main>
		<style><div></div></style>
		<script><span></span></script>
	</main>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	for _, needle := range []string{"<div>", "<span>"} {
		offset := strings.Index(source, needle) + 2
		position := referencePosition(t, document, offset)
		if linked := linkedEditingRanges(document, position); linked != nil {
			t.Fatalf("linked editing at %s = %#v, want nil", needle, linked)
		}
	}
}
