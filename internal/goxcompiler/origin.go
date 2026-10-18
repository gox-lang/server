package gox

import (
	"go/ast"
	"go/token"

	"github.com/gox-lang/server/internal/goxmap"
)

// NodeOrigin records the GOX source range represented by one Go AST node.
// Exact nodes came from preserved source bytes; Anchor nodes were parsed from
// compiler-generated Go associated with a GOX construct.
type NodeOrigin struct {
	Source goxmap.SourceRange
	Kind   goxmap.MappingKind
}

// Exact reports whether the node preserves its source bytes exactly.
func (o NodeOrigin) Exact() bool {
	return o.Kind == goxmap.Exact
}

// OriginTable stores source provenance by AST node identity. Nodes are pointer
// values in Go's AST, so their entries survive in-place tree mutations.
type OriginTable struct {
	entries map[ast.Node]NodeOrigin
}

// NewOriginTable creates an empty origin table.
func NewOriginTable() *OriginTable {
	return &OriginTable{entries: make(map[ast.Node]NodeOrigin)}
}

// SeedOriginTable records every parsed node fully contained in one source-map
// segment. Larger nodes crossing mapping boundaries are omitted deliberately.
func SeedOriginTable(file *ast.File, fset *token.FileSet, sourceMap *goxmap.Map) *OriginTable {
	table := NewOriginTable()
	if file == nil || fset == nil || sourceMap == nil {
		return table
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil || !node.Pos().IsValid() || !node.End().IsValid() {
			return true
		}
		start := fset.PositionFor(node.Pos(), false).Offset
		end := fset.PositionFor(node.End(), false).Offset
		source, kind, ok := sourceMap.OriginalRange(goxmap.Range{Start: start, End: end})
		if ok {
			table.Set(node, NodeOrigin{Source: source, Kind: kind})
		}
		return true
	})
	return table
}

// Len returns the number of nodes with known provenance.
func (t *OriginTable) Len() int {
	if t == nil {
		return 0
	}
	return len(t.entries)
}

// Merge adds origins from another table. Existing entries are replaced only
// when the incoming table contains the same AST node identity.
func (t *OriginTable) Merge(other *OriginTable) {
	if t == nil || other == nil {
		return
	}
	for node, origin := range other.entries {
		t.entries[node] = origin
	}
}

// Get returns the origin associated with node.
func (t *OriginTable) Get(node ast.Node) (NodeOrigin, bool) {
	if t == nil || node == nil {
		return NodeOrigin{}, false
	}
	origin, ok := t.entries[node]
	return origin, ok
}

// Set associates a node with an explicit origin.
func (t *OriginTable) Set(node ast.Node, origin NodeOrigin) {
	if t == nil || node == nil || origin.Source.URI == "" || !origin.Source.Range.Valid() {
		return
	}
	t.entries[node] = origin
}

// Inherit associates a generated node with the source range of another node.
// The inherited origin is always an anchor because the generated node does not
// preserve the source node's bytes.
func (t *OriginTable) Inherit(generated ast.Node, source ast.Node) bool {
	origin, ok := t.Get(source)
	if !ok || generated == nil {
		return false
	}
	origin.Kind = goxmap.Anchor
	t.Set(generated, origin)
	return true
}

// InheritTree anchors every node in a generated subtree to source. Existing
// entries are preserved so reused parsed expressions retain exact provenance.
func (t *OriginTable) InheritTree(generated ast.Node, source ast.Node) bool {
	origin, ok := t.Get(source)
	if !ok || generated == nil {
		return false
	}
	origin.Kind = goxmap.Anchor
	ast.Inspect(generated, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		if _, exists := t.Get(node); !exists {
			t.Set(node, origin)
		}
		return true
	})
	return true
}

// AnchorGeneratedSelectors records receiver-qualified selectors introduced by
// a binding pass. The original identifier is reused as Sel and remains exact.
func (t *OriginTable) AnchorGeneratedSelectors(file *ast.File, receiverName string) {
	if t == nil || file == nil || receiverName == "" {
		return
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || receiver.Name != receiverName {
			return true
		}
		if _, exists := t.Get(selector); exists {
			return true
		}
		if _, ok := t.Get(selector.Sel); ok {
			t.InheritTree(selector, selector.Sel)
		}
		return true
	})
}
