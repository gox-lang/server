package lsp

import (
	"strings"
	"testing"
)

func TestSemanticDiagnosticsValidateReturnCounts(t *testing.T) {
	source := `module page
func Pair() (string, bool) { return "George", true }
func Name() string { return "George" }
func Missing() string { return }
func Extra() { return "George" }
func TooFew() (string, bool) { return "George" }
func TooMany() string { return "George", true }
func FromCall() string { return Pair() }
func SplitCall() (string, bool) { return Name() }
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "return-count", "return value mismatch: have 0 values, want 1 value", indexAfter(source, "return }", "func Missing"))
	assertDiagnosticAt(t, document, diagnostics, "return-count", "return value mismatch: have 1 value, want 0 values", indexAfter(source, "return \"George\"", "func Extra"))
	assertDiagnosticAt(t, document, diagnostics, "return-count", "return value mismatch: have 1 value, want 2 values", indexAfter(source, "return \"George\" }", "func TooFew"))
	assertDiagnosticAt(t, document, diagnostics, "return-count", "return value mismatch: have 2 values, want 1 value", indexAfter(source, "return \"George\", true", "func TooMany"))
	assertDiagnosticAt(t, document, diagnostics, "return-count", "return value mismatch: have 2 values, want 1 value", strings.Index(source, "return Pair()"))
	assertDiagnosticAt(t, document, diagnostics, "return-count", "return value mismatch: have 1 value, want 2 values", strings.Index(source, "return Name()"))
}

func indexAfter(source, needle, marker string) int {
	start := strings.Index(source, marker)
	if start < 0 {
		return -1
	}
	relative := strings.Index(source[start:], needle)
	if relative < 0 {
		return -1
	}
	return start + relative
}

func TestSemanticDiagnosticsValidateReturnTypesAndAllowNamedReturns(t *testing.T) {
	source := `module page
func Named() (name string, ok bool) {
	name = "George"
	ok = true
	return
}
func Wrong() (string, bool) {
	return 42, "true"
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "return-type-mismatch", "cannot use 42 (type untyped int) as string in return value 1", strings.Index(source, "42"))
	assertDiagnosticAt(t, document, diagnostics, "return-type-mismatch", "cannot use \"true\" (type untyped string) as bool in return value 2", strings.Index(source, "\"true\""))
}

func TestSemanticDiagnosticsLeaveGoxTemplateReturnsToCompiler(t *testing.T) {
	source := `module page
import "github.com/gox-lang/gox"
func Card() gox.Component {
	return <div>{{ "George" }}</div>
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "return-count" || diagnostic.Code == "return-type-mismatch" {
			t.Fatalf("template return received Go return diagnostic: %#v", diagnostic)
		}
	}
}

func TestSemanticDiagnosticsValidateAnonymousFunctionReturns(t *testing.T) {
	source := `module page
func Test() {
	voidFn := func(name string, age int) {
		return "hola mundo"
	}
	missing := func() string {
		return
	}
	wrong := func() string {
		return 42
	}
	_ = voidFn
	_ = missing
	_ = wrong
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	assertDiagnosticAt(t, document, diagnostics, "return-count", "return value mismatch: have 1 value, want 0 values", strings.Index(source, `return "hola mundo"`))
	assertDiagnosticAt(t, document, diagnostics, "return-count", "return value mismatch: have 0 values, want 1 value", indexAfter(source, "return", "missing :="))
	assertDiagnosticAt(t, document, diagnostics, "return-type-mismatch", "cannot use 42 (type untyped int) as string in return value 1", strings.Index(source, "42"))
}

func TestSemanticDiagnosticsAnonymousFunctionUsesOwnReturnSignature(t *testing.T) {
	source := `module page
func Outer() string {
	inner := func() int {
		return 42
	}
	_ = inner
	return "ok"
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "return-count" || diagnostic.Code == "return-type-mismatch" {
			t.Fatalf("valid nested function return received diagnostic: %#v", diagnostic)
		}
	}
}

func TestSemanticDiagnosticsReportMissingReturnAtFunctionEnd(t *testing.T) {
	source := `module page
func Empty() string {
}
func Partial(ok bool) string {
	if ok {
		return "ok"
	}
}
func Anonymous() {
	fn := func() string {
	}
	_ = fn
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	diagnostics := server.diagnostics(document)
	if got := countDiagnostics(diagnostics, "missing-return", "missing return"); got != 3 {
		t.Fatalf("missing-return diagnostics = %d, want 3: %#v", got, diagnostics)
	}
}

func TestSemanticDiagnosticsAcceptTerminatingFunctionBodies(t *testing.T) {
	source := `module page
func IfElse(ok bool) string {
	if ok {
		return "yes"
	} else {
		return "no"
	}
}
func Infinite() string {
	for {
	}
}
func Panics() string {
	panic("stop")
}
func Switch(value int) string {
	switch value {
	case 1:
		return "one"
	case 2:
		fallthrough
	default:
		return "other"
	}
}
func Anonymous() {
	fn := func(ok bool) string {
		if ok {
			return "yes"
		}
		return "no"
	}
	_ = fn
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "missing-return" {
			t.Fatalf("valid terminating function received missing-return diagnostic: %#v", diagnostic)
		}
	}
}

func countDiagnostics(diagnostics []Diagnostic, code, message string) int {
	count := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Message == message {
			count++
		}
	}
	return count
}

func TestSemanticDiagnosticsRespectGoxSemanticReturns(t *testing.T) {
	source := `module page
import "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page { return "hello" }
func Login() gox.Action { return true }
func Card() gox.Component { return "card" }
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "return-type-mismatch" {
			t.Fatalf("GOX semantic return received Go type diagnostic: %#v", diagnostic)
		}
	}
}

func TestSemanticDiagnosticsAllowWebSocketWithoutReturn(t *testing.T) {
	source := `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("ready", func() {})
}
`
	document := Document{URI: "file:///workspace/websocket/index.gox", Path: "/workspace/websocket/index.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "missing-return" {
			t.Fatalf("WebSocket entrypoint received missing-return diagnostic: %#v", diagnostic)
		}
	}
}

