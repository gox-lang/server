package gox

import (
	"strings"
	"testing"
)

func TestCollectSemanticTokensUsesGOXParsers(t *testing.T) {
	source := `module main

import "github.com/gox-lang/gox"

client Menu react {
	src: "menu.jsx"
}

func Card() gox.Component {
	return <span>George</span>
}

func GET(ctx *gox.Context) gox.Page {
	return <main class="page"><:if condition={{true}}><&Card/><@Menu/></:if></main>
}
`
	tokens, err := CollectSemanticTokens("pages/index.gox", source)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]SemanticTokenKind{
		"module": SemanticGoxModuleKeyword,
		"main":   SemanticNamespace,
		"client": SemanticGoxClientKeyword,
		"Menu":   SemanticGoxClient,
		"react":  SemanticGoxClientFramework,
		"class":  SemanticGoxAttribute,
		`"page"`: SemanticGoxAttributeValue,
		"if":     SemanticGoxDirective,
		"Card":   SemanticGoxComponent,
		"span":   SemanticGoxTag,
	}
	for text, kind := range want {
		if !hasSemanticToken(source, tokens, text, kind) {
			t.Errorf("missing %q token of kind %q: %#v", text, kind, tokens)
		}
	}
}

func hasSemanticToken(source string, tokens []SemanticToken, text string, kind SemanticTokenKind) bool {
	lines := strings.Split(source, "\n")
	for _, token := range tokens {
		if token.Kind != kind || token.Length != len(text) {
			continue
		}
		if token.Line >= 0 && token.Line < len(lines) && token.Column >= 0 && token.Column+token.Length <= len(lines[token.Line]) && lines[token.Line][token.Column:token.Column+token.Length] == text {
			return true
		}
	}
	return false
}
