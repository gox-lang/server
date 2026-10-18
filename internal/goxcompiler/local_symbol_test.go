package gox

import (
	"strings"
	"testing"
)

func TestCollectLocalSymbolsPreservesScopesAndInferredTypes(t *testing.T) {
	source := `module profile

func Build(prefix string) string { return prefix }

func Render(ctx *gox.Context) gox.Page {
	name := "George"
	count := 3
	active := true
	label := Build(name)
	var explicit uint64
	if active {
		name := "Milo"
		_ = name
	}
	_, _, _, _, _ = ctx, count, label, explicit, name
	return <h1>{{name}}</h1>
}`
	module, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"prefix":   "var prefix string",
		"ctx":      "var ctx *gox.Context",
		"count":    "var count int",
		"active":   "var active bool",
		"label":    "var label string",
		"explicit": "var explicit uint64",
	}
	for name, detail := range want {
		if !hasLocalDetail(module.LocalSymbols, name, detail) {
			t.Errorf("missing %s with detail %q: %#v", name, detail, module.LocalSymbols)
		}
	}
	var names []LocalSymbol
	for _, symbol := range module.LocalSymbols {
		if symbol.Name == "name" {
			names = append(names, symbol)
		}
	}
	if len(names) != 2 {
		t.Fatalf("name declarations = %d, want 2", len(names))
	}
	if names[0].Scope.Range == names[1].Scope.Range {
		t.Fatal("shadowed name must retain its narrower lexical scope")
	}
}

func TestCollectTypedSelectorReferencesAndGoLikeReceiverDetail(t *testing.T) {
	source := `module profile
type User struct { Name string }
func (user User) GetName() string { return user.Name }
func (user *User) setName(name string) { user.Name = name }
`
	module, err := ParseModule("profile.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLocalDetail(module.LocalSymbols, "user", "var user User") {
		t.Fatalf("missing value receiver detail: %#v", module.LocalSymbols)
	}
	if !hasLocalDetail(module.LocalSymbols, "user", "var user *User") {
		t.Fatalf("missing pointer receiver detail: %#v", module.LocalSymbols)
	}
	uses := []int{strings.Index(source, "user.Name") + len("user."), strings.LastIndex(source, "user.Name") + len("user.")}
	for _, use := range uses {
		found := false
		for _, reference := range module.SelectorReferences {
			if reference.Name == "Name" && reference.Kind == SymbolField && reference.Container == "User" && reference.Source.Range.Contains(use) {
				found = true
			}
		}
		if !found {
			t.Errorf("missing typed Name selector at %d: %#v", use, module.SelectorReferences)
		}
	}
}

func TestLocalSymbolKeepsQualifiedGOXType(t *testing.T) {
	source := `module card
import users "./users.gox"
func Build() { var user users.User; _ = user }
`
	module, err := ParseModule("card.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range module.LocalSymbols {
		if symbol.Name == "user" {
			if symbol.Type != "users.User" || symbol.Detail != "var user users.User" {
				t.Fatalf("user symbol = %#v", symbol)
			}
			return
		}
	}
	t.Fatalf("user symbol not found: %#v", module.LocalSymbols)
}

func TestLocalSymbolKeepsInitializerForWorkspaceInference(t *testing.T) {
	source := `module card
import users "./users.gox"
func Build() {
	user := users.NewUser("George")
	name := user.GetName()
	_, _ = user, name
}
`
	module, err := ParseModule("card.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"user": `users.NewUser("George")`, "name": "user.GetName()"}
	for name, initializer := range want {
		found := false
		for _, symbol := range module.LocalSymbols {
			if symbol.Name == name && symbol.Initializer == initializer {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %s initializer %q: %#v", name, initializer, module.LocalSymbols)
		}
	}
}

func TestLocalSymbolsMapMultipleResultsAndIgnoreBlankIdentifier(t *testing.T) {
	source := `module page
import users "./users.gox"
func Build() {
	user, err := users.Lookup()
	_, secondErr := users.Lookup()
	thirdUser, _ := users.Lookup()
	var fourthUser, fourthErr = users.Lookup()
	var _, fifthErr = users.Lookup()
	_, _, _, _, _, _, _ = user, err, secondErr, thirdUser, fourthUser, fourthErr, fifthErr
}
`
	module, err := ParseModule("page.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"user": 0, "err": 1, "secondErr": 1, "thirdUser": 0, "fourthUser": 0, "fourthErr": 1, "fifthErr": 1}
	for name, resultIndex := range want {
		found := false
		for _, symbol := range module.LocalSymbols {
			if symbol.Name == name {
				found = true
				if symbol.Initializer != "users.Lookup()" || symbol.InitializerResult != resultIndex {
					t.Errorf("%s symbol = %#v", name, symbol)
				}
			}
		}
		if !found {
			t.Errorf("symbol %s not found", name)
		}
	}
	for _, symbol := range module.LocalSymbols {
		if symbol.Name == "_" {
			t.Fatalf("blank identifier was indexed: %#v", symbol)
		}
	}
}

func hasLocalDetail(symbols []LocalSymbol, name, detail string) bool {
	for _, symbol := range symbols {
		if symbol.Name == name && symbol.Detail == detail {
			return true
		}
	}
	return false
}
