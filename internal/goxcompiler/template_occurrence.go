package gox

import (
	"fmt"

	"github.com/gox-lang/server/internal/goxmap"
)

type TemplateOccurrenceKind string

const (
	TemplateOccurrenceReturn TemplateOccurrenceKind = "return"
	TemplateOccurrenceValue  TemplateOccurrenceKind = "value"
)

// TemplateOccurrence describes one template embedded in GOX source. Source
// covers the complete user-facing template syntax. Content covers the exact
// bytes parsed by Template; fragments therefore exclude their <> and </>
// delimiters from Content while retaining them in Source.
type TemplateOccurrence struct {
	Kind       TemplateOccurrenceKind
	ModuleName string
	FuncName   string
	Source     goxmap.SourceRange
	Content    goxmap.SourceRange
	Raw        string
	Template   *Template
	StaticName string
}

func exactTemplateSourceRange(path string, upstream *goxmap.Map, start, end int) (goxmap.SourceRange, error) {
	rng := goxmap.Range{Start: start, End: end}
	if upstream == nil {
		return goxmap.SourceRange{URI: path, Range: rng}, nil
	}
	mapped, kind, ok := upstream.OriginalRange(rng)
	if !ok || kind != goxmap.Exact {
		return goxmap.SourceRange{}, fmt.Errorf("template source range %v in %q does not map exactly to GOX source", rng, path)
	}
	return mapped, nil
}
