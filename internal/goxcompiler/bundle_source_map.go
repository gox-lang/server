package gox

import (
	"bytes"
	"container/heap"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"reflect"
	"sort"

	"github.com/gox-lang/server/internal/goxmap"
)

type bundleMapCandidate struct {
	generated goxmap.Range
	origin    NodeOrigin
	kind      goxmap.MappingKind
}

// FormatBundleMapped formats the bundle and builds a source map from the
// formatted Go bytes to the original GOX documents.
func FormatBundleMapped(bundle *Bundle, generatedURI string) (string, *goxmap.Map, error) {
	if bundle == nil {
		return "", nil, fmt.Errorf("bundle cannot be nil")
	}
	if generatedURI == "" {
		return "", nil, fmt.Errorf("generated URI cannot be empty")
	}
	original := &ast.File{Name: ast.NewIdent(bundle.GoPackage), Decls: bundle.Decls}
	var output bytes.Buffer
	if err := format.Node(&output, token.NewFileSet(), original); err != nil {
		return "", nil, err
	}
	formatted := output.String()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, generatedURI, formatted, parser.ParseComments)
	if err != nil {
		return "", nil, err
	}
	originalNodes := bundleComparableNodes(original)
	parsedNodes := bundleComparableNodes(parsed)
	if len(originalNodes) != len(parsedNodes) {
		return "", nil, fmt.Errorf("formatted bundle AST changed shape: %d source nodes, %d parsed nodes", len(originalNodes), len(parsedNodes))
	}

	candidates := make([]bundleMapCandidate, 0, bundle.Origins.Len())
	for index, sourceNode := range originalNodes {
		generatedNode := parsedNodes[index]
		if reflect.TypeOf(sourceNode) != reflect.TypeOf(generatedNode) {
			return "", nil, fmt.Errorf("formatted bundle AST node %d changed from %T to %T", index, sourceNode, generatedNode)
		}
		origin, ok := bundle.Origins.Get(sourceNode)
		if !ok || !generatedNode.Pos().IsValid() || !generatedNode.End().IsValid() {
			continue
		}
		start := fset.PositionFor(generatedNode.Pos(), false).Offset
		end := fset.PositionFor(generatedNode.End(), false).Offset
		generated := goxmap.Range{Start: start, End: end}
		if !generated.Valid() || generated.Empty() || generated.End > len(formatted) {
			continue
		}
		kind := goxmap.Anchor
		if origin.Kind == goxmap.Exact && exactBundleCandidate(formatted, generated, origin.Source, bundle.Sources) {
			kind = goxmap.Exact
		}
		candidates = append(candidates, bundleMapCandidate{generated: generated, origin: origin, kind: kind})
	}
	mappings := flattenBundleMapCandidates(candidates)
	sourceMap, err := goxmap.New(generatedURI, mappings)
	if err != nil {
		return "", nil, err
	}
	return formatted, sourceMap, nil
}

func bundleComparableNodes(root ast.Node) []ast.Node {
	nodes := make([]ast.Node, 0)
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		switch node.(type) {
		case *ast.Comment, *ast.CommentGroup:
			return false
		}
		nodes = append(nodes, node)
		return true
	})
	return nodes
}

func exactBundleCandidate(formatted string, generated goxmap.Range, source goxmap.SourceRange, sources map[string]string) bool {
	original, ok := sources[source.URI]
	if !ok || source.Range.End > len(original) || generated.Len() != source.Range.Len() {
		return false
	}
	return formatted[generated.Start:generated.End] == original[source.Range.Start:source.Range.End]
}

