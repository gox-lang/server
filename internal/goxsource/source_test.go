package goxsource

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsPublicPage(t *testing.T) {
	tests := map[string]bool{
		"pages/index.gox": true, "pages/dashboard/dashboard.gox": true,
		"pages/user.private.gox": false, "pages/@layout.gox": false,
		"pages/@components/menu.gox": false, "pages/dashboard/@components/menu.gox": false,
	}
	for name, want := range tests {
		if got := IsPublicPage(name); got != want {
			t.Errorf("IsPublicPage(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestResolveAliasUsesAncestralShadowingAndFallback(t *testing.T) {
	root := t.TempDir()
	pages := filepath.Join(root, "pages")
	entry := filepath.Join(pages, "dashboard", "reports", "index.gox")
	local := filepath.Join(pages, "dashboard", "@components", "menu.gox")
	parent := filepath.Join(pages, "@components", "header.gox")
	for _, file := range []string{entry, local, parent} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("module test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ResolveAlias(pages, entry, "@components/menu.gox")
	if err != nil || got.ResolvedPath != local {
		t.Fatalf("local resolution = %#v, %v", got, err)
	}
	got, err = ResolveAlias(pages, entry, "@components/header.gox")
	if err != nil || got.ResolvedPath != parent {
		t.Fatalf("fallback resolution = %#v, %v", got, err)
	}
	if len(got.Candidates) != 3 {
		t.Fatalf("candidates = %#v", got.Candidates)
	}
}

func TestResolveAliasNeverUsesSiblingOrImplicitIndex(t *testing.T) {
	root := t.TempDir()
	pages := filepath.Join(root, "pages")
	entry := filepath.Join(pages, "shop", "index.gox")
	files := []string{entry, filepath.Join(pages, "account", "@layout.gox"), filepath.Join(pages, "@thing", "index.gox")}
	for _, file := range files {
		_ = os.MkdirAll(filepath.Dir(file), 0o755)
		_ = os.WriteFile(file, nil, 0o644)
	}
	if _, err := ResolveAlias(pages, entry, "@layout.gox"); err == nil {
		t.Fatal("sibling alias unexpectedly resolved")
	}
	if _, err := ResolveAlias(pages, entry, "@thing.gox"); err == nil {
		t.Fatal("index.gox unexpectedly resolved implicitly")
	}
}

func TestResolveAliasRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	pages := filepath.Join(root, "pages")
	entry := filepath.Join(pages, "index.gox")
	outside := filepath.Join(root, "outside.gox")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(pages, "@escape.gox")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ResolveAlias(pages, entry, "@escape.gox"); err == nil {
		t.Fatal("expected symlink escape to fail")
	}
}

func TestClassifyModuleContexts(t *testing.T) {
	tests := []struct {
		path     string
		kind     ModuleKind
		channel  string
		function string
	}{
		{"pages/index.gox", ModulePublicPage, "", ""},
		{"pages/shop/cart.gox", ModulePublicPage, "", ""},
		{"pages/shop/cart.helper.gox", ModulePrivatePage, "", ""},
		{"pages/@shared/menu.gox", ModulePageAlias, "", ""},
		{"websocket/index.gox", ModuleGlobalWebSocket, "", ""},
		{"websocket/chat.gox", ModulePublicChannel, "Chat", "Chat"},
		{"websocket/chat/chat.gox", ModulePublicChannel, "Chat", "Chat"},
		{"websocket/chat/index.gox", ModulePublicChannel, "Chat/Index", "Index"},
		{"websocket/chat/moderation.gox", ModulePublicChannel, "Chat/Moderation", "Moderation"},
		{"websocket/game/room/private.gox", ModulePublicChannel, "Game/Room/Private", "Private"},
		{"websocket/chat/chat.helper.gox", ModulePrivateWebSocket, "", ""},
		{"websocket/chat/.index.gox", ModulePrivateWebSocket, "", ""},
		{"websocket/@shared/auth.gox", ModuleWebSocketAlias, "", ""},
		{"websocket/chat/@shared/auth.gox", ModuleWebSocketAlias, "", ""},
		{"app/config.gox", ModuleOther, "", ""},
	}
	for _, tt := range tests {
		got := ClassifyModule(tt.path)
		if got.Kind != tt.kind || got.ChannelName != tt.channel || got.ChannelFunction != tt.function {
			t.Errorf("ClassifyModule(%q) = %#v, want kind=%q channel=%q function=%q", tt.path, got, tt.kind, tt.channel, tt.function)
		}
	}
}

func TestResolveAliasWorksWithWebSocketSourceRoot(t *testing.T) {
	root := t.TempDir()
	websocket := filepath.Join(root, "websocket")
	entry := filepath.Join(websocket, "chat", "rooms", "room.gox")
	local := filepath.Join(websocket, "chat", "@shared", "auth.gox")
	parent := filepath.Join(websocket, "@shared", "events.gox")
	for _, file := range []string{entry, local, parent} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("module test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ResolveAlias(websocket, entry, "@shared/auth.gox")
	if err != nil || got.ResolvedPath != local {
		t.Fatalf("local resolution = %#v, %v", got, err)
	}
	got, err = ResolveAlias(websocket, entry, "@shared/events.gox")
	if err != nil || got.ResolvedPath != parent {
		t.Fatalf("fallback resolution = %#v, %v", got, err)
	}
}

func TestAliasRootSeparatesPagesAndWebSocketTrees(t *testing.T) {
	root := t.TempDir()
	pages, err := AliasRoot(root, filepath.Join(root, "pages", "shop", "index.gox"))
	if err != nil || pages != filepath.Join(root, "pages") {
		t.Fatalf("pages root = %q, %v", pages, err)
	}
	websocket, err := AliasRoot(root, filepath.Join(root, "websocket", "chat", "chat.gox"))
	if err != nil || websocket != filepath.Join(root, "websocket") {
		t.Fatalf("websocket root = %q, %v", websocket, err)
	}
	if _, err := AliasRoot(root, filepath.Join(root, "app", "config.gox")); err == nil {
		t.Fatal("expected alias outside source trees to fail")
	}
}

func TestClassifyModuleAbsolutePaths(t *testing.T) {
	cases := []struct {
		path string
		kind ModuleKind
	}{
		{"/workspace/demo/pages/index.gox", ModulePublicPage},
		{"/workspace/demo/websocket/index.gox", ModuleGlobalWebSocket},
		{"/workspace/demo/websocket/chat/index.gox", ModulePublicChannel},
	}
	for _, tt := range cases {
		if got := ClassifyModule(tt.path); got.Kind != tt.kind {
			t.Fatalf("ClassifyModule(%q) = %#v, want %q", tt.path, got, tt.kind)
		}
	}
}
