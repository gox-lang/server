package lsp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSignatureHelpTracksNestedCallsAndActiveParameter(t *testing.T) {
	source := `module page
func Inner(value string, trim bool) string { return value }
func Format(prefix string, value string, counts ...int) string { return value }
func Build() {
	_ = Format("name", Inner("George", true), 1, 2)
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}

	inner := signatureAtOffset(t, server, document, strings.Index(source, "true),")+len("true"))
	assertSignature(t, inner, "func Inner(value string, trim bool) string", 1, "value string", "trim bool")

	outer := signatureAtOffset(t, server, document, strings.Index(source, ", 1, 2)")+len(", 1, 2"))
	assertSignature(t, outer, "func Format(prefix string, value string, counts ...int) string", 2, "prefix string", "value string", "counts ...int")
}

func TestSignatureHelpSurvivesIncompleteCallsAndMultipleSyntaxErrors(t *testing.T) {
	valid := `module page
func Format(prefix string, value int) string { return prefix }
func Build() { _ = Format("x", 1) }
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: valid}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	document.Text = `module page
func Format(prefix string, value int) string { return prefix }
func Build() {
	Format(
	Format("x",
}
`
	if err := server.symbols.UpdateOpen(document); err == nil {
		t.Fatal("incomplete calls unexpectedly produced a valid index")
	}
	help := signatureAtOffset(t, server, document, strings.Index(document.Text, "Format(\"x\",")+len("Format(\"x\","))
	assertSignature(t, help, "func Format(prefix string, value int) string", 1, "prefix string", "value int")
}

func TestSignatureHelpResolvesMethodsAndImportedGOXFunctions(t *testing.T) {
	root := t.TempDir()
	usersPath := filepath.Join(root, "users.gox")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, usersPath, `module users
type User struct { Name string }
func Lookup(id int, strict bool) (User, error) { return User{}, nil }
`)
	pageSource := `module page
import users "./users.gox"
type Profile struct { Name string }
func (profile *Profile) Rename(name string, trim bool) { profile.Name = name }
func Build(profile *Profile) {
	profile.Rename("George", true)
	_, _ = users.Lookup(10, false)
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	method := signatureAtOffset(t, server, document, strings.Index(pageSource, "true)")+len("true"))
	assertSignature(t, method, "func (profile *Profile) Rename(name string, trim bool)", 1, "name string", "trim bool")
	imported := signatureAtOffset(t, server, document, strings.Index(pageSource, "false)")+len("false"))
	assertSignature(t, imported, "func Lookup(id int, strict bool) (User, error)", 1, "id int", "strict bool")
}

func TestSignatureHelpResolvesGoPackagesInsideTemplateExpressions(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import "strings"
func Card(name string) gox.Component {
	return <span>{{ strings.ReplaceAll(name, "a", "b") }}</span>
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	help := signatureAtOffset(t, server, document, strings.Index(pageSource, `"b")`)+len(`"b"`))
	assertSignature(t, help, "func ReplaceAll(s string, old string, new string) string", 2, "s string", "old string", "new string")
}

func TestSignatureHelpNeverOffersComponentsAsFunctions(t *testing.T) {
	source := `module page
func Card(name string) gox.Component { return <span>{{ name }}</span> }
func Build() { _ = Card("George") }
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	if help := signatureAtOffset(t, server, document, strings.Index(source, `"George")`)+len(`"George"`)); help != nil {
		t.Fatalf("component signature help = %#v", help)
	}
}

