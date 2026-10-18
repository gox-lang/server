package goxmap

import (
	"encoding/json"
	"fmt"
	"sort"
)

type JSONPosition struct {
	Offset int `json:"offset"`
	Line   int `json:"line"`
	Column int `json:"column"`
}

type JSONRange struct {
	Start JSONPosition `json:"start"`
	End   JSONPosition `json:"end"`
}

type JSONMapping struct {
	Generated JSONRange `json:"generated"`
	Source    string    `json:"source"`
	Original  JSONRange `json:"original"`
	Kind      string    `json:"kind"`
}

type JSONDocument struct {
	Version      int           `json:"version"`
	GeneratedURI string        `json:"generatedUri"`
	Sources      []string      `json:"sources"`
	Mappings     []JSONMapping `json:"mappings"`
}

// MarshalJSONDocument serializes a source map as readable line, column and
// byte ranges. Lines and columns are zero based, matching LSP conventions.
func MarshalJSONDocument(sourceMap *Map, generated string, sources map[string]string) ([]byte, error) {
	if sourceMap == nil {
		return nil, fmt.Errorf("source map cannot be nil")
	}
	generatedIndex := NewLineIndex(generated)
	sourceIndexes := make(map[string]*LineIndex, len(sources))
	names := make([]string, 0, len(sources))
	for uri, source := range sources {
		names = append(names, uri)
		sourceIndexes[uri] = NewLineIndex(source)
	}
	sort.Strings(names)

	document := JSONDocument{Version: 1, GeneratedURI: sourceMap.GeneratedURI(), Sources: names}
	document.Mappings = make([]JSONMapping, 0, len(sourceMap.mappings))
	for _, mapping := range sourceMap.mappings {
		originalIndex, ok := sourceIndexes[mapping.Original.URI]
		if !ok {
			return nil, fmt.Errorf("source %q is missing from source map documents", mapping.Original.URI)
		}
		generatedRange, err := jsonRange(generatedIndex, mapping.Generated)
		if err != nil {
			return nil, err
		}
		originalRange, err := jsonRange(originalIndex, mapping.Original.Range)
		if err != nil {
			return nil, err
		}
		kind := "anchor"
		if mapping.Kind == Exact {
			kind = "exact"
		}
		document.Mappings = append(document.Mappings, JSONMapping{
			Generated: generatedRange,
			Source:    mapping.Original.URI,
			Original:  originalRange,
			Kind:      kind,
		})
	}
	return json.MarshalIndent(document, "", "  ")
}

// UnmarshalJSONDocument restores the immutable offset map used by the
// compiler and language tooling from its persisted, readable representation.
func UnmarshalJSONDocument(data []byte) (*Map, error) {
	var document JSONDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if document.Version != 1 {
		return nil, fmt.Errorf("unsupported source map version %d", document.Version)
	}
	mappings := make([]Mapping, 0, len(document.Mappings))
	for index, item := range document.Mappings {
		kind := Exact
		switch item.Kind {
		case "exact":
		case "anchor":
			kind = Anchor
		default:
			return nil, fmt.Errorf("mapping %d has unknown kind %q", index, item.Kind)
		}
		mappings = append(mappings, Mapping{
			Generated: Range{Start: item.Generated.Start.Offset, End: item.Generated.End.Offset},
			Original: SourceRange{
				URI:   item.Source,
				Range: Range{Start: item.Original.Start.Offset, End: item.Original.End.Offset},
			},
			Kind: kind,
		})
	}
	return New(document.GeneratedURI, mappings)
}

func jsonRange(index *LineIndex, value Range) (JSONRange, error) {
	start, err := index.BytePosition(value.Start)
	if err != nil {
		return JSONRange{}, err
	}
	end, err := index.BytePosition(value.End)
	if err != nil {
		return JSONRange{}, err
	}
	return JSONRange{
		Start: JSONPosition{Offset: value.Start, Line: start.Line, Column: start.Column},
		End:   JSONPosition{Offset: value.End, Line: end.Line, Column: end.Column},
	}, nil
}
