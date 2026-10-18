package gox

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gox-lang/server/internal/goxerrors"
)

func TestClientTopLevelNameConflicts(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "var",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
var Menu string
`,
		},
		{
			name: "const",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
const Menu = "value"
`,
		},
		{
			name: "type",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
type Menu struct{}
`,
		},
		{
			name: "function",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
func Menu() {}
`,
		},
		{
			name: "grouped var",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
var (
	Header string
	Menu string
)
`,
		},
		{
			name: "multiple names",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
var Header, Menu string
`,
		},
		{
			name: "import alias",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
import Menu "some/package"
`,
		},
		{
			name: "gox import alias",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
import Menu "components/menu.gox"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseModule("pages/index.gox", tt.source)
			assertClientSemanticError(t, err, "client-name-conflict")
		})
	}
}

func TestClientDuplicateNameReportsBothPositions(t *testing.T) {
	source := `module main
client Menu react {
	src: "menu.jsx"
}

client Menu vue {
	src: "menu.vue"
}
`

	_, err := ParseModule("pages/index.gox", source)
	compileErr := assertClientSemanticError(t, err, "duplicate-client-component")
	if compileErr.Line != 6 || compileErr.Column != 8 {
		t.Fatalf("unexpected redeclaration position: %d:%d", compileErr.Line, compileErr.Column)
	}
	want := `client component "Menu" redeclared; previous declaration at pages/index.gox:2:8`
	if compileErr.Message != want {
		t.Fatalf("unexpected message:\nwant: %s\n got: %s", want, compileErr.Message)
	}
}

func TestClientConflictReportsIdentifierAndClientPositions(t *testing.T) {
	source := `module main
client Menu react {
	src: "menu.jsx"
}

var Menu string
`

	_, err := ParseModule("pages/index.gox", source)
	compileErr := assertClientSemanticError(t, err, "client-name-conflict")
	if compileErr.Line != 6 || compileErr.Column != 5 {
		t.Fatalf("unexpected conflict position: %d:%d", compileErr.Line, compileErr.Column)
	}
	want := `identifier "Menu" conflicts with client component declared at pages/index.gox:2:8`
	if compileErr.Message != want {
		t.Fatalf("unexpected message:\nwant: %s\n got: %s", want, compileErr.Message)
	}
}

func TestClientAllowsFunctionScopeNames(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "local variable",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
func GET() {
	Menu := "local"
	_ = Menu
}
`,
		},
		{
			name: "parameter",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
func GET(Menu string) {}
`,
		},
		{
			name: "named result",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
func GET() (Menu string) {
	return ""
}
`,
		},
		{
			name: "receiver and method",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
type Page struct{}
func (Menu *Page) Menu() {}
`,
		},
		{
			name: "struct field",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
type Page struct {
	Menu string
}
`,
		},
		{
			name: "nested blocks",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
func GET() {
	if Menu := true; Menu {
		for Menu := 0; Menu < 1; Menu++ {
			switch Menu := Menu; Menu {
			case 0:
			}
		}
	}
}
`,
		},
		{
			name: "ignored import aliases",
			source: `module main
client Menu react {
	src: "menu.jsx"
}
import _ "some/package"
import . "other/package"
`,
		},
		{
			name: "unaliased import",
			source: `module main
client some react {
	src: "menu.jsx"
}
import "example.com/some"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseModule("pages/index.gox", tt.source); err != nil {
				t.Fatalf("expected declaration to be allowed, got %v", err)
			}
		})
	}
}

func TestClientNameCanBeReusedByDifferentModules(t *testing.T) {
	source := `module %s
client Menu react {
	src: "menu.jsx"
}
`

	if _, err := ParseModule("pages/home.gox", fmt.Sprintf(source, "home")); err != nil {
		t.Fatalf("home module: %v", err)
	}
	if _, err := ParseModule("pages/dashboard.gox", fmt.Sprintf(source, "dashboard")); err != nil {
		t.Fatalf("dashboard module: %v", err)
	}
}

func assertClientSemanticError(t *testing.T, err error, kind string) *goxerrors.GoxError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", kind)
	}
	var compileErr *goxerrors.GoxError
	if !errors.As(err, &compileErr) {
		t.Fatalf("expected *goxerrors.GoxError, got %T: %v", err, err)
	}
	if compileErr.Kind != kind {
		t.Fatalf("expected kind %q, got %q: %v", kind, compileErr.Kind, err)
	}
	return compileErr
}
