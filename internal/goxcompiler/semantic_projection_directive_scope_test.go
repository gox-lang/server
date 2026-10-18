package gox

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func TestSemanticProjectionForBindingsUseGoTypesScopes(t *testing.T) {
	source := `module page

type User struct { Name string }

func Build(users []User) interface{} {
	return <ul>
		<:for index user range={{ users }}>
			{{ index }}
			{{ user.Name }}
		</:for>
	</ul>
}
`
	projection, semantic := semanticProjectionForTest(t, source)
	if len(semantic.Errors) != 0 {
		t.Fatalf("semantic errors = %v\nprojection:\n%s", semantic.Errors, projection.Source)
	}

	bindingIndexOffset := strings.Index(source, "<:for index") + len("<:for ")
	bindingUserOffset := strings.Index(source, "<:for index user") + len("<:for index ")
	indexUseOffset := strings.Index(source, "{{ index }}") + len("{{ ")
	userUseOffset := strings.Index(source, "{{ user.Name }}") + len("{{ ")

	indexDef := exactSemanticIdentAtOffset(t, projection, bindingIndexOffset, "index")
	userDef := exactSemanticIdentAtOffset(t, projection, bindingUserOffset, "user")
	indexUse := exactSemanticIdentAtOffset(t, projection, indexUseOffset, "index")
	userUse := exactSemanticIdentAtOffset(t, projection, userUseOffset, "user")

	indexObject := semantic.Info.Defs[indexDef]
	userObject := semantic.Info.Defs[userDef]
	if indexObject == nil || userObject == nil {
		t.Fatalf("missing range binding definitions: index=%v user=%v", indexObject, userObject)
	}
	if got := semanticTypeString(indexObject.Type()); got != "int" {
		t.Fatalf("index type = %q, want int", got)
	}
	if got := semanticTypeString(userObject.Type()); got != "User" {
		t.Fatalf("user type = %q, want User", got)
	}
	if semantic.Info.ObjectOf(indexUse) != indexObject {
		t.Fatal("template index use does not resolve to range binding")
	}
	if semantic.Info.ObjectOf(userUse) != userObject {
		t.Fatal("template user use does not resolve to range binding")
	}

	var nameSelector *ast.SelectorExpr
	ast.Inspect(projection.File, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel != nil && selector.Sel.Name == "Name" {
			nameSelector = selector
		}
		return true
	})
	if nameSelector == nil || semantic.Info.Selections[nameSelector] == nil {
		t.Fatal("user.Name is missing its go/types field selection")
	}
	if got := semanticTypeString(semantic.Info.TypeOf(nameSelector)); got != "string" {
		t.Fatalf("user.Name type = %q, want string", got)
	}
}

func TestSemanticProjectionAdvancedForScopesBindingsOnlyInsideEach(t *testing.T) {
	source := `module page

type User struct { Name string }

func Build(users []User) interface{} {
	return <div>
		<:for _ user range={{ users }}>
			<:empty>{{ user.Name }}</:empty>
			<:each>{{ user.Name }}</:each>
		</:for>
	</div>
}
`
	projection, semantic := semanticProjectionForTest(t, source)

	bindingOffset := strings.Index(source, "<:for _ user") + len("<:for _ ")
	emptyUseOffset := strings.Index(source, "{{ user.Name }}") + len("{{ ")
	eachUseOffset := strings.LastIndex(source, "{{ user.Name }}") + len("{{ ")

	binding := exactSemanticIdentAtOffset(t, projection, bindingOffset, "user")
	emptyUse := exactSemanticIdentAtOffset(t, projection, emptyUseOffset, "user")
	eachUse := exactSemanticIdentAtOffset(t, projection, eachUseOffset, "user")
	bindingObject := semantic.Info.Defs[binding]
	if bindingObject == nil {
		t.Fatal("advanced for user binding has no go/types definition")
	}
	if semantic.Info.ObjectOf(emptyUse) != nil {
		t.Fatal("for binding unexpectedly leaks into <:empty> scope")
	}
	if semantic.Info.ObjectOf(eachUse) != bindingObject {
		t.Fatal("for binding is not visible inside <:each> scope")
	}
	if !directiveSemanticErrorsContain(semantic.Errors, "undefined: user") {
		t.Fatalf("missing undefined user error for <:empty>: %v", semantic.Errors)
	}
}

