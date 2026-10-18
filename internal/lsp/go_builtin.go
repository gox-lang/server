package lsp

import (
	"go/types"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type goBuiltinSignature struct {
	Label      string
	Parameters []goxcompiler.SourceParameter
	Results    []string
	Type       string
}

var goBuiltinSignatures = map[string]goBuiltinSignature{
	"append":  {Label: "func append(slice []Type, elems ...Type) []Type", Parameters: []goxcompiler.SourceParameter{{Name: "slice", Type: "[]Type"}, {Name: "elems", Type: "...Type"}}, Results: []string{"[]Type"}, Type: "[]Type"},
	"cap":     {Label: "func cap(v Type) int", Parameters: []goxcompiler.SourceParameter{{Name: "v", Type: "Type"}}, Results: []string{"int"}, Type: "int"},
	"clear":   {Label: "func clear[T ~[]Type | ~map[Type]Type1](t T)", Parameters: []goxcompiler.SourceParameter{{Name: "t", Type: "T"}}},
	"close":   {Label: "func close(c chan<- Type)", Parameters: []goxcompiler.SourceParameter{{Name: "c", Type: "chan<- Type"}}},
	"complex": {Label: "func complex(r, i FloatType) ComplexType", Parameters: []goxcompiler.SourceParameter{{Name: "r", Type: "FloatType"}, {Name: "i", Type: "FloatType"}}, Results: []string{"ComplexType"}, Type: "ComplexType"},
	"copy":    {Label: "func copy(dst, src []Type) int", Parameters: []goxcompiler.SourceParameter{{Name: "dst", Type: "[]Type"}, {Name: "src", Type: "[]Type"}}, Results: []string{"int"}, Type: "int"},
	"delete":  {Label: "func delete(m map[Type]Type1, key Type)", Parameters: []goxcompiler.SourceParameter{{Name: "m", Type: "map[Type]Type1"}, {Name: "key", Type: "Type"}}},
	"imag":    {Label: "func imag(c ComplexType) FloatType", Parameters: []goxcompiler.SourceParameter{{Name: "c", Type: "ComplexType"}}, Results: []string{"FloatType"}, Type: "FloatType"},
	"len":     {Label: "func len(v Type) int", Parameters: []goxcompiler.SourceParameter{{Name: "v", Type: "Type"}}, Results: []string{"int"}, Type: "int"},
	"make":    {Label: "func make(t Type, size ...IntegerType) Type", Parameters: []goxcompiler.SourceParameter{{Name: "t", Type: "Type"}, {Name: "size", Type: "...IntegerType"}}, Results: []string{"Type"}, Type: "Type"},
	"max":     {Label: "func max[T cmp.Ordered](x T, y ...T) T", Parameters: []goxcompiler.SourceParameter{{Name: "x", Type: "T"}, {Name: "y", Type: "...T"}}, Results: []string{"T"}, Type: "T"},
	"min":     {Label: "func min[T cmp.Ordered](x T, y ...T) T", Parameters: []goxcompiler.SourceParameter{{Name: "x", Type: "T"}, {Name: "y", Type: "...T"}}, Results: []string{"T"}, Type: "T"},
	"new":     {Label: "func new(Type) *Type", Parameters: []goxcompiler.SourceParameter{{Type: "Type"}}, Results: []string{"*Type"}, Type: "*Type"},
	"panic":   {Label: "func panic(v any)", Parameters: []goxcompiler.SourceParameter{{Name: "v", Type: "any"}}},
	"print":   {Label: "func print(args ...Type)", Parameters: []goxcompiler.SourceParameter{{Name: "args", Type: "...Type"}}},
	"println": {Label: "func println(args ...Type)", Parameters: []goxcompiler.SourceParameter{{Name: "args", Type: "...Type"}}},
	"real":    {Label: "func real(c ComplexType) FloatType", Parameters: []goxcompiler.SourceParameter{{Name: "c", Type: "ComplexType"}}, Results: []string{"FloatType"}, Type: "FloatType"},
	"recover": {Label: "func recover() any", Results: []string{"any"}, Type: "any"},
}

func goBuiltinSignatureFor(name string) (goBuiltinSignature, bool) {
	object := types.Universe.Lookup(name)
	if object == nil {
		return goBuiltinSignature{}, false
	}
	if _, ok := object.(*types.Builtin); !ok {
		return goBuiltinSignature{}, false
	}
	signature, ok := goBuiltinSignatures[name]
	return signature, ok
}

func goBuiltinSignatureCandidate(name string) (signatureCandidate, bool) {
	signature, ok := goBuiltinSignatureFor(name)
	if !ok {
		return signatureCandidate{}, false
	}
	return signatureCandidate{Label: signature.Label, Parameters: signature.Parameters, Results: signature.Results, Type: signature.Type}, true
}

func goBuiltinCompletionItems(prefix string) []CompletionItem {
	items := make([]CompletionItem, 0, len(goBuiltinSignatures))
	for name, signature := range goBuiltinSignatures {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if _, ok := goBuiltinSignatureFor(name); !ok {
			continue
		}
		items = append(items, CompletionItem{
			Label:               name,
			Kind:                3,
			Detail:              signature.Label,
			InsertText:          name,
			SortText:            "2_" + name,
			CandidateType:       signature.Type,
			CandidateResults:    signature.Results,
			CandidateParameters: signature.Parameters,
		})
	}
	return items
}
