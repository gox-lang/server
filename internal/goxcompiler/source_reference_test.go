package gox

import "testing"

func TestParseModuleExposesTemplateSourceReferences(t *testing.T) {
	source := `module page

import Layout "../layout.gox"

client Menu react {
	src: "menu.jsx"
}

func Card() gox.Component {
	return <section>George</section>
}

func Render() gox.Component {
	return <main><&Card/><&Layout.Header/><@Menu/></main>
}
`
	module, err := ParseModule("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name      string
		qualifier string
		kind      SourceReferenceKind
	}{
		{name: "Card", kind: ReferenceComponent},
		{name: "Header", qualifier: "Layout", kind: ReferenceComponent},
		{name: "Menu", kind: ReferenceClient},
	}
	for _, expected := range want {
		found := false
		for _, reference := range module.References {
			if reference.Name == expected.name && reference.Qualifier == expected.qualifier && reference.Kind == expected.kind && reference.Container == "Render" && reference.Source.Range.End-reference.Source.Range.Start == len(expected.name) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing reference %#v: %#v", expected, module.References)
		}
	}
}