func TestSemanticProjectionRepeatOnlyDeclaresExplicitBinding(t *testing.T) {
	source := `module page

func Build(count int) interface{} {
	first := <div><:repeat times={{ count }}>{{ i }}</:repeat></div>
	_ = first
	return <div><:repeat attempt times={{ count }}>{{ attempt }}</:repeat></div>
}
`
	projection, semantic := semanticProjectionForTest(t, source)

	implicitUseOffset := strings.Index(source, "{{ i }}") + len("{{ ")
	bindingOffset := strings.Index(source, "<:repeat attempt") + len("<:repeat ")
	explicitUseOffset := strings.Index(source, "{{ attempt }}") + len("{{ ")

	implicitUse := exactSemanticIdentAtOffset(t, projection, implicitUseOffset, "i")
	binding := exactSemanticIdentAtOffset(t, projection, bindingOffset, "attempt")
	explicitUse := exactSemanticIdentAtOffset(t, projection, explicitUseOffset, "attempt")

	if semantic.Info.ObjectOf(implicitUse) != nil {
		t.Fatal("repeat without binding unexpectedly declares implicit i")
	}
	bindingObject := semantic.Info.Defs[binding]
	if bindingObject == nil {
		t.Fatal("explicit repeat binding has no go/types definition")
	}
	if got := semanticTypeString(bindingObject.Type()); got != "int" {
		t.Fatalf("repeat binding type = %q, want int", got)
	}
	if semantic.Info.ObjectOf(explicitUse) != bindingObject {
		t.Fatal("repeat binding use does not resolve to explicit binding")
	}
	if !directiveSemanticErrorsContain(semantic.Errors, "undefined: i") {
		t.Fatalf("missing undefined i error for unbound repeat: %v", semantic.Errors)
	}
}

func TestSemanticProjectionPreservesDirectiveControlSemantics(t *testing.T) {
	source := `module page

const (
	Pending = "pending"
	Queued = "queued"
	Ready = "ready"
)

func Build(active bool, status string) interface{} {
	return <div>
		<:if-group>
			<:if condition={{ active }}>{{ status }}</:if>
			<:else-if condition={{ status == Pending }}>{{ Pending }}</:else-if>
			<:else>{{ Ready }}</:else>
		</:if-group>
		<:switch value={{ status }}>
			<:case is={{ Pending, Queued }} fallthrough>{{ Pending }}</:case>
			<:case is={{ Ready }}>{{ Ready }}</:case>
			<:default>{{ status }}</:default>
		</:switch>
	</div>
}
`
	projection, semantic := semanticProjectionForTest(t, source)
	if len(semantic.Errors) != 0 {
		t.Fatalf("directive semantic errors = %v\nprojection:\n%s", semantic.Errors, projection.Source)
	}

	var switchStmt *ast.SwitchStmt
	var fallthroughFound bool
	var firstCase *ast.CaseClause
	ast.Inspect(projection.File, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.SwitchStmt:
			switchStmt = current
		case *ast.CaseClause:
			if len(current.List) == 2 {
				firstCase = current
			}
		case *ast.BranchStmt:
			if current.Tok == token.FALLTHROUGH {
				fallthroughFound = true
			}
		}
		return true
	})
	if switchStmt == nil {
		t.Fatal("semantic projection is missing switch statement")
	}
	if firstCase == nil || len(firstCase.List) != 2 {
		t.Fatal("case is={{Pending, Queued}} was not projected as two Go case expressions")
	}
	if !fallthroughFound {
		t.Fatal("semantic projection is missing Go fallthrough")
	}
	for _, expression := range firstCase.List {
		origin, ok := projection.Origins.Get(expression)
		if !ok || !origin.Exact() {
			t.Fatalf("case expression origin = %#v, want exact", origin)
		}
	}
}

