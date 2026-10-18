package gox

import (
	"fmt"
	"strings"
	"testing"
)

func TestIfAssignmentRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, tpl, setup, html string
		calls                  int
	}{
		{"channel_receive", `<:if value ok :assign={{ <-ch }} condition={{ ok }}><b>{{ value }}</b></:if><:if-group><:if value ok :assign={{ <-ch }} condition={{ ok }}><b>{{ value }}</b></:if><:else><b>closed</b></:else></:if-group>`, `var ch=make(chan string,1);func init(){ch<-"message";close(ch)}`, `<b>message</b><b>closed</b>`, 0},
		{"type_assertion", `<:if user ok :assign={{ value.(*User) }} condition={{ ok }}><b>{{ user.Name }}</b></:if><:if-group><:if user ok :assign={{ other.(*User) }} condition={{ ok }}><b>{{ user.Name }}</b></:if><:else><b>{{ user == nil }}</b></:else></:if-group>`, `type User struct{Name string};var value any=&User{Name:"Ana"};var other any="wrong"`, `<b>Ana</b><b>true</b>`, 0},
		{"standalone", `<:if user ok :assign={{ find() }} condition={{ ok }}><b>{{ user }}</b></:if>`, `func find()(string,bool){outerCalls++;return "user",true}`, `<b>user</b>`, 1},
		{"chain_scope", `<:if-group><:if user ok :assign={{ find() }} condition={{ ok }}><b>wrong</b></:if><:else-if admin found :assign={{ findAdmin(user) }} condition={{ found }}><b>wrong</b></:else-if><:else-if condition={{ user == "user" && admin == "admin" && !ok && !found }}><b>{{ user }}:{{ admin }}</b></:else-if><:else><b>wrong</b></:else></:if-group>`, `func find()(string,bool){outerCalls++;return "user",false};func findAdmin(user string)(string,bool){outerCalls++;return "admin",false}`, `<b>user:admin</b>`, 2},
		{"lazy_initializer", `<:if-group><:if user ok :assign={{ find() }} condition={{ ok }}><b>{{ user }}</b></:if><:else-if user ok :assign={{ find() }} condition={{ ok }}><b>{{ user }}</b></:else-if></:if-group>`, `func find()(string,bool){outerCalls++;return "first",true}`, `<b>first</b>`, 1},
		{"shadow_rhs_and_else", `<:if-group><:if user :assign={{ "outer" }} condition={{ false }}><b>wrong</b></:if><:else-if user :assign={{ user + "-inner" }} condition={{ false }}><b>wrong</b></:else-if><:else><b>{{ user }}</b></:else></:if-group><b>{{ user }}</b>`, `var user="outside"`, `<b>outer-inner</b><b>outside</b>`, 0},
		{"map_comma_ok", `<:if user ok :assign={{ users["id"] }} condition={{ ok }}><b>{{ user }}</b></:if>`, `var users=map[string]string{"id":"map-user"}`, `<b>map-user</b>`, 0},
		{"blank", `<:if _ ok :assign={{ find() }} condition={{ ok }}><b>yes</b></:if>`, `func find()(string,bool){outerCalls++;return "user",true}`, `<b>yes</b>`, 1},
		{"nested", `<:repeat i times={{ 2 }}><:if user :assign={{ i }} condition={{ user >= 0 }}><:if user :assign={{ user + 10 }} condition={{ user > 0 }}><b>{{ user }}</b></:if><b>{{ user }}</b></:if></:repeat>`, ``, `<b>10</b><b>0</b><b>11</b><b>1</b>`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderLoopTemplate(t, tc.tpl, tc.setup)
			want := fmt.Sprintf("outer=%d inner=0 html=%q", tc.calls, tc.html)
			if got != want {
				t.Fatalf("got %s; want %s", got, want)
			}
		})
	}
}

func TestIfAssignmentInvalidSyntax(t *testing.T) {
	for _, tpl := range []string{
		`<:if user condition={{ true }}/>`,
		`<:if :assign={{ 1 }} condition={{ true }}/>`,
		`<:if _ :assign={{ 1 }} condition={{ true }}/>`,
		`<:if user :assign="value" condition={{ true }}/>`,
		`<:if user user :assign={{ find() }} condition={{ true }}/>`,
		`<:if user :assign={{ 1 }} :assign={{ 2 }} condition={{ true }}/>`,
		`<:if :assign={{ 1 }} user condition={{ true }}/>`,
		`<:if-group><:if condition={{ false }}/><:else user :assign={{ 1 }}/></:if-group>`,
	} {
		t.Run(tpl, func(t *testing.T) {
			_, err := ParseModule("pages/index.gox", `module index
func Build() interface{} {return `+tpl+`}`)
			if err == nil {
				t.Fatal("invalid assignment accepted")
			}
		})
	}
}

func TestIfAssignmentSemanticScopes(t *testing.T) {
	source := `module page
type User struct { Active bool }
func findUser()(User,bool){return User{},false}
func findAdmin(user User)(User,bool){return user,false}
func Build() interface{} {
 user := "outside"
 return <><:if-group>
 <:if user ok :assign={{ findUser() }} condition={{ ok }}>{{ user.Active }}</:if>
 <:else-if admin found :assign={{ findAdmin(user) }} condition={{ found }}>{{ admin.Active }}</:else-if>
 <:else-if condition={{ user.Active }}>{{ admin.Active }}{{ ok }}{{ found }}</:else-if>
 <:else>{{ user.Active }}{{ admin.Active }}</:else>
 </:if-group>{{ user }}</>
}`
	projection, semantic := semanticProjectionForTest(t, source)
	if len(semantic.Errors) != 0 {
		t.Fatalf("errors: %v\n%s", semantic.Errors, projection.Source)
	}
	for _, name := range []string{"user", "ok", "admin", "found"} {
		marker := "<:if user "
		if name == "admin" || name == "found" {
			marker = "<:else-if admin "
		}
		start := strings.Index(source, marker)
		offset := start + strings.Index(source[start:], name)
		id := exactSemanticIdentAtOffset(t, projection, offset, name)
		obj := semantic.Info.Defs[id]
		if obj == nil {
			t.Fatalf("missing definition for %s", name)
		}
		for _, use := range semantic.Info.Uses {
			if use == obj {
				goto found
			}
		}
		t.Fatalf("no uses resolve to %s", name)
	found:
	}
}

func TestIfAssignmentRejectsGoTypeAndScopeErrors(t *testing.T) {
	for _, tpl := range []string{
		`<><:if user :assign={{ 1 }} condition={{ true }}>{{ user }}</:if>{{ user }}</>`,
		`<:if user ok :assign={{ 1 }} condition={{ ok }}>{{ user }}</:if>`,
		`<:if user :assign={{ 1 }} condition={{ user }}>{{ user }}</:if>`,
		`<:if-group><:if condition={{ admin == 1 }}/><:else-if admin :assign={{ 1 }} condition={{ true }}>{{ admin }}</:else-if></:if-group>`,
	} {
		t.Run(tpl, func(t *testing.T) {
			_, semantic := semanticProjectionForTest(t, "module page\nfunc Build() interface{} {return "+tpl+"}")
			if len(semantic.Errors) == 0 {
				t.Fatal("invalid Go semantics accepted")
			}
		})
	}
}
