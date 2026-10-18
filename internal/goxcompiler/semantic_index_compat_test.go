package gox

import "testing"

func TestSemanticIndexCompatibilityDefersTemplateDirectiveBindings(t *testing.T) {
	source := `module user

func Render(ctx *gox.Context) gox.Page {
	users := []string{"George", "Milo"}
	return <main>
		<:for index user range={{ users }}>
			<span>{{ index }} {{ user }}</span>
		</:for>
	</main>
}
`
	module, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	for _, identifier := range module.UnresolvedIdentifiers {
		if identifier.Name == "index" || identifier.Name == "user" {
			t.Fatalf("template binding %q leaked into legacy unresolved identifiers before semantic template scopes are enabled: %#v", identifier.Name, module.UnresolvedIdentifiers)
		}
	}
	if !hasLocalDetail(module.LocalSymbols, "index", "var index int") {
		t.Fatalf("legacy template index binding was lost: %#v", module.LocalSymbols)
	}
	if !hasLocalDetail(module.LocalSymbols, "user", "var user string") {
		t.Fatalf("legacy template user binding was lost: %#v", module.LocalSymbols)
	}
}
