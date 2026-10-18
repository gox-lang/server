package gox

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"

	"github.com/gox-lang/server/internal/goxmap"
)

// SemanticProjection is a Go-only representation of GOX source intended for
// tooling. It deliberately stays separate from the runtime/compiler AST: GOX
// constructs are lowered to generated Go scaffolding while user-authored Go
// expressions remain exact source bytes with exact origins. Source is the
// mapped Go text parsed into File; marker-return lowering is intentionally an
// AST-only semantic step so exact user positions never move after parsing.
type SemanticProjection struct {
	Source     string
	SourceMap  *goxmap.Map
	FileSet    *token.FileSet
	File       *ast.File
	Origins    *OriginTable
	GoxReturns []SemanticGoxReturn
}

// BuildSemanticProjection builds the source-level Go projection used by
// semantic tooling. Template discovery is intentionally delegated to the same
// extraction/transformation pipeline used by ParseModule; the projection
// consumes the resulting TemplateOccurrences instead of defining a second set
// of rules for where templates may appear. ParseModule uses the lower-level
// buildSemanticProjectionFromOccurrences helper so it can reuse occurrences
// already discovered by its normal pipeline without repeating that work.
func BuildSemanticProjection(path, source string) (*SemanticProjection, error) {
	// BuildSemanticProjection is also used by the semantic architecture fixtures,
	// which historically model GOX marker returns with local bare type names.
	// Keep that compatibility isolated to this public projection entry point;
	// ParseModule and imported workspace projections remain identity-strict.
	return buildSemanticProjectionForSourceOptions(path, source, false, true)
}

func buildSemanticProjectionForSource(path, source string) (*SemanticProjection, error) {
	return buildSemanticProjectionForSourceMode(path, source, false)
}

func buildImportedSemanticProjectionForSource(path, source string) (*SemanticProjection, error) {
	return buildSemanticProjectionForSourceMode(path, source, true)
}

func buildSemanticProjectionForSourceMode(path, source string, imported bool) (*SemanticProjection, error) {
	return buildSemanticProjectionForSourceOptions(path, source, imported, false)
}

func buildSemanticProjectionForSourceOptions(path, source string, imported, allowBareMarkers bool) (*SemanticProjection, error) {
	declaredModuleName, source, sourceMap, err := extractModuleDeclMapped(path, source)
	if err != nil {
		return nil, err
	}
	moduleName := normalizeModuleName(declaredModuleName)

	_, source, sourceMap, err = extractClientDeclsMapped(path, source, sourceMap)
	if err != nil {
		return nil, err
	}

	extracted, err := ExtractTemplateReturnsMapped(moduleName, path, source, sourceMap)
	if err != nil {
		return nil, err
	}
	transformed, err := TransformTemplateExpressionsMapped(moduleName, path, extracted.Source, extracted.SourceMap)
	if err != nil {
		return nil, err
	}

	occurrences := make([]TemplateOccurrence, 0, len(extracted.Templates)+len(transformed.Occurrences))
	for _, template := range extracted.Templates {
		occurrences = append(occurrences, template.Occurrence())
	}
	occurrences = append(occurrences, transformed.Occurrences...)
	packageName := "main"
	if imported {
		packageName = declaredModuleName
	}
	projection, err := buildSemanticProjectionFromOccurrencesWithPackageMode(path, source, sourceMap, occurrences, packageName, allowBareMarkers)
	if err != nil {
		return nil, err
	}
	return projection, nil
}

func buildSemanticProjectionFromOccurrences(path, source string, upstream *goxmap.Map, occurrences []TemplateOccurrence) (*SemanticProjection, error) {
	return buildSemanticProjectionFromOccurrencesWithPackage(path, source, upstream, occurrences, "main")
}

func buildSemanticProjectionFromOccurrencesWithPackage(path, source string, upstream *goxmap.Map, occurrences []TemplateOccurrence, packageName string) (*SemanticProjection, error) {
	return buildSemanticProjectionFromOccurrencesWithPackageMode(path, source, upstream, occurrences, packageName, false)
}

