package lsp

import (
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type DocumentRangeFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Options      FormattingOptions      `json:"options"`
}

func documentRangeFormattingEdits(document Document, selected Range) []CompletionTextEdit {
	start, end, ok := formattingLineOffsets(document, selected)
	if !ok || start == end {
		return []CompletionTextEdit{}
	}
	original := document.Text[start:end]
	formatted, ok := formatSelectedSource(document.Path, original)
	if !ok || formatted == original {
		return []CompletionTextEdit{}
	}
	startPosition, startErr := document.Position(start)
	endPosition, endErr := document.Position(end)
	if startErr != nil || endErr != nil {
		return []CompletionTextEdit{}
	}
	return []CompletionTextEdit{{Range: Range{Start: startPosition, End: endPosition}, NewText: formatted}}
}

func formattingLineOffsets(document Document, selected Range) (int, int, bool) {
	start, err := document.Offset(Position{Line: selected.Start.Line})
	if err != nil {
		return 0, 0, false
	}
	end, err := document.Offset(selected.End)
	if err != nil {
		return 0, 0, false
	}
	if selected.End.Character == 0 && selected.End.Line > selected.Start.Line {
		// LSP selections ending at column zero exclude that final line.
	} else if newline := strings.IndexByte(document.Text[end:], '\n'); newline >= 0 {
		end += newline + 1
	} else {
		end = len(document.Text)
	}
	if end < start {
		return 0, 0, false
	}
	return start, end, true
}

func formatSelectedSource(path, source string) (string, bool) {
	indent, content := selectedCommonIndent(source)
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return source, true
	}
	trailingNewline := strings.HasSuffix(source, "\n")
	if strings.HasPrefix(trimmed, "<") {
		formatted, err := goxcompiler.FormatTemplate(trimmed, indent)
		if err != nil {
			return "", false
		}
		result := indent + formatted
		if trailingNewline {
			result += "\n"
		}
		return result, true
	}

	const moduleLine = "module __gox_range_format__\n"
	if formatted, err := goxcompiler.FormatSource(path, moduleLine+content); err == nil {
		body := strings.TrimPrefix(formatted, "module __gox_range_format__\n\n")
		return reindentSelected(body, indent, trailingNewline), true
	}

	const functionStart = "func __gox_range_format__() {\n"
	wrapped := moduleLine + functionStart + content + "\n}\n"
	formatted, err := goxcompiler.FormatSource(path, wrapped)
	if err != nil {
		return "", false
	}
	start := strings.Index(formatted, functionStart)
	if start < 0 {
		return "", false
	}
	body := formatted[start+len(functionStart):]
	if !strings.HasSuffix(body, "}\n") {
		return "", false
	}
	body = strings.TrimSuffix(body, "}\n")
	body = strings.TrimSuffix(body, "\n")
	body = removeLineIndent(body, "\t")
	return reindentSelected(body, indent, trailingNewline), true
}

func selectedCommonIndent(source string) (string, string) {
	lines := strings.Split(source, "\n")
	indent := ""
	set := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		leading := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if !set || len(leading) < len(indent) {
			indent = leading
			set = true
		}
	}
	return indent, removeLineIndent(source, indent)
}

func removeLineIndent(source, indent string) string {
	if indent == "" {
		return source
	}
	lines := strings.Split(source, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimPrefix(line, indent)
	}
	return strings.Join(lines, "\n")
}

func reindentSelected(source, indent string, trailingNewline bool) string {
	source = strings.TrimSuffix(source, "\n")
	if indent != "" {
		lines := strings.Split(source, "\n")
		for index, line := range lines {
			if line != "" {
				lines[index] = indent + line
			}
		}
		source = strings.Join(lines, "\n")
	}
	if trailingNewline {
		source += "\n"
	}
	return source
}