func TestServerAdvertisesAndReturnsSignatureHelp(t *testing.T) {
	uri := "file:///project/page.gox"
	source := "module page\nfunc Join(left string, right string) string { return left + right }\nfunc Build() { _ = Join(\"a\", \"b\") }\n"
	document := Document{URI: uri, Path: "/project/page.gox", Text: source}
	position, err := document.Position(strings.Index(source, `"b")`) + len(`"b"`))
	if err != nil {
		t.Fatal(err)
	}
	requests := framedMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`) +
		framedMessage(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","languageId":"gox","version":1,"text":`+mustJSONText(t, source)+`}}}`) +
		framedMessage(`{"jsonrpc":"2.0","id":2,"method":"textDocument/signatureHelp","params":{"textDocument":{"uri":"`+uri+`"},"position":{"line":`+strconv.Itoa(position.Line)+`,"character":`+strconv.Itoa(position.Character)+`}}}`) +
		framedMessage(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`) + framedMessage(`{"jsonrpc":"2.0","method":"exit"}`)
	var output bytes.Buffer
	if err := NewServer().Serve(strings.NewReader(requests), &output); err != nil {
		t.Fatal(err)
	}
	responses := decodeFramedResponses(t, output.String())
	if !bytes.Contains(responses[0].Result, []byte(`"signatureHelpProvider"`)) {
		t.Fatalf("initialize result = %s", responses[0].Result)
	}
	var signatureResult json.RawMessage
	for _, response := range responses {
		if string(response.ID) == "2" {
			signatureResult = response.Result
			break
		}
	}
	var help SignatureHelp
	if err := json.Unmarshal(signatureResult, &help); err != nil {
		t.Fatal(err)
	}
	assertSignature(t, &help, "func Join(left string, right string) string", 1, "left string", "right string")
}

func signatureAtOffset(t *testing.T, server *Server, document Document, offset int) *SignatureHelp {
	t.Helper()
	position, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	return server.signatureHelp(document, position)
}

func assertSignature(t *testing.T, help *SignatureHelp, label string, active int, parameters ...string) {
	t.Helper()
	if help == nil || len(help.Signatures) != 1 {
		t.Fatalf("signature help = %#v", help)
	}
	if help.Signatures[0].Label != label || help.ActiveParameter != active {
		t.Fatalf("signature help = %#v, want label %q and active parameter %d", help, label, active)
	}
	if len(help.Signatures[0].Parameters) != len(parameters) {
		t.Fatalf("parameters = %#v, want %v", help.Signatures[0].Parameters, parameters)
	}
	for index, parameter := range parameters {
		if help.Signatures[0].Parameters[index].Label != parameter {
			t.Fatalf("parameter %d = %q, want %q", index, help.Signatures[0].Parameters[index].Label, parameter)
		}
	}
}

func mustJSONText(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestSignatureHelpResolvesGoBuiltins(t *testing.T) {
	source := `module page
func Build() {
	values := []int{1}
	_ = append(values, 2)
	_ = make([]int, 4)
	_ = recover()
}
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}

	appendHelp := signatureAtOffset(t, server, document, strings.Index(source, "values, 2")+len("values,"))
	assertSignature(t, appendHelp, "func append(slice []Type, elems ...Type) []Type", 1, "slice []Type", "elems ...Type")

	makeHelp := signatureAtOffset(t, server, document, strings.Index(source, "[]int, 4")+len("[]int,"))
	assertSignature(t, makeHelp, "func make(t Type, size ...IntegerType) Type", 1, "t Type", "size ...IntegerType")

	recoverHelp := signatureAtOffset(t, server, document, strings.Index(source, "recover()")+len("recover("))
	assertSignature(t, recoverHelp, "func recover() any", 0)
}

func TestSignatureHelpPrefersLocalSymbolOverGoBuiltin(t *testing.T) {
	source := `module page
func len(value string, trim bool) int { return 0 }
func Build() { _ = len("x", true) }
`
	document := Document{URI: "file:///project/page.gox", Path: "/project/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	help := signatureAtOffset(t, server, document, strings.Index(source, "true)")+len("true"))
	assertSignature(t, help, "func len(value string, trim bool) int", 1, "value string", "trim bool")
}

func TestSignatureHelpResolvesMethodsOnInferredGoPackageValues(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	pagePath := filepath.Join(root, "page.gox")
	source := `module page
import "strings"
func Build() {
	reader := strings.NewReader("hello")
	_ = reader.Read()
}
`
	writeWorkspaceGOX(t, pagePath, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	help := signatureAtOffset(t, server, document, strings.Index(source, "reader.Read(")+len("reader.Read("))
	if help == nil || len(help.Signatures) != 1 || !strings.Contains(help.Signatures[0].Label, "Read(b []byte) (n int, err error)") {
		t.Fatalf("Read signature help = %#v", help)
	}
	if len(help.Signatures[0].Parameters) != 1 || help.Signatures[0].Parameters[0].Label != "b []byte" {
		t.Fatalf("Read parameters = %#v", help.Signatures[0].Parameters)
	}
}
