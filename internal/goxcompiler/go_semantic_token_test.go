package gox

import "testing"

func TestCollectSemanticTokensClassifiesOriginalGoSyntax(t *testing.T) {
	source := `module main

type Profile struct {
	Name string
}

func (profile *Profile) Greeting(prefix string) string {
	message := prefix + profile.Name
	return message
}
`
	tokens, err := CollectSemanticTokens("components/profile.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]SemanticTokenKind{
		"Profile":  SemanticStruct,
		"Name":     SemanticProperty,
		"profile":  SemanticParameter,
		"Greeting": SemanticMethod,
		"prefix":   SemanticParameter,
		"message":  SemanticVariable,
	}
	for text, kind := range want {
		if !hasSemanticToken(source, tokens, text, kind) {
			t.Errorf("missing %q token of kind %q: %#v", text, kind, tokens)
		}
	}
}
