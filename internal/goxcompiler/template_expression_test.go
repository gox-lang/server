package gox

import (
	"strings"
	"testing"
)

func TestTransformTemplateExpressionShortDeclaration(t *testing.T) {
	src := `import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	Image := <img src="/logo.png">
	return <>
		<div><&Image/></div>
	</>
}
`

	out, err := TransformTemplateExpressions("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, `Image := <`) {
		t.Fatalf("template expression was not rewritten:\n%s", out)
	}

	if !strings.Contains(out, `Image := func(goxWriter gox.Writer)`) {
		t.Fatalf("template expression did not become a component function:\n%s", out)
	}
}

func TestTransformTemplateExpressionVarComponentDeclaration(t *testing.T) {
	src := `import "github.com/gox-lang/gox"

var Image gox.Component = <img src="/logo.png">
`

	out, err := TransformTemplateExpressions("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, `gox.Component`) {
		t.Fatalf("gox.Component leaked into generated Go declaration:\n%s", out)
	}

	if !strings.Contains(out, `var Image = func(goxWriter gox.Writer)`) {
		t.Fatalf("var component declaration did not become a component function:\n%s", out)
	}
}

func TestParseModuleTemplateExpressionComponentUsage(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	Image := <img src="/logo.png">
	return <>
		<div><&Image/></div>
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestParseModuleVarComponentTemplateExpressionComponentUsage(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

var Image gox.Component = <img src="/logo.png">

func GET(ctx *gox.Context) gox.Page {
	return <>
		<div><&Image/></div>
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestTransformTemplateExpressionRejectsInferredVarDeclaration(t *testing.T) {
	src := `import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	var Image = <h1>Hello Templates</h1>
	return <>
		<div><&Image/></div>
	</>
}
`

	_, err := TransformTemplateExpressions("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected inferred var template declaration to be rejected")
	}

	if !strings.Contains(err.Error(), "template component expressions cannot be assigned with inferred var declarations") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseModuleReturnInlineTemplateTag(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <h1>Hello Gox</h1>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTemplateReturnsInlineTagKeepsGoExpressionReturn(t *testing.T) {
	src := `import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return value
}
`

	extracted, err := ExtractTemplateReturns("main", "pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	if len(extracted.Templates) != 0 {
		t.Fatalf("expected no templates, got %d", len(extracted.Templates))
	}

	if !strings.Contains(extracted.Source, "return value") {
		t.Fatalf("return expression was modified unexpectedly:\n%s", extracted.Source)
	}
}

func TestExtractTemplateReturnsBuildsStageSourceMap(t *testing.T) {
	src := `func GET(ctx *gox.Context) gox.Page {
	name := "George"
	return <h1>{{name}}</h1>
}
`

	extracted, err := ExtractTemplateReturns("main", "pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	if extracted.SourceMap == nil {
		t.Fatal("template return extraction did not produce a source map")
	}

	generatedName := strings.Index(extracted.Source, `name := "George"`)
	originalName := strings.Index(src, `name := "George"`)
	mapped, ok := extracted.SourceMap.OriginalPosition(generatedName)
	if !ok || !mapped.Exact || mapped.URI != "pages/index.gox" || mapped.Offset != originalName {
		t.Fatalf("copied Go mapping = %#v, %v", mapped, ok)
	}

	placeholder := strings.Index(extracted.Source, "__gox_main_GET_Template_1")
	template := strings.Index(src, "<h1>")
	mapped, ok = extracted.SourceMap.OriginalPosition(placeholder)
	if !ok || mapped.Exact || mapped.URI != "pages/index.gox" || mapped.Offset != template {
		t.Fatalf("generated placeholder mapping = %#v, %v", mapped, ok)
	}
	reverse := extracted.SourceMap.GeneratedPositions("pages/index.gox", template+5)
	if len(reverse) != 1 || reverse[0].Exact || reverse[0].Offset != placeholder {
		t.Fatalf("template reverse mapping = %#v", reverse)
	}

	originalReturn := strings.Index(src, "return")
	generatedReturn := strings.Index(extracted.Source, "return")
	mapped, ok = extracted.SourceMap.OriginalPosition(generatedReturn + 2)
	if !ok || !mapped.Exact || mapped.Offset != originalReturn+2 {
		t.Fatalf("return keyword mapping = %#v, %v", mapped, ok)
	}
}

func TestTransformTemplateExpressionsComposesReturnAndExpressionMaps(t *testing.T) {
	src := `func GET(ctx *gox.Context) gox.Page {
	Image := <img src="/george.png">
	return <main><&Image/></main>
}
`

	extracted, err := ExtractTemplateReturns("main", "pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	transformed, err := TransformTemplateExpressionsMapped("main", "pages/index.gox", extracted.Source, extracted.SourceMap)
	if err != nil {
		t.Fatal(err)
	}

	componentFunc := strings.Index(transformed.Source, "func(goxWriter gox.Writer)")
	componentTemplate := strings.Index(src, `<img src="/george.png">`)
	mapped, ok := transformed.SourceMap.OriginalPosition(componentFunc)
	if !ok || mapped.Exact || mapped.URI != "pages/index.gox" || mapped.Offset != componentTemplate {
		t.Fatalf("component function mapping = %#v, %v", mapped, ok)
	}

	placeholder := strings.Index(transformed.Source, "__gox_main_GET_Template_1")
	returnTemplate := strings.Index(src, "<main>")
	mapped, ok = transformed.SourceMap.OriginalPosition(placeholder)
	if !ok || mapped.Exact || mapped.URI != "pages/index.gox" || mapped.Offset != returnTemplate {
		t.Fatalf("return placeholder mapping after composition = %#v, %v", mapped, ok)
	}
}

func TestTransformTemplateExpressionsMapsComponentDeclarationRewrite(t *testing.T) {
	src := `var Image gox.Component = <img src="/george.png">`
	transformed, err := TransformTemplateExpressionsMapped("main", "pages/index.gox", src, nil)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Index(transformed.Source, "var Image = ")
	original := strings.Index(src, "var Image gox.Component = ")
	mapped, ok := transformed.SourceMap.OriginalPosition(rewritten)
	if !ok || mapped.Exact || mapped.Offset != original {
		t.Fatalf("component declaration rewrite mapping = %#v, %v", mapped, ok)
	}
	generatedName := strings.Index(transformed.Source, "Image")
	originalName := strings.Index(src, "Image")
	mapped, ok = transformed.SourceMap.OriginalPosition(generatedName)
	if !ok || !mapped.Exact || mapped.Offset != originalName {
		t.Fatalf("component declaration name mapping = %#v, %v", mapped, ok)
	}
}

func TestParseModuleReturnFragmentTemplate(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <>
		Hello
		<h1>Gox</h1>
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestTransformTemplateExpressionFragmentShortDeclaration(t *testing.T) {
	src := `import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	Image := <>
		Hello
		<img src="/logo.png">
	</>
	return <>
		<div><&Image/></div>
	</>
}
`

	out, err := TransformTemplateExpressions("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, `Image := <>`) {
		t.Fatalf("template fragment expression was not rewritten:\n%s", out)
	}

	if !strings.Contains(out, `Image := func(goxWriter gox.Writer)`) {
		t.Fatalf("template fragment expression did not become a component function:\n%s", out)
	}
}

func TestParseModuleFragmentComponentExpressionUsage(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	Image := <>
		Hello
		<img src="/logo.png">
	</>
	return <>
		<div><&Image/></div>
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestParseModuleRejectsParenthesizedTemplateReturn(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return (
		<h1>Hello Gox</h1>
	)
}
`

	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected parenthesized template return to be rejected")
	}

	if !strings.Contains(err.Error(), "template returns no longer use parentheses") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseModuleRejectsMultilineTemplateAfterBareReturn(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return
		<div>
			<h1>Hello Gox</h1>
		</div>
}
`

	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected multiline template after bare return to be rejected")
	}

	if !strings.Contains(err.Error(), "multiline template return requires a fragment") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseModuleTemplateDoubleBraceExpression(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	name := "Gox"
	return <>
		Hello {{name}}
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestParseModuleTemplateSingleBracesAreText(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <>
		function test() {
			return 1
		}
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestParseModuleTemplateDoubleBraceRawExpression(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	html := "<strong>Gox</strong>"
	return <>
		{{<html>}}
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestParseModuleTemplateDoubleBraceAttributeExpression(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	title := "Gox"
	return <div title={{title}}>Hello</div>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestParseModuleTemplateLessThanIsText(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <>
		2 < 3
		a <= b
		x < y && y > z
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestParseModuleTemplateLessThanInsideScriptIsText(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <>
		<script>
			if (a < b) {
				console.log(a)
			}
		</script>
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestParseModuleTemplateBackslashEscapesSpecialCharacters(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <>
		\<not-html>
		\{{name}}
		\\server
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestParseModuleTemplateBackslashEscapesFragmentClose(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <>
		literal \</> marker
	</>
}
`

	if _, err := ParseModule("pages/index.gox", src); err != nil {
		t.Fatal(err)
	}
}

func TestTransformTemplateExpressionUsesOwnStaticDictionary(t *testing.T) {
	src := `import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	Hola := <span>Mundo</span>
	return <div><&Hola/></div>
}
`

	out, statics, err := TransformTemplateExpressionsWithStatics("main", "pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, `[]byte("<span>Mundo</span>")`) || strings.Contains(out, `[]byte("<span>")`) {
		t.Fatalf("inline component still converts static text inside request path:\n%s", out)
	}

	if len(statics) != 1 {
		t.Fatalf("expected one static dictionary, got %d: %#v", len(statics), statics)
	}

	if statics[0].Name != "goxStaticMainGETHola" {
		t.Fatalf("expected semantic static name, got %q", statics[0].Name)
	}

	if len(statics[0].Literals) == 0 {
		t.Fatalf("expected inline component static literals, got none")
	}

	if !strings.Contains(out, statics[0].Name+`[0]`) {
		t.Fatalf("inline component does not write from its own static dictionary %q:\n%s", statics[0].Name, out)
	}
}
