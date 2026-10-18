package gox

import (
	"fmt"
	"testing"
)

func TestConditionalRuntimeContract(t *testing.T) {
	for _, tc := range []struct {
		name, tpl, setup, html string
		outer, inner           int
	}{
		{"if_true", `<:if condition={{ check() }}><b>yes</b></:if>`, `func check()bool{outerCalls++;return true}`, `<b>yes</b>`, 1, 0},
		{"if_false", `<:if condition={{ check() }}><b>wrong</b></:if>`, `func check()bool{outerCalls++;return false}`, ``, 1, 0},
		{"if_short_circuit", `<:if condition={{ false && check() }}><b>wrong</b></:if>`, `func check()bool{outerCalls++;return true}`, ``, 0, 0},
		{"group_first", `<:if-group><:if condition={{ check(true) }}><b>first</b></:if><:else-if condition={{ check(true) }}><b>wrong</b></:else-if><:else><b>wrong</b></:else></:if-group>`, `func check(v bool)bool{outerCalls++;return v}`, `<b>first</b>`, 1, 0},
		{"group_second", `<:if-group><:if condition={{ check(false) }}><b>wrong</b></:if><:else-if condition={{ check(true) }}><b>second</b></:else-if><:else-if condition={{ check(true) }}><b>wrong</b></:else-if><:else><b>wrong</b></:else></:if-group>`, `func check(v bool)bool{outerCalls++;return v}`, `<b>second</b>`, 2, 0},
		{"group_else", `<:if-group><:if condition={{ check(false) }}><b>wrong</b></:if><:else-if condition={{ check(false) }}><b>wrong</b></:else-if><:else><b>else</b></:else></:if-group>`, `func check(v bool)bool{outerCalls++;return v}`, `<b>else</b>`, 2, 0},
		{"group_no_match", `<:if-group><:if condition={{ false }}><b>wrong</b></:if></:if-group>`, ``, ``, 0, 0},
		{"switch_order_multi_value", `<:switch value={{ value() }}><:case is={{ candidate(1), candidate(2) }}><b>match</b></:case><:case is={{ candidate(3) }}><b>wrong</b></:case><:default><b>wrong</b></:default></:switch>`, `func value()int{outerCalls++;return 2};func candidate(v int)int{innerCalls++;return v}`, `<b>match</b>`, 1, 2},
		{"switch_default", `<:switch value={{ value() }}><:case is={{ 1 }}><b>wrong</b></:case><:default><b>default</b></:default></:switch>`, `func value()int{outerCalls++;return 9}`, `<b>default</b>`, 1, 0},
		{"switch_no_match", `<:switch value={{ value() }}><:case is={{ 1 }}><b>wrong</b></:case></:switch>`, `func value()int{outerCalls++;return 9}`, ``, 1, 0},
		{"switch_fallthrough", `<:switch value={{ value() }}><:case is={{ 1 }} fallthrough><b>one</b></:case><:case is={{ candidate() }} fallthrough><b>two</b></:case><:default><b>end</b></:default></:switch>`, `func value()int{outerCalls++;return 1};func candidate()int{innerCalls++;return 9}`, `<b>one</b><b>two</b><b>end</b>`, 1, 0},
		{"nested_loops_conditions", `<:repeat n times={{ 2 }}><:if-group><:if condition={{ n == 0 }}><:switch value={{ n }}><:case is={{ 0 }}><:for _ v range={{ []int{3,4} }}><:if condition={{ v == 4 }}><b>{{ v }}</b></:if></:for></:case></:switch></:if><:else><:if-group><:if condition={{ true }}><b>end</b></:if></:if-group></:else></:if-group></:repeat>`, ``, `<b>4</b><b>end</b>`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderLoopTemplate(t, tc.tpl, tc.setup)
			want := fmt.Sprintf("outer=%d inner=%d html=%q", tc.outer, tc.inner, tc.html)
			if got != want {
				t.Fatalf("got %s; want %s", got, want)
			}
		})
	}
}

func TestConditionalInvalidStructures(t *testing.T) {
	for _, tpl := range []string{
		`<:If condition={{ true }}><p>x</p></:If>`,
		`<:if><p>x</p></:if>`, `<:if condition="true"><p>x</p></:if>`,
		`<:else />`, `<:else-if condition={{ true }} />`,
		`<:if-group />`, `<:if-group><:else /></:if-group>`,
		`<:if-group><:if condition={{ true }}/><:if condition={{ true }}/></:if-group>`,
		`<:if-group><:if condition={{ true }}/><:else/><:else-if condition={{ true }}/></:if-group>`,
		`<:if-group><:if condition={{ true }}/><:else/><:else/></:if-group>`,
		`<:if-group><p>outside</p><:if condition={{ true }}/></:if-group>`,
		`<:switch><:default/></:switch>`, `<:switch value={{ 1 }}/>`,
		`<:switch value={{ 1 }}><:case/></:switch>`,
		`<:switch value={{ 1 }}><:default/><:case is={{ 1 }}/></:switch>`,
		`<:switch value={{ 1 }}><:default/><:default/></:switch>`,
		`<:switch value={{ 1 }}><p>outside</p></:switch>`,
		`<:switch value={{ 1 }}><:case is={{ 1 }} fallthrough/></:switch>`,
		`<:switch value={{ 1 }}><:default fallthrough/></:switch>`,
		`<:case is={{ 1 }}/>`, `<:default/>`,
	} {
		t.Run(tpl, func(t *testing.T) {
			_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page {return `+tpl+`}`)
			if err == nil {
				t.Fatal("invalid conditional accepted")
			}
		})
	}
}
