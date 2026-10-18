package gox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/goxmap"
)

func TestWriteBundleWritesFinalSourceMapBesideGoFile(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	name := "George"
	return <h1>{{name}}</h1>
}
`
	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	if err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "index.go")
	if err := WriteBundle(bundle, outPath); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	mapData, err := os.ReadFile(outPath + ".sourcemap.json")
	if err != nil {
		t.Fatal(err)
	}
	var document goxmap.JSONDocument
	if err := json.Unmarshal(mapData, &document); err != nil {
		t.Fatal(err)
	}
	if document.GeneratedURI != outPath || len(document.Mappings) == 0 {
		t.Fatalf("source map document = %#v", document)
	}
	literalOffset := strings.Index(string(generated), `"George"`)
	foundExact := false
	for _, mapping := range document.Mappings {
		if mapping.Kind == "exact" && mapping.Generated.Start.Offset <= literalOffset && mapping.Generated.End.Offset > literalOffset {
			foundExact = true
			break
		}
	}
	if !foundExact {
		t.Fatalf("generated literal at %d has no exact mapping", literalOffset)
	}
}
