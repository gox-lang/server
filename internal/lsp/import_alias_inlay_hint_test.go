package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestImportAliasInlayHintsResolveGoxModulesAndGoPackages(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "library", "api.go"), "package api\nconst Value = 1\n")
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\nfunc User() {}\n")
	writeWorkspaceGOX(t, filepath.Join(root, "explicit.gox"), "module explicit\nfunc Value() {}\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import (
	"users.gox"
	named "explicit.gox"
	"example/library"
	other "example/library"
	_ "example/library"
	. "example/library"
)
func Build() {}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	end, err := document.Position(len(document.Text))
	if err != nil {
		t.Fatal(err)
	}
	hints := server.importAliasInlayHints(document, Range{Start: Position{}, End: end})
	if len(hints) != 2 {
		t.Fatalf("alias hints = %#v", hints)
	}
	assertImportAliasHint(t, document, hints[0], "users", strings.Index(pageSource, `"users.gox"`))
	assertImportAliasHint(t, document, hints[1], "api", strings.Index(pageSource, `"example/library"`))
	for _, hint := range hints {
		if strings.Contains(hint.Label, "named") || strings.Contains(hint.Label, "other") || hint.Label == "_" || hint.Label == "." {
			t.Fatalf("explicit alias received a hint: %#v", hint)
		}
	}

	firstEnd, err := document.Position(strings.Index(pageSource, `"users.gox"`) + len(`"users.gox"`))
	if err != nil {
		t.Fatal(err)
	}
	visible := server.importAliasInlayHints(document, Range{Start: Position{}, End: firstEnd})
	if len(visible) != 1 || visible[0].Label != "users" {
		t.Fatalf("visible alias hints = %#v", visible)
	}
}

func assertImportAliasHint(t *testing.T, document Document, hint InlayHint, label string, offset int) {
	t.Helper()
	want, err := document.Position(offset)
	if err != nil {
		t.Fatal(err)
	}
	if hint.Label != label || hint.Position != want || !hint.PaddingRight || hint.Kind != 0 {
		t.Fatalf("alias hint = %#v, want %q at %#v", hint, label, want)
	}
	if hint.Tooltip == nil || hint.Tooltip.Kind != "markdown" || !strings.Contains(hint.Tooltip.Value, "inferred alias `"+label+"`") {
		t.Fatalf("alias hint tooltip = %#v", hint.Tooltip)
	}
	if label == "users" && (!strings.Contains(hint.Tooltip.Value, "module users {") || !strings.Contains(hint.Tooltip.Value, "func User()")) {
		t.Fatalf("GOX alias tooltip = %s", hint.Tooltip.Value)
	}
	if label == "api" && (!strings.Contains(hint.Tooltip.Value, "package api {") || !strings.Contains(hint.Tooltip.Value, "const Value")) {
		t.Fatalf("Go alias tooltip = %s", hint.Tooltip.Value)
	}
}