func TestSemanticProjectionDirectiveContextsRetainGoTypeRules(t *testing.T) {
	source := `module page

func Build(items []string, condition string, invalidRange bool) interface{} {
	return <div>
		<:if condition={{ condition }}>bad condition</:if>
		<:for value range={{ invalidRange }}>{{ value }}</:for>
	</div>
}
`
	_, semantic := semanticProjectionForTest(t, source)
	if !directiveSemanticErrorsContainAny(semantic.Errors, "non-boolean condition", "cannot convert condition", "mismatched types") {
		t.Fatalf("missing Go bool-condition diagnostic: %v", semantic.Errors)
	}
	if !directiveSemanticErrorsContainAny(semantic.Errors, "cannot range over invalidRange", "range over invalidRange") {
		t.Fatalf("missing Go range diagnostic: %v", semantic.Errors)
	}
}

func TestParseModuleSemanticModelResolvesTemplateLoopDeepChain(t *testing.T) {
	source := `module page

type User struct { Name string }
func (user User) Profile() User { return user }

func Build(users []User) interface{} {
	return <div>
		<:for _ user range={{ users }}>
			{{ user.Profile().Name }}
		</:for>
	</div>
}
`
	module, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(source, "Profile()") + 2
	semanticNode, ok := module.SemanticAtOffset(offset)
	if !ok || semanticNode.Ident == nil || semanticNode.Ident.Name != "Profile" || semanticNode.Object == nil {
		t.Fatalf("Profile semantic node = %#v", semanticNode)
	}
	nameOffset := strings.Index(source, ".Name") + 2
	nameNode, ok := module.SemanticAtOffset(nameOffset)
	if !ok || nameNode.Ident == nil || nameNode.Ident.Name != "Name" || nameNode.Object == nil {
		t.Fatalf("Name semantic node = %#v", nameNode)
	}
	if got := semanticTypeString(nameNode.Object.Type()); got != "string" {
		t.Fatalf("Name object type = %q, want string", got)
	}
}

func semanticProjectionForTest(t *testing.T, source string) (*SemanticProjection, *SemanticModel) {
	t.Helper()
	projection, err := BuildSemanticProjection("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	semantic := buildSemanticModelFromProjection(projection)
	if semantic == nil || semantic.Info == nil {
		t.Fatal("semantic projection did not produce go/types information")
	}
	return projection, semantic
}

func exactSemanticIdentAtOffset(t *testing.T, projection *SemanticProjection, offset int, name string) *ast.Ident {
	t.Helper()
	var best *ast.Ident
	bestLength := int(^uint(0) >> 1)
	ast.Inspect(projection.File, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name != name {
			return true
		}
		origin, ok := projection.Origins.Get(ident)
		if !ok || !origin.Exact() || origin.Source.URI != "pages/index.gox" {
			return true
		}
		rng := origin.Source.Range
		if offset < rng.Start || offset >= rng.End {
			return true
		}
		if length := rng.End - rng.Start; length < bestLength {
			best = ident
			bestLength = length
		}
		return true
	})
	if best == nil {
		t.Fatalf("missing exact semantic identifier %q at source offset %d", name, offset)
	}
	return best
}

func semanticTypeString(typ types.Type) string {
	if typ == nil {
		return ""
	}
	return types.TypeString(typ, func(*types.Package) string { return "" })
}

func directiveSemanticErrorsContain(errors []error, fragment string) bool {
	for _, err := range errors {
		if err != nil && strings.Contains(err.Error(), fragment) {
			return true
		}
	}
	return false
}

func directiveSemanticErrorsContainAny(errors []error, fragments ...string) bool {
	for _, fragment := range fragments {
		if directiveSemanticErrorsContain(errors, fragment) {
			return true
		}
	}
	return false
}
