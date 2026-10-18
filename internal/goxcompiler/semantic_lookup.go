package gox

import (
	"go/ast"
	"go/types"
)

// SemanticNode is the typed Go AST node that represents a position in the
// original .gox source. Node identity is preserved, so Info can be queried
// directly with Expr, Ident, or Selector.
type SemanticNode struct {
	Node      ast.Node
	Expr      ast.Expr
	Ident     *ast.Ident
	Selector  *ast.SelectorExpr
	TypeValue types.TypeAndValue
	Object    types.Object
	Selection *types.Selection
}

// SemanticAtOffset returns the most specific exact semantic AST node that
// contains the byte offset in the original .gox source. Runtime/compiler nodes
// are deliberately ignored: editor semantics come exclusively from
// Module.Semantic.File and its own Origins table.
func (m *Module) SemanticAtOffset(offset int) (SemanticNode, bool) {
	if m == nil || m.Semantic == nil || m.Semantic.File == nil || m.Semantic.Origins == nil || m.Semantic.Info == nil || offset < 0 || offset > len(m.Source) {
		return SemanticNode{}, false
	}

	var best ast.Node
	bestLen := int(^uint(0) >> 1)
	ast.Inspect(m.Semantic.File, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		origin, ok := m.Semantic.Origins.Get(node)
		if !ok || !origin.Exact() || origin.Source.URI != m.Path {
			return true
		}
		r := origin.Source.Range
		// End is exclusive, except that a zero-width editor position at the end
		// of an expression belongs to that expression. This is needed for '.'
		// completion where the cursor sits immediately after the receiver.
		if offset < r.Start || offset > r.End {
			return true
		}
		length := r.End - r.Start
		if length < bestLen {
			best, bestLen = node, length
		}
		return true
	})
	if best == nil {
		return SemanticNode{}, false
	}
	return m.semanticNode(best), true
}

func (m *Module) semanticNode(node ast.Node) SemanticNode {
	result := SemanticNode{Node: node}
	info := m.Semantic.Info
	if expr, ok := node.(ast.Expr); ok {
		result.Expr = expr
		result.TypeValue = info.Types[expr]
	}
	if ident, ok := node.(*ast.Ident); ok {
		result.Ident = ident
		result.Object = info.ObjectOf(ident)
	}
	if selector, ok := node.(*ast.SelectorExpr); ok {
		result.Selector = selector
		result.Selection = info.Selections[selector]
	}
	return result
}
