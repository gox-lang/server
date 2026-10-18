package gox

import "github.com/gox-lang/server/internal/goxmap"

// keepLegacySemanticIndexesOutsideTemplates prevents the source indexes that
// predate SemanticProjection from observing template expressions before their
// consumers are migrated. The semantic model itself already contains those
// expressions; Phase 5+ will progressively replace these compatibility
// boundaries with semantic template scopes and references.
func keepLegacySemanticIndexesOutsideTemplates(locals []LocalSymbol, selectors []SelectorReference, unresolved []UnresolvedIdentifier, templates []TemplateOccurrence) ([]LocalSymbol, []SelectorReference, []UnresolvedIdentifier) {
	filteredLocals := locals[:0]
	for _, symbol := range locals {
		if !sourceRangeInsideTemplate(symbol.Source, templates) {
			filteredLocals = append(filteredLocals, symbol)
		}
	}

	filteredSelectors := selectors[:0]
	for _, reference := range selectors {
		if !sourceRangeInsideTemplate(reference.Source, templates) {
			filteredSelectors = append(filteredSelectors, reference)
		}
	}

	filteredUnresolved := unresolved[:0]
	for _, identifier := range unresolved {
		if !sourceRangeInsideTemplate(identifier.Source, templates) {
			filteredUnresolved = append(filteredUnresolved, identifier)
		}
	}
	return filteredLocals, filteredSelectors, filteredUnresolved
}

func sourceRangeInsideTemplate(source goxmap.SourceRange, templates []TemplateOccurrence) bool {
	if !source.Range.Valid() {
		return false
	}
	for _, template := range templates {
		container := template.Source
		if source.URI != container.URI || !container.Range.Valid() {
			continue
		}
		if source.Range.Start >= container.Range.Start && source.Range.End <= container.Range.End {
			return true
		}
	}
	return false
}
