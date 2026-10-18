package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

// These tests describe the architectural contract for Go semantics inside GOX.
// Active tests protect behavior that already works. Pending tests are executable
// specifications for the semantic-projection phases; they should be enabled as
// soon as the corresponding phase makes the contract true.

func TestSemanticArchitectureContractGoOutsideTemplate(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	server := NewServer()

	t.Run("local completion inside call argument", func(t *testing.T) {
		source := `module page
import "strings"
func Build() {
    text := "hello"
    _ = strings.NewReplacer("a", "b").Replace(te)
}
`
		document := Document{URI: FileURI(path), Path: path, Text: source}
		items := completionItemsAtEnd(t, server, document, "te")
		assertHasCompletion(t, items, "text")
	})

	t.Run("deep chained receiver completion", func(t *testing.T) {
		source := `module page
import "reflect"
func Build() {
    text := "hello"
    _ = reflect.ValueOf(&text).Elem().
}
`
		document := Document{URI: FileURI(path), Path: path, Text: source}
		items := completionItemsAtEnd(t, server, document, "Elem().")
		assertHasCompletion(t, items, "CanSet")
	})

	t.Run("hover and signature use chained semantic object", func(t *testing.T) {
		source := `module page
import "strings"
func Build() {
    text := "hello"
    _ = strings.NewReplacer("a", "b").Replace(text)
}
`
		document := Document{URI: FileURI(path), Path: path, Text: source}
		replace := strings.LastIndex(source, "Replace")
		hover := hoverAtOffset(t, server, document, replace+1)
		if hover == nil || !strings.Contains(hover.Contents.Value, "Replace(s string) string") {
			t.Fatalf("Replace hover = %#v", hover)
		}
		help := signatureAtOffset(t, server, document, replace+len("Replace("))
		if help == nil || len(help.Signatures) != 1 || !strings.Contains(help.Signatures[0].Label, "Replace(s string) string") {
			t.Fatalf("Replace signature help = %#v", help)
		}
	})
}

func TestSemanticArchitectureContractBareAddressBelongsToGoOutsideMarkup(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	path := filepath.Join(root, "page.gox")
	source := `module page
import "reflect"
func Build() {
    text := "hello"
    _ = reflect.ValueOf(&te)
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	items := completionItemsAtEnd(t, NewServer(), document, "te")
	assertHasCompletion(t, items, "text")
}

func TestSemanticArchitectureContractReturnedTemplateGoParity(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "gox-sdk")
	writeGoPackageHoverFile(t, filepath.Join(sdk, "go.mod"), "module github.com/gox-lang/gox\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(sdk, "gox.go"), "package gox\ntype Component interface{}\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), `module example

go 1.24

require github.com/gox-lang/gox v0.0.0
replace github.com/gox-lang/gox => ./gox-sdk
`)
	path := filepath.Join(root, "page.gox")
	source := `module page
import (
    gox "github.com/gox-lang/gox"
    "reflect"
    "strings"
)
func Build() gox.Component {
    text := "hello"
    return <main>
        {{strings.NewReplacer("a", "b").Replace(text)}}
        {{reflect.ValueOf(&text).Elem()}}
    </main>
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	server := NewServer()

	replace := strings.LastIndex(source, "Replace")
	hover := hoverAtOffset(t, server, document, replace+1)
	if hover == nil || !strings.Contains(hover.Contents.Value, "Replace(s string) string") {
		t.Fatalf("template Replace hover = %#v", hover)
	}
	help := signatureAtOffset(t, server, document, replace+len("Replace("))
	if help == nil || len(help.Signatures) != 1 || !strings.Contains(help.Signatures[0].Label, "Replace(s string) string") {
		t.Fatalf("template Replace signature help = %#v", help)
	}
}

func TestSemanticArchitectureContractValueTemplateGoParity(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "gox-sdk")
	writeGoPackageHoverFile(t, filepath.Join(sdk, "go.mod"), "module github.com/gox-lang/gox\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(sdk, "gox.go"), "package gox\ntype Component interface{}\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), `module example

go 1.24

require github.com/gox-lang/gox v0.0.0
replace github.com/gox-lang/gox => ./gox-sdk
`)
	path := filepath.Join(root, "page.gox")
	source := `module page
import (
    gox "github.com/gox-lang/gox"
    "reflect"
)
func Build() gox.Component {
    text := "hello"
    view := <span>{{reflect.ValueOf(&text).Elem()}}</span>
    return view
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	server := NewServer()
	elem := strings.LastIndex(source, "Elem")
	hover := hoverAtOffset(t, server, document, elem+1)
	if hover == nil || !strings.Contains(hover.Contents.Value, "Elem()") {
		t.Fatalf("template Elem hover = %#v", hover)
	}
}

func TestSemanticArchitectureContractAttributeExpressionUsesGoSemantics(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "gox-sdk")
	writeGoPackageHoverFile(t, filepath.Join(sdk, "go.mod"), "module github.com/gox-lang/gox\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(sdk, "gox.go"), "package gox\ntype Component interface{}\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), `module example

go 1.24

require github.com/gox-lang/gox v0.0.0
replace github.com/gox-lang/gox => ./gox-sdk
`)
	path := filepath.Join(root, "page.gox")
	source := `module page
import gox "github.com/gox-lang/gox"
func Menu(text *string) gox.Component { return <span /> }
func Build() gox.Component {
    text := "hello"
    return <&Menu text={{&te}} />
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	items := completionItemsAtEnd(t, NewServer(), document, "te")
	assertHasCompletion(t, items, "text")
}

func TestSemanticArchitectureContractComponentSigilStaysTemplateMarkup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.gox")
	source := `module page
import gox "github.com/gox-lang/gox"
func Menu() gox.Component { return <nav>Menu</nav> }
func Build() gox.Component {
    return <main>&Men/*cursor*/</main>
}
`
	document := Document{URI: FileURI(path), Path: path, Text: source}
	items := lexicalItemsAtMarker(t, NewServer(), document, "/*cursor*/")
	requireSigilCompletion(t, items, "&Menu")
}
