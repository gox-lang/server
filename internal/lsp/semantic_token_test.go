package lsp

import "testing"

func TestSemanticTokensEncodeCompilerTokensForLSP(t *testing.T) {
	document := Document{
		Path: "/workspace/pages/index.gox",
		Text: `module main

import "github.com/gox-lang/gox"

func Card() gox.Component {
	return <span>George</span>
}

func Render() gox.Component {
	name := "George"
	_ = name
	return <main class="😀"><&Card/></main>
}
`,
	}
	tokens := semanticTokensForDocument(document)
	if len(tokens.Data) == 0 || len(tokens.Data)%5 != 0 {
		t.Fatalf("semantic token data = %#v", tokens.Data)
	}
	functionIndex := semanticTokenTypeIndexes["function"]
	variableIndex := semanticTokenTypeIndexes["variable"]
	tagIndex := semanticTokenTypeIndexes["goxTag"]
	componentIndex := semanticTokenTypeIndexes["goxComponent"]
	foundFunction := false
	foundVariable := false
	foundTag := false
	foundComponent := false
	for index := 0; index < len(tokens.Data); index += 5 {
		if tokens.Data[index+3] == functionIndex {
			foundFunction = true
		}
		if tokens.Data[index+3] == variableIndex {
			foundVariable = true
		}
		if tokens.Data[index+3] == tagIndex {
			foundTag = true
		}
		if tokens.Data[index+3] == componentIndex {
			foundComponent = true
		}
	}
	if !foundFunction {
		t.Fatalf("component function token not found: %#v", tokens.Data)
	}
	if !foundVariable {
		t.Fatalf("Go variable token not found: %#v", tokens.Data)
	}
	if !foundTag || !foundComponent {
		t.Fatalf("GOX template token types not found: %#v", tokens.Data)
	}
}
