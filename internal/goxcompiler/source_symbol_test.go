package gox

import "testing"

func TestCollectSourceSymbolsUsesOriginalGOXDeclarations(t *testing.T) {
	source := `module profile

client Menu react {
	src: "menu.jsx"
}

type Profile struct {
	Name string
}

const DefaultName = "George"

func (profile *Profile) Greeting() string {
	message := profile.Name
	return message
}

func Render(name string, count int, children gox.Component) gox.Component {
	LocalCard := func() gox.Component { return <span>George</span> }
	return <&LocalCard/>
}
`
	symbols, err := CollectSourceSymbols("components/profile.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]SourceSymbolKind{
		"profile":     SymbolModule,
		"Menu":        SymbolClient,
		"Profile":     SymbolStruct,
		"Name":        SymbolField,
		"DefaultName": SymbolConstant,
		"Greeting":    SymbolMethod,
		"LocalCard":   SymbolVariable,
	}
	for name, kind := range want {
		if !hasSourceSymbol(symbols, name, kind) {
			t.Errorf("missing %q symbol of kind %q: %#v", name, kind, symbols)
		}
	}
	if hasSourceSymbol(symbols, "message", SymbolVariable) {
		t.Fatalf("local variable leaked into source index: %#v", symbols)
	}
	for _, symbol := range symbols {
		if symbol.Name == "LocalCard" && symbol.Container != "Render" {
			t.Fatalf("local component container = %q", symbol.Container)
		}
	}
	assertSourceSymbolDetail(t, symbols, "Greeting", "func (profile *Profile) Greeting() string")
	assertSourceSymbolDetail(t, symbols, "Name", "field Name string")
	assertSourceSymbolDetail(t, symbols, "DefaultName", `const DefaultName = "George"`)
	assertSourceSymbolDetail(t, symbols, "LocalCard", "LocalCard := func() gox.Component")
	assertSourceSymbolDetail(t, symbols, "Menu", "client Menu react {\n\tsrc: \"menu.jsx\"\n\tdev: false\n}")
	assertSourceSymbolType(t, symbols, "Profile", "Profile")
	assertSourceSymbolType(t, symbols, "Name", "string")
	assertSourceSymbolType(t, symbols, "DefaultName", "string")
	assertSourceSymbolType(t, symbols, "Greeting", "string")
	for _, symbol := range symbols {
		if symbol.Name != "Render" {
			continue
		}
		wantParameters := []SourceParameter{{Name: "name", Type: "string"}, {Name: "count", Type: "int"}, {Name: "children", Type: "gox.Component"}}
		if len(symbol.Parameters) != len(wantParameters) {
			t.Fatalf("Render parameters = %#v", symbol.Parameters)
		}
		for index := range wantParameters {
			if symbol.Parameters[index] != wantParameters[index] {
				t.Fatalf("Render parameters = %#v", symbol.Parameters)
			}
		}
		return
	}
	t.Fatal("Render symbol not found")
}

func assertSourceSymbolType(t *testing.T, symbols []SourceSymbol, name, want string) {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Name == name {
			if symbol.Type != want {
				t.Fatalf("%s type = %q, want %q", name, symbol.Type, want)
			}
			return
		}
	}
	t.Fatalf("symbol %q not found", name)
}

func TestSourceSymbolsKeepOrderedMultipleResults(t *testing.T) {
	source := `module users
type User struct{}
func Lookup() (User, error) { return User{}, nil }
func (user User) Split() (name string, age int) { return "George", 30 }
`
	symbols, err := CollectSourceSymbols("users.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"Lookup": []string{"User", "error"}, "Split": []string{"string", "int"}}
	for name, results := range want {
		found := false
		for _, symbol := range symbols {
			if symbol.Name == name {
				found = true
				if len(symbol.Results) != len(results) {
					t.Fatalf("%s results = %#v", name, symbol.Results)
				}
				for index := range results {
					if symbol.Results[index] != results[index] {
						t.Fatalf("%s results = %#v", name, symbol.Results)
					}
				}
			}
		}
		if !found {
			t.Fatalf("symbol %s not found", name)
		}
	}
}

func TestSourceSymbolsKeepInterfaceMethodParameters(t *testing.T) {
	symbols, err := CollectSourceSymbols("reader.gox", `module reader
type Reader interface {
	Read(buffer []byte, limit ...int) (int, error)
}
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range symbols {
		if symbol.Name != "Read" {
			continue
		}
		want := []SourceParameter{{Name: "buffer", Type: "[]byte"}, {Name: "limit", Type: "...int"}}
		if len(symbol.Parameters) != len(want) || symbol.Parameters[0] != want[0] || symbol.Parameters[1] != want[1] {
			t.Fatalf("Read parameters = %#v, want %#v", symbol.Parameters, want)
		}
		return
	}
	t.Fatal("Read method not found")
}

func TestSourceSymbolsKeepDefinedTypesAliasesAndConstantValues(t *testing.T) {
	source := `module status
type Status string
type Label = string
const StatusReady Status = "ready"
const DefaultLabel Label = "George"
`
	symbols, err := CollectSourceSymbols("status.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		underlying string
		alias      bool
		value      string
	}{
		"Status":       {underlying: "string"},
		"Label":        {underlying: "string", alias: true},
		"StatusReady":  {value: `"ready"`},
		"DefaultLabel": {value: `"George"`},
	}
	for name, expected := range want {
		found := false
		for _, symbol := range symbols {
			if symbol.Name != name {
				continue
			}
			found = true
			if symbol.Underlying != expected.underlying || symbol.Alias != expected.alias || symbol.Value != expected.value {
				t.Fatalf("%s metadata = %#v", name, symbol)
			}
		}
		if !found {
			t.Fatalf("symbol %s not found", name)
		}
	}
}

func assertSourceSymbolDetail(t *testing.T, symbols []SourceSymbol, name, want string) {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Name == name {
			if symbol.Detail != want {
				t.Fatalf("%s detail = %q, want %q", name, symbol.Detail, want)
			}
			return
		}
	}
	t.Fatalf("symbol %q not found", name)
}

func hasSourceSymbol(symbols []SourceSymbol, name string, kind SourceSymbolKind) bool {
	for _, symbol := range symbols {
		if symbol.Name == name && symbol.Kind == kind && symbol.Source.Range.End-symbol.Source.Range.Start == len(name) {
			return true
		}
	}
	return false
}
