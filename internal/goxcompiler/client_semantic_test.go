package gox

import (
	"strings"
	"testing"
)

func TestClientSemanticsUseOriginalRangesAndEffectiveConfiguration(t *testing.T) {
	source := `module main

client Menu react {
	src: "menu/menu.jsx"
	dev: true
}
`
	module, err := ParseModule("page.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"react":           "framework react",
		"src":             "property src string",
		`"menu/menu.jsx"`: `string "menu/menu.jsx"`,
		"dev":             "property dev bool",
		"true":            "bool true",
	}
	for token, detail := range want {
		offset := strings.Index(source, token)
		found := false
		for _, item := range module.ClientSemantics {
			if item.Source.Range.Start == offset {
				found = true
				if item.Detail != detail {
					t.Errorf("%s detail = %q, want %q", token, item.Detail, detail)
				}
			}
		}
		if !found {
			t.Errorf("semantic item for %s not found: %#v", token, module.ClientSemantics)
		}
	}
}

func TestClientDetailIncludesDefaultDevValue(t *testing.T) {
	source := "module main\nclient Menu react {\n\tsrc: \"menu.jsx\"\n}\n"
	module, err := ParseModule("page.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range module.Symbols {
		if symbol.Name == "Menu" {
			want := "client Menu react {\n\tsrc: \"menu.jsx\"\n\tdev: false\n}"
			if symbol.Detail != want {
				t.Fatalf("Menu detail = %q, want %q", symbol.Detail, want)
			}
			return
		}
	}
	t.Fatal("Menu symbol not found")
}
