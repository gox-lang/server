package lsp

import (
	"fmt"
	"strings"
)

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type Hover struct {
	Contents MarkupContent `json:"contents"`
}

func (s *Server) hover(document Document, position Position) *Hover {
	offset, err := document.Offset(position)
	if err == nil {
		if s.contextAt(document, offset).passiveEmbedded() {
			return nil
		}
		if item, ok := s.symbols.ClientSemanticAt(document.URI, offset); ok {
			return hoverForDetail(item.Detail, "")
		}
		if result := s.semanticHoverAt(document, offset); result != nil {
			return result
		}
		if result := s.importedGoPackageHover(document, position, offset); result != nil {
			return result
		}
		if selector, ok := selectorAtOffset(document.Text, offset); ok && selector.cursorOnMember {
			if item, found := s.typedValueMember(document, selector.qualifier, selector.member, offset); found {
				return hoverForDetail(item.Detail, "")
			}
		}
	}
	definitions, err := s.definitionsAt(document, position)
	if err != nil || len(definitions) == 0 {
		if err == nil {
			if name, _, identifierErr := identifierAtPosition(document, position); identifierErr == nil {
				if builtin, ok := goBuiltinSignatureFor(name); ok {
					return hoverForDetail(builtin.Label, "")
				}
			}
		}
		return nil
	}
	if result := s.importedModuleHover(document, position, definitions); result != nil {
		return result
	}
	definition := definitions[0]
	detail := strings.TrimSpace(definition.Symbol.Detail)
	if detail == "" {
		detail = fmt.Sprintf("%s %s", definition.Symbol.Kind, definition.Symbol.Name)
	}
	return hoverForDetail(detail, definition.Symbol.Container)
}

func hoverForDetail(detail, container string) *Hover {
	value := "```gox\n" + strings.TrimSpace(detail) + "\n```"
	if container != "" {
		value += "\n\nDeclared in `" + container + "`."
	}
	return &Hover{Contents: MarkupContent{Kind: "markdown", Value: value}}
}
