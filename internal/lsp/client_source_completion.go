package lsp

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gox-lang/server/internal/clientframework"
)

type clientSourceCompletionContext struct {
	framework string
	prefix    string
	start     int
	end       int
}

func (s *Server) clientSourcePathCompletions(document Document, offset int, cursor cursorContext) ([]CompletionItem, bool) {
	if cursor.Kind != cursorContextClientSource || !cursor.HasClient {
		return nil, false
	}
	context := cursor.ClientSource
	root := filepath.Join(s.projectRoot(document.Path), "client", context.framework)
	start, startErr := document.Position(context.start)
	end, endErr := document.Position(context.end)
	if startErr != nil || endErr != nil {
		return []CompletionItem{}, true
	}
	var items []CompletionItem
	_ = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if filePath != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !clientframework.SupportsExtension(context.framework, strings.ToLower(filepath.Ext(entry.Name()))) {
			return nil
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if context.prefix != "" && !strings.HasPrefix(strings.ToLower(relative), strings.ToLower(context.prefix)) {
			return nil
		}
		items = append(items, CompletionItem{
			Label: relative, Kind: 17, Detail: "Client source in " + context.framework,
			FilterText: relative, SortText: "0_" + relative,
			TextEdit: &CompletionTextEdit{Range: Range{Start: start, End: end}, NewText: relative},
		})
		return nil
	})
	sort.SliceStable(items, func(left, right int) bool { return items[left].Label < items[right].Label })
	return items, true
}

func rawClientSourceCompletionContextAt(source string, offset int) (clientSourceCompletionContext, bool) {
	if offset < 0 || offset > len(source) {
		return clientSourceCompletionContext{}, false
	}
	clientStart := strings.LastIndex(source[:offset], "client ")
	if clientStart < 0 {
		return clientSourceCompletionContext{}, false
	}
	braceRelative := strings.IndexByte(source[clientStart:offset], '{')
	if braceRelative < 0 {
		return clientSourceCompletionContext{}, false
	}
	brace := clientStart + braceRelative
	if strings.LastIndexByte(source[brace:offset], '}') >= 0 {
		return clientSourceCompletionContext{}, false
	}
	header := strings.Fields(source[clientStart:brace])
	if len(header) != 3 || header[0] != "client" {
		return clientSourceCompletionContext{}, false
	}
	framework := strings.ToLower(header[2])
	definition, exists := clientframework.Lookup(framework)
	if !exists || definition.Status != clientframework.Supported {
		return clientSourceCompletionContext{}, false
	}
	lineStart := brace + 1
	if relative := strings.LastIndexByte(source[lineStart:offset], '\n'); relative >= 0 {
		lineStart += relative + 1
	}
	line := source[lineStart:offset]
	colon := strings.IndexByte(line, ':')
	if colon < 0 || strings.TrimSpace(line[:colon]) != "src" {
		return clientSourceCompletionContext{}, false
	}
	quoteRelative := strings.IndexByte(line[colon+1:], '"')
	if quoteRelative < 0 {
		return clientSourceCompletionContext{}, false
	}
	start := lineStart + colon + 1 + quoteRelative + 1
	if offset < start {
		return clientSourceCompletionContext{}, false
	}
	end := offset
	if closing := strings.IndexByte(source[offset:], '"'); closing >= 0 {
		end = offset + closing
	}
	return clientSourceCompletionContext{framework: framework, prefix: source[start:offset], start: start, end: end}, true
}
