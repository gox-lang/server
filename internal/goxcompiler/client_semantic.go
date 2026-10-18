package gox

import (
	"fmt"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

func collectClientSemantics(source string, module *Module) []ClientSemantic {
	if module == nil {
		return nil
	}
	var items []ClientSemantic
	for _, client := range module.Clients {
		framework := strings.ToLower(string(client.Framework))
		items = append(items, clientSemanticAt(module.Path, client.FrameworkOffset, len(framework), "framework "+framework))
		items = append(items, clientSemanticAt(module.Path, client.SrcKeyOffset, len("src"), "property src string"))
		if length := clientLiteralLength(source, client.EntryOffset); length > 0 {
			items = append(items, clientSemanticAt(module.Path, client.EntryOffset, length, fmt.Sprintf("string %q", client.Entry)))
		}
		if client.DevDeclared {
			items = append(items, clientSemanticAt(module.Path, client.DevKeyOffset, len("dev"), "property dev bool"))
			if length := clientLiteralLength(source, client.DevValueOffset); length > 0 {
				items = append(items, clientSemanticAt(module.Path, client.DevValueOffset, length, fmt.Sprintf("bool %t", client.Dev)))
			}
		}
	}
	return items
}

func clientSemanticAt(path string, offset, length int, detail string) ClientSemantic {
	return ClientSemantic{Source: goxmap.SourceRange{URI: path, Range: goxmap.Range{Start: offset, End: offset + length}}, Detail: detail}
}

func clientLiteralLength(source string, offset int) int {
	if offset < 0 || offset >= len(source) {
		return 0
	}
	if source[offset] == '"' {
		for end := offset + 1; end < len(source); end++ {
			if source[end] == '\\' {
				end++
				continue
			}
			if source[end] == '"' {
				return end - offset + 1
			}
		}
		return 0
	}
	for end := offset; end < len(source); end++ {
		if !isIdentPart(source[end]) {
			return end - offset
		}
	}
	return len(source) - offset
}