func flattenBundleMapCandidates(candidates []bundleMapCandidate) []goxmap.Mapping {
	if len(candidates) == 0 {
		return nil
	}

	boundaries := make([]int, 0, len(candidates)*2)
	order := make([]int, len(candidates))
	for index, candidate := range candidates {
		boundaries = append(boundaries, candidate.generated.Start, candidate.generated.End)
		order[index] = index
	}
	sort.Ints(boundaries)
	boundaries = compactBundleBoundaries(boundaries)
	sort.SliceStable(order, func(i, j int) bool {
		return candidates[order[i]].generated.Start < candidates[order[j]].generated.Start
	})

	// Every candidate start/end is a boundary. While sweeping consecutive
	// boundary segments, a candidate covers the whole segment exactly when it
	// has started and has not ended at the segment start. Keeping those active
	// candidates in priority order avoids rescanning every candidate for every
	// segment while preserving bestBundleMapCandidate's selection rules.
	active := make(bundleMapCandidateHeap, 0, len(candidates))
	heap.Init(&active)
	next := 0
	mappings := make([]goxmap.Mapping, 0, len(boundaries))
	for index := 0; index+1 < len(boundaries); index++ {
		segment := goxmap.Range{Start: boundaries[index], End: boundaries[index+1]}
		if segment.Empty() {
			continue
		}

		for next < len(order) && candidates[order[next]].generated.Start <= segment.Start {
			candidateIndex := order[next]
			heap.Push(&active, bundleMapHeapItem{candidate: candidates[candidateIndex], index: candidateIndex})
			next++
		}
		for active.Len() > 0 && active[0].candidate.generated.End <= segment.Start {
			heap.Pop(&active)
		}
		if active.Len() == 0 {
			continue
		}

		candidate := active[0].candidate
		original := candidate.origin.Source
		if candidate.kind == goxmap.Exact {
			start := original.Range.Start + segment.Start - candidate.generated.Start
			original.Range = goxmap.Range{Start: start, End: start + segment.Len()}
		}
		mapping := goxmap.Mapping{Generated: segment, Original: original, Kind: candidate.kind}
		mappings = appendBundleMapping(mappings, mapping)
	}
	return mappings
}

type bundleMapHeapItem struct {
	candidate bundleMapCandidate
	index     int
}

type bundleMapCandidateHeap []bundleMapHeapItem

func (h bundleMapCandidateHeap) Len() int { return len(h) }

func (h bundleMapCandidateHeap) Less(i, j int) bool {
	left, right := h[i], h[j]
	if left.candidate.kind != right.candidate.kind {
		return left.candidate.kind < right.candidate.kind
	}
	if left.candidate.generated.Len() != right.candidate.generated.Len() {
		return left.candidate.generated.Len() < right.candidate.generated.Len()
	}
	return left.index < right.index
}

func (h bundleMapCandidateHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *bundleMapCandidateHeap) Push(value any) {
	*h = append(*h, value.(bundleMapHeapItem))
}

func (h *bundleMapCandidateHeap) Pop() any {
	items := *h
	last := len(items) - 1
	item := items[last]
	items[last] = bundleMapHeapItem{}
	*h = items[:last]
	return item
}

func compactBundleBoundaries(values []int) []int {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func bestBundleMapCandidate(candidates []bundleMapCandidate, segment goxmap.Range) (bundleMapCandidate, bool) {
	var best bundleMapCandidate
	found := false
	for _, candidate := range candidates {
		if candidate.generated.Start > segment.Start || candidate.generated.End < segment.End {
			continue
		}
		if !found || candidate.kind < best.kind || (candidate.kind == best.kind && candidate.generated.Len() < best.generated.Len()) {
			best = candidate
			found = true
		}
	}
	return best, found
}

func appendBundleMapping(mappings []goxmap.Mapping, mapping goxmap.Mapping) []goxmap.Mapping {
	if len(mappings) == 0 {
		return append(mappings, mapping)
	}
	last := &mappings[len(mappings)-1]
	if last.Generated.End != mapping.Generated.Start || last.Kind != mapping.Kind || last.Original.URI != mapping.Original.URI {
		return append(mappings, mapping)
	}
	if mapping.Kind == goxmap.Exact && last.Original.Range.End == mapping.Original.Range.Start {
		last.Generated.End = mapping.Generated.End
		last.Original.Range.End = mapping.Original.Range.End
		return mappings
	}
	if mapping.Kind == goxmap.Anchor && last.Original.Range == mapping.Original.Range {
		last.Generated.End = mapping.Generated.End
		return mappings
	}
	return append(mappings, mapping)
}
