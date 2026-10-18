package gox

import (
	"strings"
	"testing"
)

func TestTemplateLoopBindingsHaveTypesAndLexicalScopes(t *testing.T) {
	source := `module user

func Render(ctx *gox.Context) gox.Page {
	users := []string{"George", "Milo"}
	return <main>
		<:for index user range={{ users }}>
			<span>{{ index }} {{ user }}</span>
		</:for>
		<:repeat attempt times={{ 3 }}>
			<span>{{ attempt }}</span>
		</:repeat>
	</main>
}`
	module, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	index := requireTemplateLocal(t, module.LocalSymbols, "index", "var index int")
	user := requireTemplateLocal(t, module.LocalSymbols, "user", "var user string")
	attempt := requireTemplateLocal(t, module.LocalSymbols, "attempt", "var attempt int")
	for _, check := range []struct {
		symbol LocalSymbol
		use    string
	}{{index, "{{ index }}"}, {user, "{{ user }}"}, {attempt, "{{ attempt }}"}} {
		offset := strings.Index(source, check.use) + 3
		if !check.symbol.Scope.Range.Contains(offset) {
			t.Errorf("%s scope %#v does not contain use at %d", check.symbol.Name, check.symbol.Scope, offset)
		}
	}
}

func TestTemplateLoopBindingsRespectEmptyEachUnderscoreAndNesting(t *testing.T) {
	source := `module user

func Render(ctx *gox.Context) gox.Page {
	users := []string{"George", "Milo"}
	return <main>
		<:for _ user range={{ users }}>
			<:empty>{{ user }}</:empty>
			<:each>
				<:repeat index times={{ 2 }}>{{ user }} {{ index }}</:repeat>
			</:each>
		</:for>
	</main>
}`
	module, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	user := requireTemplateLocal(t, module.LocalSymbols, "user", "var user string")
	index := requireTemplateLocal(t, module.LocalSymbols, "index", "var index int")
	emptyUse := strings.Index(source, "{{ user }}") + 3
	eachUse := strings.LastIndex(source, "{{ user }}") + 3
	indexUse := strings.Index(source, "{{ index }}") + 3
	if user.Scope.Range.Contains(emptyUse) {
		t.Fatal("for value must not exist inside <:empty>")
	}
	if !user.Scope.Range.Contains(eachUse) || !index.Scope.Range.Contains(indexUse) {
		t.Fatal("nested each/repeat bindings do not contain their uses")
	}
	for _, symbol := range module.LocalSymbols {
		if symbol.Name == "_" {
			t.Fatal("blank identifier must not become a local symbol")
		}
	}
}

func TestTemplateRangeBindingTypesFollowGoRangeValues(t *testing.T) {
	tests := []struct {
		rangeType string
		key       string
		value     string
	}{
		{"[]string", "int", "string"},
		{"[...]User", "int", "User"},
		{"*[...]User", "int", "User"},
		{"string", "int", "rune"},
		{"map[string]User", "string", "User"},
		{"map[int]string", "int", "string"},
		{"<-chan Message", "Message", "any"},
		{"uint16", "uint16", "any"},
	}
	for _, test := range tests {
		key, value := templateRangeBindingTypes(test.rangeType)
		if key != test.key || value != test.value {
			t.Errorf("range %s = (%s, %s), want (%s, %s)", test.rangeType, key, value, test.key, test.value)
		}
	}
}

func requireTemplateLocal(t *testing.T, symbols []LocalSymbol, name, detail string) LocalSymbol {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Name == name && symbol.Detail == detail {
			return symbol
		}
	}
	t.Fatalf("missing template local %s with detail %q: %#v", name, detail, symbols)
	return LocalSymbol{}
}
