package gox

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func renderLoopTemplate(t *testing.T, template, setup string) string {
	t.Helper()
	tpl, err := ParseTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	stmts, err := GenerateTemplateStmts(tpl, &TemplateGenContext{WriterName: "w"})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), &ast.BlockStmt{List: stmts}); err != nil {
		t.Fatal(err)
	}
	src := `package main
import("fmt";"strings")
type writer struct {strings.Builder}
func(w *writer)WriteTextBool(v bool){fmt.Fprint(&w.Builder,v)}
func(w *writer)WriteTextAny(v any){fmt.Fprint(&w.Builder,v)}
func(w *writer)WriteTextInt(v int){fmt.Fprint(&w.Builder,v)}
func(w *writer)WriteTextString(v string){fmt.Fprint(&w.Builder,v)}
func __goxLine(...any){}
func __goxLineReset(){}
var outerCalls,innerCalls int
` + setup + "\nfunc render(w *writer)" + buf.String() + `
func main(){w:=&writer{};render(w);fmt.Printf("outer=%d inner=%d html=%q",outerCalls,innerCalls,w.String())}
`
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "go", "run", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, output, src)
	}
	return string(output)
}
func TestForEvaluatesRangeOnce(t *testing.T) {
	for _, outerAdvanced := range []bool{false, true} {
		for _, innerAdvanced := range []bool{false, true} {
			t.Run(fmt.Sprintf("nested_outerAdvanced=%v_innerAdvanced=%v", outerAdvanced, innerAdvanced), func(t *testing.T) {
				inner := `<:for index item range={{ inner(row) }}><p>{{ index }}:{{ item }}</p></:for>`
				if innerAdvanced {
					inner = `<:for index item range={{ inner(row) }}><:empty><b>innerEmpty</b></:empty><:each><p>{{ index }}:{{ item }}</p></:each></:for>`
				}
				outer := `<:for _ row range={{ outer() }}>` + inner + `</:for>`
				if outerAdvanced {
					outer = `<:for _ row range={{ outer() }}><:empty><b>outerEmpty</b></:empty><:each>` + inner + `</:each></:for>`
				}
				got := renderLoopTemplate(t, outer, `func outer()[]int{outerCalls++;return []int{0,2}}
 func inner(n int)[]int{innerCalls++;if n==0{return nil};return []int{5,6}}`)
				wantHTML := `<p>0:5</p><p>1:6</p>`
				if innerAdvanced {
					wantHTML = `<b>innerEmpty</b>` + wantHTML
				}
				want := fmt.Sprintf("outer=1 inner=2 html=%q", wantHTML)
				if got != want {
					t.Fatalf("got %s; want %s", got, want)
				}
			})
		}
	}
	t.Run("empty", func(t *testing.T) {
		got := renderLoopTemplate(t, `<:for range={{ outer() }}><:empty><b>empty</b></:empty><:each><p>each</p></:each></:for>`, `func outer()[]int{outerCalls++;return nil}`)
		if got != `outer=1 inner=0 html="<b>empty</b>"` {
			t.Fatal(got)
		}
	})
	t.Run("changing_result", func(t *testing.T) {
		got := renderLoopTemplate(t, `<:for range={{ outer() }}><:empty><b>empty</b></:empty><:each><p>each</p></:each></:for>`, `func outer()[]int{outerCalls++;if outerCalls==1{return []int{1}};return nil}`)
		if got != `outer=1 inner=0 html="<p>each</p>"` {
			t.Fatal(got)
		}
	})
}
func TestForPairedBlocksValidation(t *testing.T) {
	for _, body := range []string{`<:empty/>`, `<:each/>`, `<:each/><:empty/>`, `<:empty/><:empty/><:each/>`, `<:empty/><:each/><:each/>`, `<:empty/><:each/><p>outside</p>`} {
		tpl, err := ParseTemplate(`<:for range={{ items }}>` + body + `</:for>`)
		if err == nil {
			_, err = GenerateTemplateStmts(tpl, &TemplateGenContext{})
		}
		if err == nil {
			t.Errorf("accepted invalid %s", body)
		} else {
			t.Logf("%s: %v", body, err)
		}
	}
}

func TestAdvancedForTemporaryDoesNotCaptureUserNames(t *testing.T) {
	for _, values := range []string{"nil", "[]int{4}"} {
		t.Run(values, func(t *testing.T) {
			got := renderLoopTemplate(t, `<:for goxRange2 goxRange3 range={{ outer() }}><:empty>{{ goxRange1 }}</:empty><:each>{{ goxRange1 }}:{{ goxRange2 }}:{{ goxRange3 }}<:for range={{ inner(goxRange3) }}><:empty>{{ goxRange1 }}</:empty><:each>{{ goxRange1 }}</:each></:for>{{ goxRange1 }}</:each></:for>`,
				`var goxRange1 = "user"
func outer() []int { outerCalls++; return `+values+` }
func inner(n int) []int { innerCalls++; return []int{n} }`)
			want := `outer=1 inner=1 html="user:0:4useruser"`
			if values == "nil" {
				want = `outer=1 inner=0 html="user"`
			}
			if got != want {
				t.Fatalf("got %s; want %s", got, want)
			}
		})
	}
}

