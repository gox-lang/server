package gox

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatSourceFormatsGoAndTemplates(t *testing.T) {
	source := `module page
import (
 strings "strings"
 fmt "fmt"
)

// DisplayName keeps its comment.
func DisplayName( name string,count int )string{
 badge:=<span class="badge">{{  name }}</span>
 _=badge
 if count>0{return fmt.Sprintf("%s: %d",strings.TrimSpace(name),count)}
 return <article>
        <h1>{{ name }}</h1>
    </article>
}
`
	formatted, err := FormatSource(filepath.Join(t.TempDir(), "page.gox"), source)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func DisplayName(name string, count int) string {",
		"if count > 0 {\n\t\treturn fmt.Sprintf",
		`badge := <span class="badge">{{ name }}</span>`,
		"return <article>\n\t\t<h1>{{ name }}</h1>\n\t</article>",
		"// DisplayName keeps its comment.",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted source does not contain %q:\n%s", want, formatted)
		}
	}
	again, err := FormatSource(filepath.Join(t.TempDir(), "page.gox"), formatted)
	if err != nil || again != formatted {
		t.Fatalf("formatting is not stable: %v\nfirst:\n%s\nsecond:\n%s", err, formatted, again)
	}
}

func TestFormatSourceFormatsTemplateExpressionAndFragment(t *testing.T) {
	source := `module page
import fmt "fmt"
func Card(name string) {
	content := <>
<section class='card' data-name={{name}}>
<h2>{{ fmt.Sprintf("Hello, %s",name) }}</h2>
<&Badge active label={{name}} />
</section>
</>
	_ = content
}
`
	formatted, err := FormatSource(filepath.Join(t.TempDir(), "page.gox"), source)
	if err != nil {
		t.Fatal(err)
	}
	want := `content := <>
		<section class="card" data-name={{ name }}>
			<h2>{{ fmt.Sprintf("Hello, %s", name) }}</h2>
			<&Badge active label={{ name }} />
		</section>
	</>`
	if !strings.Contains(formatted, want) {
		t.Fatalf("fragment formatting mismatch:\n%s", formatted)
	}
}

func TestFormatSourcePreservesClientDeclaration(t *testing.T) {
	source := `module page

client Menu react {
  src: "menu.jsx"
  dev: false
}

func Build( value int )int{return value+1}
`
	formatted, err := FormatSource(filepath.Join(t.TempDir(), "page.gox"), source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(formatted, "client Menu react {\n  src: \"menu.jsx\"\n  dev: false\n}") {
		t.Fatalf("client declaration changed:\n%s", formatted)
	}
	if !strings.Contains(formatted, "func Build(value int) int { return value + 1 }") {
		t.Fatalf("Go declaration was not formatted:\n%s", formatted)
	}
}
