package gox

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/gox-lang/server/internal/goxmap"
)

func TestPhase16_2A1SourceMapSweepMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(160201))
	for iteration := 0; iteration < 2000; iteration++ {
		count := 1 + rng.Intn(96)
		candidates := make([]bundleMapCandidate, 0, count)
		for index := 0; index < count; index++ {
			start := rng.Intn(256)
			end := start + 1 + rng.Intn(257-start)
			kind := goxmap.MappingKind(rng.Intn(2))
			originalStart := rng.Intn(512)
			originalEnd := originalStart + 1 + rng.Intn(64)
			if kind == goxmap.Exact {
				originalEnd = originalStart + end - start
			}
			candidates = append(candidates, bundleMapCandidate{
				generated: goxmap.Range{Start: start, End: end},
				origin: NodeOrigin{Source: goxmap.SourceRange{
					URI:   []string{"a.gox", "b.gox", "c.gox"}[rng.Intn(3)],
					Range: goxmap.Range{Start: originalStart, End: originalEnd},
				}},
				kind: kind,
			})
		}

		got := flattenBundleMapCandidates(candidates)
		want := phase16ReferenceFlattenBundleMapCandidates(candidates)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: sweep mappings differ from reference\n got: %#v\nwant: %#v", iteration, got, want)
		}
	}
}

func TestPhase16_2A1SourceMapSweepPreservesStableTieBreak(t *testing.T) {
	candidates := []bundleMapCandidate{
		{
			generated: goxmap.Range{Start: 0, End: 20},
			origin:    NodeOrigin{Source: goxmap.SourceRange{URI: "first.gox", Range: goxmap.Range{Start: 10, End: 30}}},
			kind:      goxmap.Anchor,
		},
		{
			generated: goxmap.Range{Start: 0, End: 20},
			origin:    NodeOrigin{Source: goxmap.SourceRange{URI: "second.gox", Range: goxmap.Range{Start: 40, End: 60}}},
			kind:      goxmap.Anchor,
		},
	}
	mappings := flattenBundleMapCandidates(candidates)
	if len(mappings) != 1 {
		t.Fatalf("mappings = %d, want 1", len(mappings))
	}
	if mappings[0].Original.URI != "first.gox" {
		t.Fatalf("stable tie selected %q, want first.gox", mappings[0].Original.URI)
	}
}

func BenchmarkPhase16_2A1FlattenBundleMapCandidates(b *testing.B) {
	for _, count := range []int{128, 256, 512, 1024} {
		candidates := phase16NestedBundleMapCandidates(count)
		b.Run("Sweep/N"+itoaPhase16(count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				phase16BundleMappingsSink = flattenBundleMapCandidates(candidates)
			}
		})
		b.Run("Reference/N"+itoaPhase16(count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				phase16BundleMappingsSink = phase16ReferenceFlattenBundleMapCandidates(candidates)
			}
		})
	}
}

var phase16BundleMappingsSink []goxmap.Mapping

func phase16NestedBundleMapCandidates(count int) []bundleMapCandidate {
	candidates := make([]bundleMapCandidate, 0, count*2)
	for i := 0; i < count; i++ {
		start := i
		end := count*2 - i
		kind := goxmap.Anchor
		if i%4 == 0 {
			kind = goxmap.Exact
		}
		originalStart := i * 4
		originalEnd := originalStart + 1
		if kind == goxmap.Exact {
			originalEnd = originalStart + end - start
		}
		candidates = append(candidates, bundleMapCandidate{
			generated: goxmap.Range{Start: start, End: end},
			origin: NodeOrigin{Source: goxmap.SourceRange{
				URI:   "pages/index.gox",
				Range: goxmap.Range{Start: originalStart, End: originalEnd},
			}},
			kind: kind,
		})
	}
	return candidates
}

func phase16ReferenceFlattenBundleMapCandidates(candidates []bundleMapCandidate) []goxmap.Mapping {
	boundaries := make([]int, 0, len(candidates)*2)
	for _, candidate := range candidates {
		boundaries = append(boundaries, candidate.generated.Start, candidate.generated.End)
	}
	sort.Ints(boundaries)
	boundaries = compactBundleBoundaries(boundaries)

	mappings := make([]goxmap.Mapping, 0, len(boundaries))
	for index := 0; index+1 < len(boundaries); index++ {
		segment := goxmap.Range{Start: boundaries[index], End: boundaries[index+1]}
		if segment.Empty() {
			continue
		}
		candidate, ok := bestBundleMapCandidate(candidates, segment)
		if !ok {
			continue
		}
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

func itoaPhase16(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for value > 0 {
		pos--
		buf[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[pos:])
}