func TestAdvancedForCompilesInReturnAndInlineTemplates(t *testing.T) {
	phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
		entryRel: "pages/index.gox",
		source: `module index
import "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page {
 goxRange1 := "user"
 items := []int{1, 2}
 inline := <:for index item range={{ items }}><:empty>{{ goxRange1 }}</:empty><:each>{{ index }}:{{ item }}</:each></:for>
 return <:for index item range={{ items }}><:empty>{{ goxRange1 }}</:empty><:each><:repeat attemp times={{ item }}>{{ index }}:{{ attemp }}<&inline /></:repeat></:each></:for>
}
`,
	})
}

func TestMixedLoopRuntimeContracts(t *testing.T) {
	repeatSetup := `func outer()int{outerCalls++;return 2};func inner(n int)int{innerCalls++;return n+1}`
	for _, tc := range []struct{ name, template, setup, want string }{
		{"repeat_repeat", `<:repeat attemp times={{ outer() }}><:repeat retry times={{ inner(attemp) }}><p>{{ attemp }}:{{ retry }}</p></:repeat></:repeat>`, repeatSetup, `outer=1 inner=2 html="<p>0:0</p><p>1:0</p><p>1:1</p>"`},
		{"same_name_and_restore", `<:repeat attemp times={{ outer() }}><b>{{ attemp }}</b><:repeat attemp times={{ inner(attemp) }}><i>{{ attemp }}</i></:repeat><b>{{ attemp }}</b></:repeat>`, repeatSetup, `outer=1 inner=2 html="<b>0</b><i>0</i><b>0</b><b>1</b><i>0</i><i>1</i><b>1</b>"`},
		{"repeat_simple_for", `<:repeat attemp times={{ outer() }}><:for index item range={{ inner(attemp) }}><p>{{ attemp }}:{{ index }}:{{ item }}</p></:for></:repeat>`, `func outer()int{outerCalls++;return 2};func inner(n int)[]int{innerCalls++;return []int{n+10}}`, `outer=1 inner=2 html="<p>0:0:10</p><p>1:0:11</p>"`},
		{"simple_for_repeat", `<:for _ item range={{ outer() }}><:repeat attemp times={{ inner(item) }}><p>{{ item }}:{{ attemp }}</p></:repeat></:for>`, `func outer()[]int{outerCalls++;return []int{0,1}};func inner(n int)int{innerCalls++;return n+1}`, `outer=1 inner=2 html="<p>0:0</p><p>1:0</p><p>1:1</p>"`},
		{"repeat_advanced_for", `<:repeat attemp times={{ outer() }}><:for index item range={{ inner(attemp) }}><:empty><b>empty</b></:empty><:each><p>{{ attemp }}:{{ index }}:{{ item }}</p></:each></:for></:repeat>`, `func outer()int{outerCalls++;return 2};func inner(n int)[]int{innerCalls++;if n==0{return nil};return []int{11}}`, `outer=1 inner=2 html="<b>empty</b><p>1:0:11</p>"`},
		{"advanced_for_repeat", `<:for _ item range={{ outer() }}><:empty><b>empty</b></:empty><:each><:repeat attemp times={{ inner(item) }}><p>{{ item }}:{{ attemp }}</p></:repeat></:each></:for>`, `func outer()[]int{outerCalls++;return []int{0,1}};func inner(n int)int{innerCalls++;return n+1}`, `outer=1 inner=2 html="<p>0:0</p><p>1:0</p><p>1:1</p>"`},
		{"repeat_inside_empty", `<:for range={{ outer() }}><:empty><:repeat attemp times={{ inner(1) }}><i>{{ attemp }}</i></:repeat></:empty><:each><b>each</b></:each></:for>`, `func outer()[]int{outerCalls++;return nil};func inner(n int)int{innerCalls++;return n+1}`, `outer=1 inner=1 html="<i>0</i><i>1</i>"`},
		{"no_binding_zero_negative", `<:repeat times={{ 0 }}><b>bad</b></:repeat><:repeat times={{ -1 }}><b>bad</b></:repeat><:repeat times={{ outer() }}><i>ok</i></:repeat>`, `func outer()int{outerCalls++;return 2}`, `outer=1 inner=0 html="<i>ok</i><i>ok</i>"`},
		{"nested_break_continue", `<:repeat attemp times={{ 2 }}><:repeat retry times={{ 4 }}><:if condition={{ retry == 0 }}><:continue /></:if><:if condition={{ retry == 2 }}><:break /></:if><p>{{ attemp }}:{{ retry }}</p></:repeat><b>{{ attemp }}</b></:repeat>`, ``, `outer=0 inner=0 html="<p>0:1</p><b>0</b><p>1:1</p><b>1</b>"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderLoopTemplate(t, tc.template, tc.setup); got != tc.want {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
		})
	}
}
