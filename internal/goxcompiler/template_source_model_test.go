package gox

import (
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/goxmap"
)

func TestTemplateLexerPreservesExactGoExpressionRanges(t *testing.T) {
	const base = 200
	source := `<div value={{   reflect.ValueOf(&text).Elem()   }}>{{ text }}</div>`
	lexer := NewTemplateLexerAtOffset(source, "pages/index.gox", 7, 3, base)
	tokens, err := lexer.LexAll()
	if err != nil {
		t.Fatal(err)
	}

	var expressions []TemplateToken
	for _, token := range tokens {
		if token.Kind == TokenExpr {
			expressions = append(expressions, token)
		}
	}
	if len(expressions) != 2 {
		t.Fatalf("expression token count = %d, want 2", len(expressions))
	}

	assertTemplateRangeText(t, source, base, expressions[0].Source, `{{   reflect.ValueOf(&text).Elem()   }}`)
	assertTemplateRangeText(t, source, base, expressions[0].GoSource, `reflect.ValueOf(&text).Elem()`)
	assertTemplateRangeText(t, source, base, expressions[1].Source, `{{ text }}`)
	assertTemplateRangeText(t, source, base, expressions[1].GoSource, `text`)
}

func TestTemplateParserPreservesAttributeAndExpressionRanges(t *testing.T) {
	const base = 75
	source := `<:for index user range={{ users }}><&Menu text={{ &user.Name }}/></:for>`
	tpl, err := ParseTemplateAtOffset(source, "pages/index.gox", 4, 2, base)
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Source.URI != "pages/index.gox" || tpl.Source.Range != (goxmap.Range{Start: base, End: base + len(source)}) {
		t.Fatalf("template source = %#v", tpl.Source)
	}

	directive, ok := tpl.Root.(*DirectiveNode)
	if !ok {
		t.Fatalf("root = %T, want *DirectiveNode", tpl.Root)
	}
	if len(directive.Attrs) != 3 {
		t.Fatalf("directive attrs = %d, want 3", len(directive.Attrs))
	}
	assertTemplateRangeText(t, source, base, directive.Attrs[0].NameSource, "index")
	assertTemplateRangeText(t, source, base, directive.Attrs[1].NameSource, "user")
	assertTemplateRangeText(t, source, base, directive.Attrs[2].NameSource, "range")
	assertTemplateRangeText(t, source, base, directive.Attrs[2].ExprSource, "users")

	if len(directive.Children) != 1 {
		t.Fatalf("directive children = %d, want 1", len(directive.Children))
	}
	component, ok := directive.Children[0].(*ComponentNode)
	if !ok {
		t.Fatalf("directive child = %T, want *ComponentNode", directive.Children[0])
	}
	if len(component.Attrs) != 1 {
		t.Fatalf("component attrs = %d, want 1", len(component.Attrs))
	}
	assertTemplateRangeText(t, source, base, component.Attrs[0].NameSource, "text")
	assertTemplateRangeText(t, source, base, component.Attrs[0].ExprSource, "&user.Name")
}

func TestParseModuleRegistersReturnAndValueTemplateOccurrences(t *testing.T) {
	source := `module page

import (
    gox "github.com/gox-lang/gox"
    "reflect"
    "strings"
)

func Build() gox.Component {
    text := "hello"
    view := <span title={{ strings.TrimSpace(text) }}>{{ reflect.ValueOf(&text).Elem() }}</span>
    _ = view
    return <main data={{ &text }}>{{strings.NewReplacer("a", "b").Replace(text)}}</main>
}
`
	module, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(module.Templates) != 1 {
		t.Fatalf("legacy return templates = %d, want 1", len(module.Templates))
	}
	if len(module.TemplateOccurrences) != 2 {
		t.Fatalf("template occurrences = %d, want 2", len(module.TemplateOccurrences))
	}

	value := module.TemplateOccurrences[0]
	returned := module.TemplateOccurrences[1]
	if value.Kind != TemplateOccurrenceValue || returned.Kind != TemplateOccurrenceReturn {
		t.Fatalf("occurrence kinds = %q, %q", value.Kind, returned.Kind)
	}
	assertSourceRangeText(t, source, value.Source, `<span title={{ strings.TrimSpace(text) }}>{{ reflect.ValueOf(&text).Elem() }}</span>`)
	assertSourceRangeText(t, source, value.Content, `<span title={{ strings.TrimSpace(text) }}>{{ reflect.ValueOf(&text).Elem() }}</span>`)
	assertSourceRangeText(t, source, returned.Source, `<main data={{ &text }}>{{strings.NewReplacer("a", "b").Replace(text)}}</main>`)
	assertSourceRangeText(t, source, returned.Content, `<main data={{ &text }}>{{strings.NewReplacer("a", "b").Replace(text)}}</main>`)

	valueExpressions := templateExpressionSources(value.Template)
	if len(valueExpressions) != 2 {
		t.Fatalf("value template Go expressions = %d, want 2", len(valueExpressions))
	}
	assertSourceRangeText(t, source, valueExpressions[0], `strings.TrimSpace(text)`)
	assertSourceRangeText(t, source, valueExpressions[1], `reflect.ValueOf(&text).Elem()`)

	returnExpressions := templateExpressionSources(returned.Template)
	if len(returnExpressions) != 2 {
		t.Fatalf("return template Go expressions = %d, want 2", len(returnExpressions))
	}
	assertSourceRangeText(t, source, returnExpressions[0], `&text`)
	assertSourceRangeText(t, source, returnExpressions[1], `strings.NewReplacer("a", "b").Replace(text)`)
}

