package lsp

import (
	"sort"
	"unicode"
	"unicode/utf8"
)

type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type referenceTarget struct {
	URI   string
	Range Range
}

type identifierOccurrence struct {
	Name  string
	Start int
	End   int
}

func (s *Server) references(document Document, position Position, includeDeclaration bool) []Location {
	if offset, err := document.Offset(position); err == nil && s.contextAt(document, offset).passiveEmbedded() {
		return []Location{}
	}
	if locations, handled := s.semanticReferences(document, position, includeDeclaration); handled {
		return locations
	}
	name, _, err := identifierAtPosition(document, position)
	if err != nil || name == "" {
		return []Location{}
	}
	targets := s.referenceTargetsAt(document, position)
	if len(targets) == 0 {
		return []Location{}
	}
	targetSet := make(map[referenceTarget]bool, len(targets))
	for _, target := range targets {
		targetSet[target] = true
	}

	var locations []Location
	seen := make(map[Location]bool)
	for _, candidateDocument := range s.symbols.Documents() {
		for _, occurrence := range scanIdentifierOccurrences(candidateDocument.Text, name) {
			if !s.referenceOccurrenceAllowed(candidateDocument, occurrence.Start) {
				continue
			}
			candidatePosition, err := candidateDocument.Position(occurrence.Start)
			if err != nil || !referenceTargetsIntersect(targetSet, s.referenceTargetsAt(candidateDocument, candidatePosition)) {
				continue
			}
			start, startErr := candidateDocument.Position(occurrence.Start)
			end, endErr := candidateDocument.Position(occurrence.End)
			if startErr != nil || endErr != nil {
				continue
			}
			location := Location{URI: candidateDocument.URI, Range: Range{Start: start, End: end}}
			if !includeDeclaration && targetSet[referenceTarget{URI: location.URI, Range: location.Range}] {
				continue
			}
			if !seen[location] {
				seen[location] = true
				locations = append(locations, location)
			}
		}
	}
	sort.SliceStable(locations, func(left, right int) bool {
		if locations[left].URI != locations[right].URI {
			return locations[left].URI < locations[right].URI
		}
		if locations[left].Range.Start.Line != locations[right].Range.Start.Line {
			return locations[left].Range.Start.Line < locations[right].Range.Start.Line
		}
		return locations[left].Range.Start.Character < locations[right].Range.Start.Character
	})
	return locations
}

func (s *Server) referenceTargetsAt(document Document, position Position) []referenceTarget {
	if links := s.importDefinitionLinks(document, position); len(links) > 0 {
		targets := make([]referenceTarget, 0, len(links))
		for _, link := range links {
			targets = append(targets, referenceTarget{URI: link.TargetURI, Range: link.TargetSelectionRange})
		}
		return targets
	}
	if locations := s.importedGoDefinitionLocations(document, position); len(locations) > 0 {
		return referenceTargetsFromLocations(locations)
	}
	definitions, err := s.definitionsAt(document, position)
	if err != nil || len(definitions) == 0 {
		return nil
	}
	return referenceTargetsFromLocations(s.locationsForDefinitions(definitions))
}

func referenceTargetsFromLocations(locations []Location) []referenceTarget {
	targets := make([]referenceTarget, 0, len(locations))
	for _, location := range locations {
		targets = append(targets, referenceTarget{URI: location.URI, Range: location.Range})
	}
	return targets
}

func referenceTargetsIntersect(targets map[referenceTarget]bool, candidates []referenceTarget) bool {
	for _, candidate := range candidates {
		if targets[candidate] {
			return true
		}
	}
	return false
}

func (s *Server) referenceOccurrenceAllowed(document Document, offset int) bool {
	for _, templateRange := range s.symbols.TemplateRanges(document.URI) {
		if !templateRange.Contains(offset) && offset != templateRange.End {
			continue
		}
		if _, ok := s.symbols.ReferenceAt(document.URI, offset); ok {
			return true
		}
		if _, ok := s.symbols.LocalDeclarationAt(document.URI, offset); ok {
			return true
		}
		return s.contextAt(document, offset).Kind == cursorContextTemplateExpression || templateSymbolTagAt(document.Text, offset)
	}
	return true
}

func templateSymbolTagAt(source string, offset int) bool {
	if offset < 0 || offset > len(source) {
		return false
	}
	start := offset
	for start > 0 {
		value, size := utf8.DecodeLastRuneInString(source[:start])
		if value != '_' && value != '.' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
			break
		}
		start -= size
	}
	if start == 0 || source[start-1] != '&' && source[start-1] != '@' {
		return false
	}
	start--
	if start == 0 {
		return false
	}
	if source[start-1] == '<' {
		return true
	}
	return start >= 2 && source[start-2:start] == "</"
}

func scanIdentifierOccurrences(source, wanted string) []identifierOccurrence {
	var occurrences []identifierOccurrence
	state := byte(0)
	escaped := false
	for offset := 0; offset < len(source); {
		value, size := utf8.DecodeRuneInString(source[offset:])
		switch state {
		case 1:
			if value == '\n' {
				state = 0
			}
			offset += size
			continue
		case 2:
			if value == '*' && offset+size < len(source) && source[offset+size] == '/' {
				state = 0
				offset += size + 1
			} else {
				offset += size
			}
			continue
		case 3, 4:
			if escaped {
				escaped = false
			} else if value == '\\' {
				escaped = true
			} else if state == 3 && value == '"' || state == 4 && value == '\'' {
				state = 0
			}
			offset += size
			continue
		case 5:
			if value == '`' {
				state = 0
			}
			offset += size
			continue
		}
		if value == '/' && offset+size < len(source) {
			next := source[offset+size]
			if next == '/' {
				state = 1
				offset += size + 1
				continue
			}
			if next == '*' {
				state = 2
				offset += size + 1
				continue
			}
		}
		switch value {
		case '"':
			state = 3
			offset += size
			continue
		case '\'':
			state = 4
			offset += size
			continue
		case '`':
			state = 5
			offset += size
			continue
		}
		if value != '_' && !unicode.IsLetter(value) {
			offset += size
			continue
		}
		start := offset
		offset += size
		for offset < len(source) {
			part, partSize := utf8.DecodeRuneInString(source[offset:])
			if part != '_' && !unicode.IsLetter(part) && !unicode.IsDigit(part) {
				break
			}
			offset += partSize
		}
		if source[start:offset] == wanted {
			occurrences = append(occurrences, identifierOccurrence{Name: wanted, Start: start, End: offset})
		}
	}
	return occurrences
}
