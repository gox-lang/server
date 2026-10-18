package gox

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPhase16_2A2TemplateBoundaryMatchesParserDrivenReference(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{name: "element", source: `<main><span>{{value}}</span></main> + trailing`},
		{name: "void-root", source: `<img src="/logo.png"> + trailing`},
		{name: "self-closed-root", source: `<section/> + trailing`},
		{name: "nested-void", source: `<main><img src="/logo.png"><br><span>ok</span></main> + trailing`},
		{name: "component", source: `<&Card><span>{{value}}</span></&Card> + trailing`},
		{name: "client", source: `<@Menu><span>{{value}}</span></@Menu> + trailing`},
		{name: "directive", source: `<:if condition={{ok}}><span>{{value}}</span></:if> + trailing`},
		{name: "mismatched", source: `<main><span></main> + trailing`},
		{name: "unclosed", source: `<main><span>value</span>`},
		{name: "unfinished-open", source: `<main`},
		{name: "unfinished-nested-open", source: `<main><span`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			phase16A2AssertTemplateBoundaryMatchesReference(t, tc.source)
		})
	}

	for _, nodes := range []int{1, 8, 32, 128} {
		t.Run(fmt.Sprintf("generated-%d", nodes), func(t *testing.T) {
			phase16A2AssertTemplateBoundaryMatchesReference(t, phase16A2TemplateExpression(nodes)+" + trailing")
		})
	}
}

func TestPhase16_2A2ReadTemplateExpressionStopsAtFirstRoot(t *testing.T) {
	source := `<main><span>{{value}}</span></main><aside>second</aside>`
	raw, end, err := readTemplateExpressionByHTML("pages/index.gox", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := `<main><span>{{value}}</span></main>`
	if raw != want {
		t.Fatalf("raw template = %q, want %q", raw, want)
	}
	if end != len(want) {
		t.Fatalf("template end = %d, want %d", end, len(want))
	}
}

func BenchmarkPhase16_2A2LoadGraphScaling(b *testing.B) {
	for _, nodes := range []int{32, 128, 256, 512} {
		fixture := phase16LargeTemplateCompileFixture(b, nodes)
		b.Run(fmt.Sprintf("Nodes%d", nodes), func(b *testing.B) {
			phase16BenchmarkLoadGraph(b, fixture)
		})
	}
}

func BenchmarkPhase16_2A2TemplateBoundaryScaling(b *testing.B) {
	for _, nodes := range []int{32, 128, 256, 512} {
		source := phase16A2TemplateExpression(nodes) + " + trailing"
		b.Run(fmt.Sprintf("Linear/N%d", nodes), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, _, err := readTemplateExpressionRootTokens("pages/index.gox", source, 1, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("ParserDrivenReference/N%d", nodes), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, _, err := readTemplateExpressionRootTokensParserDriven("pages/index.gox", source, 1, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPhase16_2A2TemplateParseScaling(b *testing.B) {
	for _, nodes := range []int{32, 128, 256, 512} {
		raw := phase16A2TemplateExpression(nodes)
		b.Run(fmt.Sprintf("Nodes%d", nodes), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := ParseTemplateAtOffset(raw, "pages/index.gox", 1, 1, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPhase16_2A2ExtractTemplateReturnsScaling(b *testing.B) {
	for _, nodes := range []int{32, 128, 256, 512} {
		raw := phase16A2TemplateExpression(nodes)
		source := "func GET() interface{} {\n\tvalue := \"hello\"\n\treturn " + raw + "\n}\n"
		b.Run(fmt.Sprintf("Nodes%d", nodes), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := ExtractTemplateReturnsMapped("index", "pages/index.gox", source, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func phase16A2AssertTemplateBoundaryMatchesReference(t *testing.T, source string) {
	t.Helper()
	gotTokens, gotEnd, gotErr := readTemplateExpressionRootTokens("pages/index.gox", source, 1, 1)
	wantTokens, wantEnd, wantErr := readTemplateExpressionRootTokensParserDriven("pages/index.gox", source, 1, 1)

	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("error mismatch: got %v, want %v", gotErr, wantErr)
	}
	if gotErr != nil {
		if gotErr.Error() != wantErr.Error() {
			t.Fatalf("error text mismatch:\n got: %v\nwant: %v", gotErr, wantErr)
		}
		return
	}
	if gotEnd != wantEnd {
		t.Fatalf("boundary end = %d, want %d", gotEnd, wantEnd)
	}
	if !reflect.DeepEqual(gotTokens, wantTokens) {
		t.Fatalf("boundary tokens differ:\n got: %#v\nwant: %#v", gotTokens, wantTokens)
	}
}

func phase16A2TemplateExpression(nodes int) string {
	if nodes < 1 {
		nodes = 1
	}
	var source strings.Builder
	source.Grow(nodes*48 + 32)
	source.WriteString("<main>")
	for i := 0; i < nodes; i++ {
		source.WriteString(fmt.Sprintf(`<span data-index="%d">{{value}}</span>`, i))
	}
	source.WriteString("</main>")
	return source.String()
}
