package gox

import (
	"strings"
	"testing"
)

func TestParseModuleCollectsOnlyUnqualifiedUnresolvedIdentifiers(t *testing.T) {
	source := `module page
import (
	users "./users.gox"
	"strings"
)
func Card(name string) gox.Component {
	known := name
	_ = known
	_ = missing
	_ = MissingCall()
	_ = users.Lookup()
	_ = strings.TrimSpace(name)
	_ = User{Name: name}
	return <span>{{ unknownTemplate }} {{ known }}</span>
}
`
	module, err := ParseModule("page.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"missing":     strings.Index(source, "missing"),
		"MissingCall": strings.Index(source, "MissingCall"),
		"User":        strings.Index(source, "User{Name"),
	}
	if len(module.UnresolvedIdentifiers) != len(want) {
		t.Fatalf("unresolved identifiers = %#v", module.UnresolvedIdentifiers)
	}
	for _, identifier := range module.UnresolvedIdentifiers {
		offset, ok := want[identifier.Name]
		if !ok || identifier.Source.Range.Start != offset {
			t.Errorf("unexpected unresolved identifier = %#v", identifier)
		}
		delete(want, identifier.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing unresolved identifiers = %#v", want)
	}
}

func TestParseModuleDoesNotReportCompositeKeysLabelsOrComments(t *testing.T) {
	source := `module page
type User struct { Name string }
func Build() {
loop:
	for {
		_ = User{Name: "George"}
		break loop
	}
	// missingInComment
	_ = "missingInString"
}
`
	module, err := ParseModule("page.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if len(module.UnresolvedIdentifiers) != 0 {
		t.Fatalf("unresolved identifiers = %#v", module.UnresolvedIdentifiers)
	}
}