func TestSemanticDiagnosticsAllowChannelWithoutReturn(t *testing.T) {
	source := `module room
import "github.com/gox-lang/gox"
func Room(channel *gox.Channel) gox.Channel {
	channel.On("message", func(socket *gox.Socket) {})
}
`
	document := Document{URI: "file:///workspace/websocket/room.gox", Path: "/workspace/websocket/room.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "missing-return" {
			t.Fatalf("Channel entrypoint received missing-return diagnostic: %#v", diagnostic)
		}
	}
}

func TestSemanticDiagnosticsRejectValuesReturnedByWebSocketAndChannel(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		source string
	}{
		{
			name: "websocket",
			path: "/workspace/websocket/index.gox",
			source: `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket { return true }
`,
		},
		{
			name: "channel",
			path: "/workspace/websocket/room.gox",
			source: `module room
import "github.com/gox-lang/gox"
func Room(channel *gox.Channel) gox.Channel { return true }
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			document := Document{URI: FileURI(tc.path), Path: tc.path, Text: tc.source}
			server := NewServer()
			if err := server.symbols.UpdateOpen(document); err != nil {
				t.Fatal(err)
			}
			diagnostics := server.diagnostics(document)
			if got := countDiagnostics(diagnostics, "return-count", "return value mismatch: have 1 value, want 0 values"); got != 1 {
				t.Fatalf("return-count diagnostics = %d, want 1: %#v", got, diagnostics)
			}
		})
	}
}

func TestSemanticDiagnosticsAllowPageAndActionWithoutReturn(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{
			name: "page empty body",
			source: `module page
import "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page {
}
`,
		},
		{
			name: "page bare return",
			source: `module page
import "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page {
	return
}
`,
		},
		{
			name: "action empty body",
			source: `module page
import "github.com/gox-lang/gox"
func Save(ctx *gox.Context) gox.Action {
}
`,
		},
		{
			name: "action bare return",
			source: `module page
import "github.com/gox-lang/gox"
func Save(ctx *gox.Context) gox.Action {
	return
}
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: tc.source}
			server := NewServer()
			if err := server.symbols.UpdateOpen(document); err != nil {
				t.Fatal(err)
			}
			for _, diagnostic := range server.diagnostics(document) {
				if diagnostic.Code == "missing-return" || diagnostic.Code == "return-count" {
					t.Fatalf("GOX semantic function without a response body received return diagnostic: %#v", diagnostic)
				}
			}
		})
	}
}
