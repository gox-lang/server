package lsp

import (
	"sort"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

type rankedWorkspaceSymbol struct {
	information SymbolInformation
	score       int
	offset      int
}

func (s *Server) workspaceSymbols(query string) []SymbolInformation {
	indexed := s.symbols.Symbols()
	modules := make(map[string]string)
	for _, candidate := range indexed {
		if candidate.Symbol.Kind == goxcompiler.SymbolModule {
			modules[candidate.URI] = candidate.Symbol.Name
		}
	}

	ranked := make([]rankedWorkspaceSymbol, 0)
	for _, candidate := range indexed {
		if !workspaceSearchableSymbol(candidate.Symbol) {
			continue
		}
		container := modules[candidate.URI]
		if candidate.Symbol.Container != "" {
			if container != "" {
				container += "."
			}
			container += candidate.Symbol.Container
		}
		score, matches := workspaceSymbolMatchScore(query, candidate.Symbol.Name, container)
		if !matches {
			continue
		}
		document, ok := s.symbols.Document(candidate.URI)
		if !ok {
			continue
		}
		start, startErr := document.Position(candidate.Symbol.Source.Range.Start)
		end, endErr := document.Position(candidate.Symbol.Source.Range.End)
		if startErr != nil || endErr != nil {
			continue
		}
		ranked = append(ranked, rankedWorkspaceSymbol{
			information: SymbolInformation{
				Name: candidate.Symbol.Name, Kind: workspaceSymbolKind(candidate.Symbol.Kind),
				Location:      Location{URI: candidate.URI, Range: Range{Start: start, End: end}},
				ContainerName: container,
			},
			score: score, offset: candidate.Symbol.Source.Range.Start,
		})
	}
	sort.SliceStable(ranked, func(left, right int) bool {
		if ranked[left].score != ranked[right].score {
			return ranked[left].score < ranked[right].score
		}
		leftName := strings.ToLower(ranked[left].information.Name)
		rightName := strings.ToLower(ranked[right].information.Name)
		if leftName != rightName {
			return leftName < rightName
		}
		if ranked[left].information.ContainerName != ranked[right].information.ContainerName {
			return ranked[left].information.ContainerName < ranked[right].information.ContainerName
		}
		if ranked[left].information.Location.URI != ranked[right].information.Location.URI {
			return ranked[left].information.Location.URI < ranked[right].information.Location.URI
		}
		return ranked[left].offset < ranked[right].offset
	})
	result := make([]SymbolInformation, len(ranked))
	for index := range ranked {
		result[index] = ranked[index].information
	}
	return result
}

func workspaceSearchableSymbol(symbol goxcompiler.SourceSymbol) bool {
	if symbol.Name == "" || symbol.Name == "_" {
		return false
	}
	return symbol.Kind != goxcompiler.SymbolVariable || symbol.Container == ""
}

func workspaceSymbolMatchScore(query, name, container string) (int, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 4, true
	}
	name = strings.ToLower(name)
	qualified := name
	if container != "" {
		qualified = strings.ToLower(container) + "." + name
	}
	candidate := name
	if strings.Contains(query, ".") {
		candidate = qualified
	}
	switch {
	case name == query:
		return 0, true
	case strings.HasPrefix(name, query):
		return 1, true
	case strings.Contains(name, query):
		return 2, true
	case candidate != name && strings.Contains(candidate, query):
		return 3, true
	case workspaceSymbolSubsequence(query, candidate):
		return 4, true
	default:
		return 0, false
	}
}

func workspaceSymbolSubsequence(query, candidate string) bool {
	queryRunes := []rune(query)
	index := 0
	for _, character := range candidate {
		if index < len(queryRunes) && character == queryRunes[index] {
			index++
		}
	}
	return index == len(queryRunes)
}

func workspaceSymbolKind(kind goxcompiler.SourceSymbolKind) int {
	return documentSymbolKind(kind)
}
