package gox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebSocketTreeRejectsPageDeclarations(t *testing.T) {
	cases := []string{
		"websocket/index.gox",
		"websocket/chat.gox",
		"websocket/private.helper.gox",
		"websocket/@shared.gox",
	}
	for _, rel := range cases {
		t.Run(rel, func(t *testing.T) {
			src := "module test\n\nimport \"github.com/gox-lang/gox\"\n\nfunc Bad(ctx *gox.Context) gox.Page { return nil }\n"
			_, err := ParseModule(filepath.Join(t.TempDir(), rel), src)
			if err == nil || !strings.Contains(err.Error(), "declares Page entrypoint Bad inside websocket/") {
				t.Fatalf("expected websocket-tree Page rejection, got %v", err)
			}
		})
	}
}

func TestWebSocketTreeRejectsActionDeclarations(t *testing.T) {
	cases := []string{
		"websocket/index.gox",
		"websocket/chat.gox",
		"websocket/private.helper.gox",
		"websocket/@shared.gox",
	}
	for _, rel := range cases {
		t.Run(rel, func(t *testing.T) {
			src := "module test\n\nimport \"github.com/gox-lang/gox\"\n\nfunc Bad(value string) gox.Action { return value }\n"
			_, err := ParseModule(filepath.Join(t.TempDir(), rel), src)
			if err == nil || !strings.Contains(err.Error(), "declares Action Bad inside websocket/") {
				t.Fatalf("expected websocket-tree Action rejection, got %v", err)
			}
		})
	}
}

func TestPageMayUseSocketAPIsWithoutDeclaringWebSocket(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pages := filepath.Join(root, "pages")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `module index

import "github.com/gox-lang/gox"

func Index(ctx *gox.Context) gox.Page {
    target, found := gox.Sockets.GetByID("socket-1")
    if found {
        target.Emit("profile", 42)
    }
    return "ok"
}
`
	if err := os.WriteFile(filepath.Join(pages, "index.gox"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGraph(filepath.Join(pages, "index.gox")); err != nil {
		t.Fatalf("Page using socket APIs must remain valid: %v", err)
	}
}
