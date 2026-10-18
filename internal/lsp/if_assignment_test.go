package lsp

import (
	"strings"
	"testing"
)

func TestHoverIfAssignmentScopes(t *testing.T) {
	source := `module page
func findUser()(string,bool){return "user",false}
func Render(ctx *gox.Context) gox.Page {
 return <><:if-group>
 <:if user ok :assign={{ findUser() }} condition={{ ok }}>{{ user }}</:if>
 <:else-if admin found :assign={{ findUser() }} condition={{ found }}>{{ admin }}</:else-if>
 <:else>{{ user }}{{ admin }}{{ ok }}{{ found }}</:else>
 </:if-group></>
}`
	doc := Document{URI: "file:///project/pages/index.gox", Path: "/project/pages/index.gox", Text: source}
	s := NewServer()
	if err := s.symbols.UpdateOpen(doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"user", "admin", "ok", "found"} {
		typ := "string"
		if name == "ok" || name == "found" {
			typ = "bool"
		}
		assertLocalHover(t, s, doc, strings.LastIndex(source, "{{ "+name+" }}")+3, "var "+name+" "+typ)
	}
}

func TestIfAssignmentDefinitionReferencesRename(t *testing.T) {
	source := `module page
func Render() gox.Component {
 user := "outside"
 return <><:if-group>
 <:if user :assign={{ user + "-first" }} condition={{ user == "first" }}>{{ user }}</:if>
 <:else-if user :assign={{ user + "-second" }} condition={{ false }}>{{ user }}</:else-if>
 <:else>{{ user }}</:else>
 </:if-group>{{ user }}</>
}`
	doc := Document{URI: "file:///project/pages/index.gox", Path: "/project/pages/index.gox", Text: source}
	s := NewServer()
	if err := s.symbols.UpdateOpen(doc); err != nil {
		t.Fatal(err)
	}
	outer := strings.Index(source, "user :=")
	first := strings.Index(source, "<:if user") + len("<:if ")
	second := strings.Index(source, "<:else-if user") + len("<:else-if ")
	rhs1 := strings.Index(source, `user + "-first"`)
	rhs2 := strings.Index(source, `user + "-second"`)
	use1 := strings.Index(source, "{{ user }}") + 3
	use2 := strings.Index(source[use1:], "{{ user }}") + use1 + 3
	use3 := strings.Index(source[use2:], "{{ user }}") + use2 + 3
	outside := strings.LastIndex(source, "{{ user }}") + 3
	cond := strings.Index(source, `user == "first"`)
	for _, pair := range [][2]int{{rhs1, outer}, {rhs2, first}, {use1, first}, {use2, second}, {use3, second}, {outside, outer}} {
		assertDefinitionOffset(t, s, doc, pair[0], pair[1])
	}
	refs := s.references(doc, referencePosition(t, doc, first), true)
	assertReferenceOffsets(t, doc, refs, first, cond, use1, rhs2)
	edit := s.rename(doc, referencePosition(t, doc, first), "person")
	if edit == nil {
		t.Fatal("rename returned nil")
	}
	assertRenameOffsets(t, doc, edit.Changes[doc.URI], "person", first, cond, use1, rhs2)
}

func TestIfAssignmentAttributeCompletion(t *testing.T) {
	for _, tag := range []string{"if", "else-if"} {
		source := `module page
func Render() interface{} {return <:` + tag + ` user :as/*cursor*/></:` + tag + `>}`
		doc := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
		requireCompletionItem(t, lexicalItemsAtMarker(t, NewServer(), doc, "/*cursor*/"), ":assign")
	}
}

func TestIfAssignmentCommaOkTypes(t *testing.T) {
	source := `module page
type User struct { Name string }
func Render(ch chan string, users map[string]*User, value any) gox.Component {
 return <:if-group>
 <:if message received :assign={{ <-ch }} condition={{ received }}>{{ message }}</:if>
 <:else-if mapped exists :assign={{ users["id"] }} condition={{ exists }}>{{ mapped.Name }}</:else-if>
 <:else-if typed valid :assign={{ value.(*User) }} condition={{ valid }}>{{ typed.Name }}</:else-if>
 <:else>{{ message }}{{ received }}{{ mapped }}{{ exists }}{{ typed }}{{ valid }}</:else>
 </:if-group>
}`
	doc := Document{URI: "file:///project/pages/index.gox", Path: "/project/pages/index.gox", Text: source}
	s := NewServer()
	if err := s.symbols.UpdateOpen(doc); err != nil {
		t.Fatal(err)
	}
	for name, typ := range map[string]string{"message": "string", "received": "bool", "mapped": "*User", "exists": "bool", "typed": "*User", "valid": "bool"} {
		use := strings.LastIndex(source, "{{ "+name+" }}") + 3
		assertLocalHover(t, s, doc, use, "var "+name+" "+typ)
		declaration := strings.Index(source, name+" ")
		assertDefinitionOffset(t, s, doc, use, declaration)
	}
}