func buildSemanticProjectionFromOccurrencesWithPackageMode(path, source string, upstream *goxmap.Map, occurrences []TemplateOccurrence, packageName string, allowBareMarkers bool) (*SemanticProjection, error) {
	if !isValidIdent(packageName) {
		return nil, fmt.Errorf("invalid semantic package name %q", packageName)
	}
	sourceURI := path
	if upstream != nil {
		sourceURI = upstream.GeneratedURI()
	}
	generatedURI := path + "#semantic-projection"
	writer, err := goxmap.NewWriter(generatedURI, sourceURI, source)
	if err != nil {
		return nil, err
	}
	if err := writer.WriteGenerated("package "+packageName+"\n", semanticProjectionPointAnchor(source, 0)); err != nil {
		return nil, err
	}

	type projectedOccurrence struct {
		occurrence TemplateOccurrence
		input      goxmap.Range
	}
	projected := make([]projectedOccurrence, 0, len(occurrences))
	for _, occurrence := range occurrences {
		inputRange, err := semanticProjectionInputRange(upstream, sourceURI, occurrence.Source)
		if err != nil {
			return nil, err
		}
		projected = append(projected, projectedOccurrence{occurrence: occurrence, input: inputRange})
	}
	sort.SliceStable(projected, func(i, j int) bool {
		return projected[i].input.Start < projected[j].input.Start
	})

	last := 0
	for _, current := range projected {
		if current.input.Start < last {
			return nil, fmt.Errorf("semantic projection template ranges overlap: previous end %d, next range %v", last, current.input)
		}
		if err := writer.WriteSource(goxmap.Range{Start: last, End: current.input.Start}); err != nil {
			return nil, err
		}
		if err := writeSemanticTemplateValue(writer, current.occurrence.Template, current.input, upstream, sourceURI, source); err != nil {
			return nil, err
		}
		last = current.input.End
	}
	if err := writer.WriteSource(goxmap.Range{Start: last, End: len(source)}); err != nil {
		return nil, err
	}

	projectionSource, projectionMap, err := writer.Build()
	if err != nil {
		return nil, err
	}
	if upstream != nil {
		projectionMap, err = goxmap.Compose(generatedURI, projectionMap, upstream)
		if err != nil {
			return nil, err
		}
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, projectionSource, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	origins := SeedOriginTable(file, fset, projectionMap)
	var goxReturns []SemanticGoxReturn
	if allowBareMarkers {
		goxReturns = lowerSemanticGoxFunctionsForProjectionFixtures(file, origins)
	} else {
		goxReturns = lowerSemanticGoxFunctions(file, origins)
	}

	return &SemanticProjection{
		Source:     projectionSource,
		SourceMap:  projectionMap,
		FileSet:    fset,
		File:       file,
		Origins:    origins,
		GoxReturns: goxReturns,
	}, nil
}

func writeSemanticTemplateValue(writer *goxmap.Writer, template *Template, templateRange goxmap.Range, upstream *goxmap.Map, sourceURI, source string) error {
	projector := semanticTemplateProjector{
		writer:        writer,
		upstream:      upstream,
		sourceURI:     sourceURI,
		source:        source,
		sourceLength:  len(source),
		templateRange: templateRange,
	}
	return projector.writeTemplateValue(template)
}

func semanticProjectionInputRange(upstream *goxmap.Map, sourceURI string, original goxmap.SourceRange) (goxmap.Range, error) {
	if upstream == nil {
		if original.URI != sourceURI {
			return goxmap.Range{}, fmt.Errorf("semantic projection source URI %q does not match input %q", original.URI, sourceURI)
		}
		return original.Range, nil
	}

	var (
		result goxmap.Range
		found  bool
	)
	for _, mapping := range upstream.Mappings() {
		if mapping.Kind != goxmap.Exact || mapping.Original.URI != original.URI {
			continue
		}
		if original.Range.Start < mapping.Original.Range.Start || original.Range.End > mapping.Original.Range.End {
			continue
		}
		start := mapping.Generated.Start + original.Range.Start - mapping.Original.Range.Start
		candidate := goxmap.Range{Start: start, End: start + original.Range.Len()}
		if found && candidate != result {
			return goxmap.Range{}, fmt.Errorf("semantic projection source range %#v maps to multiple input ranges", original)
		}
		result = candidate
		found = true
	}
	if !found {
		return goxmap.Range{}, fmt.Errorf("semantic projection source range %#v has no exact input mapping", original)
	}
	return result, nil
}

func semanticProjectionPointAnchor(source string, offset int) goxmap.Range {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	if offset < len(source) {
		return goxmap.Range{Start: offset, End: offset + 1}
	}
	if offset > 0 {
		return goxmap.Range{Start: offset - 1, End: offset}
	}
	return goxmap.Range{Start: 0, End: 0}
}
