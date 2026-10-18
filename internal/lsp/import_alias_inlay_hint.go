package lsp

import (
	"sort"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"
)

type InlayHintParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

type InlayHint struct {
	Position     Position       `json:"position"`
	Label        string         `json:"label"`
	Kind         int            `json:"kind,omitempty"`
	Tooltip      *MarkupContent `json:"tooltip,omitempty"`
	PaddingRight bool           `json:"paddingRight,omitempty"`
}

func (s *Server) importAliasInlayHints(document Document, visible Range) []InlayHint {
	hints := make([]InlayHint, 0)
	for _, declaration := range s.symbols.Imports(document.URI) {
		if declaration.Name != "" {
			continue
		}
		alias, targetURI, ok := s.inferredGoxImportAlias(document, declaration)
		if !ok {
			continue
		}
		tooltip := s.importedModuleMarkup(targetURI, alias, declaration.Path, alias, false)
		if hint, ok := importAliasHint(document, declaration.Source, alias, &tooltip, visible); ok {
			hints = append(hints, hint)
		}
	}
	root := s.projectRoot(document.Path)
	for _, declaration := range s.symbols.GoImports(document.URI) {
		if declaration.Alias != "" {
			continue
		}
		packageInfo, err := s.goPackages.Load(root, declaration.Path)
		if err != nil || packageInfo.Name == "" {
			continue
		}
		tooltip := goPackageImportMarkup(packageInfo, declaration.Path, packageInfo.Name, false)
		if hint, ok := importAliasHint(document, declaration.Source, packageInfo.Name, &tooltip, visible); ok {
			hints = append(hints, hint)
		}
	}
	sort.SliceStable(hints, func(i, j int) bool {
		if hints[i].Position.Line != hints[j].Position.Line {
			return hints[i].Position.Line < hints[j].Position.Line
		}
		return hints[i].Position.Character < hints[j].Position.Character
	})
	return hints
}

func (s *Server) inferredGoxImportAlias(document Document, declaration goxcompiler.GoxImport) (string, string, bool) {
	root := s.projectRoot(document.Path)
	resolved, err := goxcompiler.ResolveGoxImport(root, document.Path, declaration.Path)
	if err != nil {
		return "", "", false
	}
	for _, symbol := range s.symbols.DocumentSymbols(FileURI(resolved)) {
		if symbol.Symbol.Kind == goxcompiler.SymbolModule && symbol.Symbol.Name != "" {
			return symbol.Symbol.Name, FileURI(resolved), true
		}
	}
	return "", "", false
}

func importAliasHint(document Document, source goxmap.SourceRange, alias string, tooltip *MarkupContent, visible Range) (InlayHint, bool) {
	if source.URI == "" || source.Range.Start < 0 {
		return InlayHint{}, false
	}
	position, err := document.Position(source.Range.Start)
	if err != nil || !positionInRange(position, visible) {
		return InlayHint{}, false
	}
	return InlayHint{
		Position: position, Label: alias, PaddingRight: true, Tooltip: tooltip,
	}, true
}

func positionInRange(position Position, visible Range) bool {
	return comparePosition(position, visible.Start) >= 0 && comparePosition(position, visible.End) <= 0
}

func comparePosition(left, right Position) int {
	if left.Line < right.Line || left.Line == right.Line && left.Character < right.Character {
		return -1
	}
	if left == right {
		return 0
	}
	return 1
}
