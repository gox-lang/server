package gox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/goxerrors"
)

func TestClientSrcParserValidation(t *testing.T) {
	tests := []struct {
		name     string
		property string
		kind     string
		message  string
	}{
		{name: "missing", property: "", kind: "missing-client-src", message: "client declaration requires src"},
		{name: "empty", property: `src: ""`, kind: "invalid-client-src", message: "client src must be a non-empty string"},
		{name: "single quoted", property: `src: 'menu.jsx'`, kind: "invalid-client-src", message: "client src must be a non-empty string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "module main\n\nclient Menu react {\n\t" + tt.property + "\n}\n"
			_, err := ParseModule("pages/index.gox", src)
			assertClientSourceError(t, err, tt.kind, tt.message)
		})
	}
}

func TestResolveClientSrcRejectsInvalidPaths(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		prepare func(t *testing.T, root string)
		kind    string
		message string
	}{
		{name: "absolute", entry: "/tmp/menu.jsx", kind: "invalid-client-entry-path", message: "client src must be relative"},
		{name: "parent traversal", entry: "../menu.jsx", kind: "invalid-client-entry-path", message: "client src cannot escape the framework directory"},
		{name: "missing file", entry: "menu.jsx", kind: "missing-client-entry", message: "client source file does not exist"},
		{
			name:  "directory",
			entry: "menu.jsx",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "client", "react", "menu.jsx"), 0755); err != nil {
					t.Fatal(err)
				}
			},
			kind:    "invalid-client-entry-path",
			message: "client source path is a directory",
		},
		{
			name:  "wrong extension",
			entry: "menu.vue",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeClientSource(t, root, "react", "menu.vue")
			},
			kind:    "invalid-client-source-extension",
			message: `framework "react" requires a .jsx or .tsx source file`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, modulePath, src, mod := clientSourceFixture(t, "react", tt.entry)
			if tt.prepare != nil {
				tt.prepare(t, root)
			}
			err := resolveClientEntries(root, modulePath, src, mod)
			assertClientSourceError(t, err, tt.kind, tt.message)
		})
	}
}

func TestResolveClientSrcAcceptsFrameworkExtensions(t *testing.T) {
	tests := []struct {
		framework string
		extension string
	}{
		{framework: "react", extension: ".jsx"},
		{framework: "react", extension: ".tsx"},
		{framework: "preact", extension: ".jsx"},
		{framework: "preact", extension: ".tsx"},
		{framework: "solid", extension: ".jsx"},
		{framework: "solid", extension: ".tsx"},
		{framework: "vue", extension: ".vue"},
		{framework: "svelte", extension: ".svelte"},
	}
	for _, tt := range tests {
		t.Run(tt.framework+tt.extension, func(t *testing.T) {
			entry := "menu" + tt.extension
			root, modulePath, src, mod := clientSourceFixture(t, tt.framework, entry)
			writeClientSource(t, root, tt.framework, entry)
			if err := resolveClientEntries(root, modulePath, src, mod); err != nil {
				t.Fatal(err)
			}
			got := mod.Clients[0]
			wantPath := filepath.ToSlash(filepath.Join("client", tt.framework, entry))
			if got.EntryPath != wantPath {
				t.Fatalf("EntryPath = %q, want %q", got.EntryPath, wantPath)
			}
		})
	}
}

func TestResolveClientSrcRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	root, modulePath, src, mod := clientSourceFixture(t, "react", "menu.jsx")
	outside := filepath.Join(t.TempDir(), "menu.jsx")
	if err := os.WriteFile(outside, []byte("export default function Menu() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	frameworkRoot := filepath.Join(root, "client", "react")
	if err := os.MkdirAll(frameworkRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(frameworkRoot, "menu.jsx")); err != nil {
		t.Fatal(err)
	}
	err := resolveClientEntries(root, modulePath, src, mod)
	assertClientSourceError(t, err, "invalid-client-entry-path", "client src cannot escape the framework directory")
}

func TestResolveClientSrcDistinguishesAccessErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	root, modulePath, src, mod := clientSourceFixture(t, "react", "menu.jsx")
	frameworkRoot := filepath.Join(root, "client", "react")
	if err := os.MkdirAll(frameworkRoot, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(frameworkRoot, "menu.jsx")
	if err := os.Symlink("menu.jsx", source); err != nil {
		t.Fatal(err)
	}
	err := resolveClientEntries(root, modulePath, src, mod)
	assertClientSourceError(t, err, "client-source-access-error", "cannot inspect client source file")
}

func clientSourceFixture(t *testing.T, framework string, entry string) (string, string, string, *Module) {
	t.Helper()
	root := t.TempDir()
	modulePath := filepath.Join(root, "pages", "index.gox")
	src := "module main\n\nclient Menu " + framework + " {\n\tsrc: \"" + entry + "\"\n}\n"
	mod, err := ParseModule(modulePath, src)
	if err != nil {
		t.Fatal(err)
	}
	return root, modulePath, src, mod
}

func writeClientSource(t *testing.T, root string, framework string, entry string) {
	t.Helper()
	path := filepath.Join(root, "client", framework, filepath.FromSlash(entry))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("export default function Menu() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func assertClientSourceError(t *testing.T, err error, kind string, message string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", kind)
	}
	goxErr, ok := err.(*goxerrors.GoxError)
	if !ok {
		t.Fatalf("expected *goxerrors.GoxError, got %T: %v", err, err)
	}
	if goxErr.Kind != kind {
		t.Fatalf("Kind = %q, want %q: %v", goxErr.Kind, kind, err)
	}
	if !strings.Contains(goxErr.Message, message) {
		t.Fatalf("Message = %q, want it to contain %q", goxErr.Message, message)
	}
	if goxErr.File == "" || goxErr.Line == 0 || goxErr.Column == 0 {
		t.Fatalf("missing source position: %+v", goxErr)
	}
}
