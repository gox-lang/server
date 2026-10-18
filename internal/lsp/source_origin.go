package lsp

import (
	"go/ast"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

// moduleSourceOrigin returns source provenance for a node from whichever AST
// owns it. Semantic nodes are checked first because editor type semantics are
// authoritative there; runtime/compiler nodes remain supported for GOX-only
// validators that intentionally operate on runtime/template structures.
func moduleSourceOrigin(module *goxcompiler.Module, node ast.Node) (goxcompiler.NodeOrigin, bool) {
	if module == nil || node == nil {
		return goxcompiler.NodeOrigin{}, false
	}
	if module.Semantic != nil && module.Semantic.Origins != nil {
		if origin, ok := module.Semantic.Origins.Get(node); ok {
			return origin, true
		}
	}
	if module.Origins != nil {
		return module.Origins.Get(node)
	}
	return goxcompiler.NodeOrigin{}, false
}

func sourceOriginInsideTemplate(module *goxcompiler.Module, origin goxcompiler.NodeOrigin) bool {
	if module == nil || !origin.Source.Range.Valid() {
		return false
	}
	for _, occurrence := range module.TemplateOccurrences {
		container := occurrence.Source
		if container.URI != origin.Source.URI || !container.Range.Valid() {
			continue
		}
		if origin.Source.Range.Start >= container.Range.Start && origin.Source.Range.End <= container.Range.End {
			return true
		}
	}
	return false
}