func TestTemplateOccurrenceDistinguishesFragmentSyntaxFromContent(t *testing.T) {
	source := `module page

import gox "github.com/gox-lang/gox"

func Build() gox.Component {
    text := "hello"
    view := <>
        <span>{{text}}</span>
    </>
    return view
}
`
	module, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(module.TemplateOccurrences) != 1 {
		t.Fatalf("template occurrences = %d, want 1", len(module.TemplateOccurrences))
	}
	occurrence := module.TemplateOccurrences[0]
	if occurrence.Kind != TemplateOccurrenceValue {
		t.Fatalf("occurrence kind = %q, want value", occurrence.Kind)
	}
	full := source[occurrence.Source.Range.Start:occurrence.Source.Range.End]
	if !strings.HasPrefix(full, "<>") || !strings.HasSuffix(full, "</>") {
		t.Fatalf("full fragment source = %q", full)
	}
	content := source[occurrence.Content.Range.Start:occurrence.Content.Range.End]
	if strings.HasPrefix(content, "<>") || strings.HasSuffix(content, "</>") {
		t.Fatalf("fragment content unexpectedly includes delimiters: %q", content)
	}
	if occurrence.Raw != content {
		t.Fatalf("occurrence raw = %q, content = %q", occurrence.Raw, content)
	}
	expressions := templateExpressionSources(occurrence.Template)
	if len(expressions) != 1 {
		t.Fatalf("fragment expressions = %d, want 1", len(expressions))
	}
	assertSourceRangeText(t, source, expressions[0], "text")
}

func assertTemplateRangeText(t *testing.T, local string, base int, source goxmap.SourceRange, want string) {
	t.Helper()
	if source.URI != "pages/index.gox" {
		t.Fatalf("source URI = %q, want pages/index.gox", source.URI)
	}
	start := source.Range.Start - base
	end := source.Range.End - base
	if start < 0 || end < start || end > len(local) {
		t.Fatalf("range %#v is outside local source base %d", source.Range, base)
	}
	if got := local[start:end]; got != want {
		t.Fatalf("range %#v = %q, want %q", source.Range, got, want)
	}
}

func assertSourceRangeText(t *testing.T, source string, rng goxmap.SourceRange, want string) {
	t.Helper()
	if rng.URI != "pages/index.gox" {
		t.Fatalf("source URI = %q, want pages/index.gox", rng.URI)
	}
	if !rng.Range.Valid() || rng.Range.End > len(source) {
		t.Fatalf("invalid source range %#v for source length %d", rng.Range, len(source))
	}
	if got := source[rng.Range.Start:rng.Range.End]; got != want {
		t.Fatalf("range %#v = %q, want %q", rng.Range, got, want)
	}
}

func templateExpressionSources(tpl *Template) []goxmap.SourceRange {
	if tpl == nil || tpl.Root == nil {
		return nil
	}
	var result []goxmap.SourceRange
	var visit func(TemplateNode)
	visit = func(node TemplateNode) {
		switch current := node.(type) {
		case *ExprNode:
			result = append(result, current.ExprSource)
		case *FragmentNode:
			for _, child := range current.Children {
				visit(child)
			}
		case *ElementNode:
			for _, attr := range current.Attrs {
				if attr.ExprSource.Range.Valid() && !attr.ExprSource.Range.Empty() {
					result = append(result, attr.ExprSource)
				}
			}
			for _, child := range current.Children {
				visit(child)
			}
		case *DirectiveNode:
			for _, attr := range current.Attrs {
				if attr.ExprSource.Range.Valid() && !attr.ExprSource.Range.Empty() {
					result = append(result, attr.ExprSource)
				}
			}
			for _, child := range current.Children {
				visit(child)
			}
		case *ComponentNode:
			for _, attr := range current.Attrs {
				if attr.ExprSource.Range.Valid() && !attr.ExprSource.Range.Empty() {
					result = append(result, attr.ExprSource)
				}
			}
			for _, child := range current.Children {
				visit(child)
			}
		case *ClientNode:
			for _, attr := range current.Attrs {
				if attr.ExprSource.Range.Valid() && !attr.ExprSource.Range.Empty() {
					result = append(result, attr.ExprSource)
				}
			}
			for _, child := range current.Children {
				visit(child)
			}
		}
	}
	visit(tpl.Root)
	return result
}
