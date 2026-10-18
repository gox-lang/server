package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGoxSDKHoverShowsGroupedPublicAPI(t *testing.T) {
	root, pagePath, pageSource := goxSDKPublicAPIFixture(t)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	hover := hoverAtOffset(t, server, document, strings.Index(pageSource, "gox.Component")+1)
	if hover == nil {
		t.Fatal("GOX SDK hover is nil")
	}
	for _, want := range []string{
		"GOX SDK public API", "**Types**", "interface Component", "struct Context",
		"**Functions**", "func Render(value string) Component", "**Variables**", "var Version string",
		"**Constants**", "const DefaultName", "explicit alias `gox`",
	} {
		if !strings.Contains(hover.Contents.Value, want) {
			t.Errorf("GOX SDK hover missing %q: %s", want, hover.Contents.Value)
		}
	}
	if strings.Contains(hover.Contents.Value, "privateRuntime") || strings.Contains(hover.Contents.Value, "return nil") {
		t.Fatalf("GOX SDK view exposed implementation details: %s", hover.Contents.Value)
	}
	for _, internal := range []string{"RuntimeBridge", "BindRuntime", "RuntimeField", "RuntimeMethod"} {
		if strings.Contains(hover.Contents.Value, internal) {
			t.Fatalf("GOX SDK view exposed internal API %q: %s", internal, hover.Contents.Value)
		}
	}
}

func TestGoxSDKCompletionAndDefinitionUsePublicDeclarations(t *testing.T) {
	root, pagePath, pageSource := goxSDKPublicAPIFixture(t)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	document.Text = strings.Replace(pageSource, "gox.Component", "gox.Co", 1)
	position, err := document.Position(strings.Index(document.Text, "gox.Co") + len("gox.Co"))
	if err != nil {
		t.Fatal(err)
	}
	item := requireCompletionItem(t, server.completions(document, position).Items, "Component")
	if !strings.Contains(item.Detail, "GOX SDK public API") {
		t.Fatalf("GOX SDK completion = %#v", item)
	}
	assertCompletionMissing(t, server.completions(document, position).Items, "privateRuntime")
	assertCompletionMissing(t, server.completions(document, position).Items, "RuntimeBridge")
	assertCompletionMissing(t, server.completions(document, position).Items, "BindRuntime")

	sdkPath := filepath.Join(root, "gox-sdk", "gox.go")
	locations := definitionLocationsAtOffset(t, server, Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}, strings.Index(pageSource, "Component")+1)
	if len(locations) != 1 || locations[0].URI != FileURI(sdkPath) {
		t.Fatalf("GOX SDK definition = %#v", locations)
	}
}

func goxSDKPublicAPIFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	sdk := filepath.Join(root, "gox-sdk")
	writeGoPackageHoverFile(t, filepath.Join(sdk, "go.mod"), "module github.com/gox-lang/gox\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(sdk, "gox.go"), `package gox
type Component interface{}
type Context struct {
	Request string
	// gox:internal
	RuntimeField any
	runtimeState any
}
// gox:internal
func (Context) RuntimeMethod() {}
// gox:internal
type RuntimeBridge interface{}
const DefaultName = "George"
var Version string
func Render(value string) Component { return nil }
// gox:internal
func BindRuntime() {}
func privateRuntime() {}
`)
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), `module example

go 1.24

require github.com/gox-lang/gox v0.0.0
replace github.com/gox-lang/gox => ./gox-sdk
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import gox "github.com/gox-lang/gox"
func Page() gox.Component { return nil }
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	return root, pagePath, pageSource
}

func TestGoxSDKInternalMarkerFiltersTypeMembers(t *testing.T) {
	root, pagePath, pageSource := goxSDKPublicAPIFixture(t)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document := Document{URI: FileURI(pagePath), Path: pagePath, Text: pageSource}
	_, packageInfo, _, _, ok := server.resolveGoPackageImport(document, "gox")
	if !ok {
		t.Fatal("GOX SDK import was not resolved")
	}
	for _, exported := range packageInfo.Exports {
		if exported.Name != "Context" {
			continue
		}
		for _, member := range exported.Members {
			if member.Name == "RuntimeField" || member.Name == "RuntimeMethod" {
				t.Fatalf("GOX SDK type exposed internal member: %#v", member)
			}
		}
		return
	}
	t.Fatal("public Context type was not indexed")
}

func TestRealGoxSDKSeparatesUserAndRuntimeAPI(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	packageInfo, err := NewGoPackageIndex().Load(root, goxSDKImportPath)
	if err != nil {
		t.Fatal(err)
	}
	exports := make(map[string]bool, len(packageInfo.Exports))
	for _, exported := range packageInfo.Exports {
		exports[exported.Name] = true
	}
	for _, public := range []string{"Component", "Context", "HTML", "File", "WritePageFile"} {
		if !exports[public] {
			t.Errorf("GOX SDK public API missing %q", public)
		}
	}
	for _, internal := range []string{
		"ActionSpec", "BindClientIslandRuntime", "BindServerInfo", "ClientIslandRuntime",
		"ResetRuntimeContext", "ResponseImportMapRuntime", "SetRuntimeResponseState",
	} {
		if exports[internal] {
			t.Errorf("GOX SDK exposed runtime API %q", internal)
		}
	}
}
