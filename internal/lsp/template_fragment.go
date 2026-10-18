package lsp

import "strings"

type DocumentOnTypeFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Ch           string                 `json:"ch"`
	Options      FormattingOptions      `json:"options"`
}

type FormattingOptions struct {
	TabSize      int  `json:"tabSize"`
	InsertSpaces bool `json:"insertSpaces"`
}

func templateFragmentClosingEdits(document Document, position Position, ch string) []CompletionTextEdit {
	if ch != ">" {
		return []CompletionTextEdit{}
	}
	offset, err := document.Offset(position)
	if err != nil || offset < 2 || document.Text[offset-2:offset] != "<>" {
		return []CompletionTextEdit{}
	}
	if strings.HasPrefix(document.Text[offset:], "</>") {
		return []CompletionTextEdit{}
	}
	openOffset := offset - 2
	if !insideOpenTemplateMarkup(document.Text, openOffset) && !expectedTemplateValue(document.Text, openOffset) {
		return []CompletionTextEdit{}
	}
	return []CompletionTextEdit{{Range: Range{Start: position, End: position}, NewText: "</>"}}
}
