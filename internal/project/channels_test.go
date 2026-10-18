package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChannelsDiscoverDefinitionsAndIgnorePrivateModules(t *testing.T) {
	root := t.TempDir()
	files := []string{
		"websocket/index.gox",
		"websocket/chat/chat.gox",
		"websocket/chat/moderation.gox",
		"websocket/chat/.validate.gox",
		"websocket/chat/chat.helper.gox",
		"websocket/finances/finances.gox",
	}
	for _, name := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("module test\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	project := &Project{RootDir: root}
	entries, err := project.Channels()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("channels = %#v", entries)
	}
	want := []ChannelEntry{
		{Name: "Chat", File: "websocket/chat/chat.gox", Function: "Chat"},
		{Name: "Chat/Moderation", File: "websocket/chat/moderation.gox", Function: "Moderation"},
		{Name: "Finances", File: "websocket/finances/finances.gox", Function: "Finances"},
	}
	for index := range want {
		if entries[index] != want[index] {
			t.Fatalf("channel %d = %#v, want %#v", index, entries[index], want[index])
		}
	}
}

func TestChannelsRejectCanonicalDuplicates(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"websocket/chat.gox", "websocket/chat/chat.gox"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("module test\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := (&Project{RootDir: root}).Channels()
	if err == nil || !strings.Contains(err.Error(), "duplicate channel Chat") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestChannelsUseSharedWebSocketFilesystemRules(t *testing.T) {
	root := t.TempDir()
	files := []string{
		"websocket/index.gox",
		"websocket/chat/chat.gox",
		"websocket/chat/index.gox",
		"websocket/chat/moderation.gox",
		"websocket/chat/chat.helper.gox",
		"websocket/chat/.index.gox",
		"websocket/chat/@shared.gox",
		"websocket/@shared/auth.gox",
	}
	for _, name := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("module test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := (&Project{RootDir: root}).Channels()
	if err != nil {
		t.Fatal(err)
	}
	want := []ChannelEntry{
		{Name: "Chat", File: "websocket/chat/chat.gox", Function: "Chat"},
		{Name: "Chat/Index", File: "websocket/chat/index.gox", Function: "Index"},
		{Name: "Chat/Moderation", File: "websocket/chat/moderation.gox", Function: "Moderation"},
	}
	if len(entries) != len(want) {
		t.Fatalf("channels = %#v, want %#v", entries, want)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("channel %d = %#v, want %#v", i, entries[i], want[i])
		}
	}
}
