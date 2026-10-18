package serverruntime

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gox-lang/gox"
)

func TestWebSocketBuildErrorUsesGoxSourceDiagnostic(t *testing.T) {
	root := t.TempDir()
	route := &GoxRouteRuntime{
		RelPath:       "websocket/index.gox",
		GeneratedPath: filepath.Join(root, ".build", "websocket", "123.go"),
	}
	cause := errors.New("# command-line-arguments\nwebsocket/index.gox:27:19: too many arguments in call to socket.Disconnect\n\thave (gox.WSCloseCode, string)\n\twant (gox.WSCloseCode)")
	err := newGoxError(gox.GoxErrorBuild, route, nil, "page build failed", cause)

	if err.File != "websocket/index.gox" {
		t.Fatalf("file = %q", err.File)
	}
	if err.Line != 27 {
		t.Fatalf("line = %d, want 27", err.Line)
	}
	if err.Column != 19 {
		t.Fatalf("column = %d, want 19", err.Column)
	}
	if err.Message != "too many arguments in call to socket.Disconnect" {
		t.Fatalf("message = %q", err.Message)
	}
	if err.Kind != "build-error" {
		t.Fatalf("kind = %q", err.Kind)
	}
	console := formatGoxErrorConsole("request", err)
	for _, want := range []string{"websocket/index.gox", "Line: 27", "Column: 19", "too many arguments in call to", "socket.Disconnect"} {
		if !strings.Contains(console, want) {
			t.Fatalf("console missing %q:\n%s", want, console)
		}
	}
}

func TestBuildErrorRelativizesAbsoluteGoxDiagnostic(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "websocket", "index.gox")
	route := &GoxRouteRuntime{GeneratedPath: filepath.Join(root, ".build", "websocket", "123.go")}
	cause := errors.New(source + ":9:2: undefined: missingValue")
	err := newGoxError(gox.GoxErrorBuild, route, nil, "page build failed", cause)

	if err.File != "websocket/index.gox" || err.Line != 9 {
		t.Fatalf("location = %q:%d", err.File, err.Line)
	}
	if err.Kind != "undefined-identifier" || err.Message != "undefined: missingValue" {
		t.Fatalf("diagnostic = kind %q message %q", err.Kind, err.Message)
	}
}

func TestBuildDiagnosticOverridesEntryWithImportedGoxModule(t *testing.T) {
	root := t.TempDir()
	route := &GoxRouteRuntime{
		RelPath:       "pages/index.gox",
		GeneratedPath: filepath.Join(root, ".build", "pages", "index", "123.go"),
	}
	cause := errors.New("components/card.gox:14:7: undefined: cardTitle")
	err := newGoxError(gox.GoxErrorBuild, route, nil, "page build failed", cause)

	if err.File != "components/card.gox" || err.Line != 14 {
		t.Fatalf("location = %q:%d", err.File, err.Line)
	}
}
