package lsp

import (
	"sort"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"
)

var semanticTokenTypes = []string{
	"namespace", "type", "class", "enum", "interface", "struct", "typeParameter",
	"parameter", "variable", "property", "enumMember", "event", "function", "method",
	"macro", "label", "comment", "string", "number", "regexp", "operator", "decorator", "keyword",
	"goxModuleKeyword", "goxClientKeyword", "goxClientFramework", "goxTag", "goxAttribute",
	"goxAttributeValue", "goxDirective", "goxComponent", "goxClient",
}

var semanticTokenTypeIndexes = func() map[string]uint32 {
	indexes := make(map[string]uint32, len(semanticTokenTypes))
	for index, name := range semanticTokenTypes {
		indexes[name] = uint32(index)
	}
	return indexes
}()

type SemanticTokensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type SemanticTokens struct {
	Data []uint32 `json:"data"`
}

func semanticTokensForDocument(document Document) SemanticTokens {
	compilerTokens, err := goxcompiler.CollectSemanticTokens(document.Path, document.Text)
	if err != nil {
		return SemanticTokens{Data: []uint32{}}
	}
	sort.SliceStable(compilerTokens, func(i, j int) bool {
		if compilerTokens[i].Line != compilerTokens[j].Line {
			return compilerTokens[i].Line < compilerTokens[j].Line
		}
		return compilerTokens[i].Column < compilerTokens[j].Column
	})
	data := make([]uint32, 0, len(compilerTokens)*5)
	previousLine, previousCharacter := 0, 0
	lineIndex := goxmap.NewLineIndex(document.Text)
	for _, token := range compilerTokens {
		startOffset, err := lineIndex.ByteOffset(goxmap.BytePosition{Line: token.Line, Column: token.Column})
		if err != nil {
			continue
		}
		endOffset := startOffset + token.Length
		start, startErr := document.Position(startOffset)
		end, endErr := document.Position(endOffset)
		if startErr != nil || endErr != nil || start.Line != end.Line || end.Character <= start.Character {
			continue
		}
		typeIndex, ok := semanticTokenTypeIndexes[string(token.Kind)]
		if !ok {
			continue
		}
		deltaLine := start.Line - previousLine
		deltaStart := start.Character
		if deltaLine == 0 {
			deltaStart -= previousCharacter
		}
		data = append(data, uint32(deltaLine), uint32(deltaStart), uint32(end.Character-start.Character), typeIndex, 0)
		previousLine, previousCharacter = start.Line, start.Character
	}
	return SemanticTokens{Data: data}
}
