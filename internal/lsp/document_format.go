package lsp

import (
	"errors"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

var errNoCompleteFormattingPrefix = errors.New("no complete GOX prefix to format")

type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Options      FormattingOptions      `json:"options"`
}

func documentFormattingEdits(document Document) []CompletionTextEdit {
	formatted, err := goxcompiler.FormatSource(document.Path, document.Text)
	if err != nil {
		formatted, err = formatCompleteDocumentPrefix(document)
	}
	if err != nil || formatted == document.Text {
		return []CompletionTextEdit{}
	}
	end, err := document.Position(len(document.Text))
	if err != nil {
		return []CompletionTextEdit{}
	}
	return []CompletionTextEdit{{Range: Range{Start: Position{}, End: end}, NewText: formatted}}
}

// formatCompleteDocumentPrefix formats the largest line-aligned prefix that
// forms a valid GOX document and preserves the incomplete suffix byte-for-byte.
func formatCompleteDocumentPrefix(document Document) (string, error) {
	for end := strings.LastIndexByte(document.Text, '\n'); end > 0; {
		prefix := document.Text[:end+1]
		formatted, err := goxcompiler.FormatSource(document.Path, prefix)
		if err == nil {
			return formatted + document.Text[end+1:], nil
		}
		next := strings.LastIndexByte(document.Text[:end], '\n')
		if next < 0 {
			break
		}
		end = next
	}
	return "", errNoCompleteFormattingPrefix
}
