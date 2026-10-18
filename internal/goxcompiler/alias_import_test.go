package gox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGraphResolvesAliasWithAncestralShadowing(t *testing.T) {
	root := t.TempDir()
	writeAliasTestFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	entry := filepath.Join(root, "pages", "dashboard", "reports", "index.gox")
	local := filepath.Join(root, "pages", "dashboard", "@components", "menu.gox")
	writeAliasTestFile(t, entry, "module page\n\nimport Menu \"@components/menu.gox\"\n")
	writeAliasTestFile(t, local, "module Menu\n")
	writeAliasTestFile(t, filepath.Join(root, "pages", "@components", "menu.gox"), "module RootMenu\n")

	graph, err := LoadGraph(entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Entry.GoxImports) != 1 {
		t.Fatalf("imports = %#v", graph.Entry.GoxImports)
	}
	imp := graph.Entry.GoxImports[0]
	if imp.ResolvedPath != local {
		t.Fatalf("resolved = %q, want %q", imp.ResolvedPath, local)
	}
	if len(imp.ResolutionCandidates) != 2 {
		t.Fatalf("candidates = %#v", imp.ResolutionCandidates)
	}
	if len(graph.Modules) != 2 {
		t.Fatalf("modules = %d", len(graph.Modules))
	}
}

func TestLoadGraphAliasRequiresExplicitExtension(t *testing.T) {
	root := t.TempDir()
	writeAliasTestFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	entry := filepath.Join(root, "pages", "index.gox")
	writeAliasTestFile(t, entry, "module page\n\nimport Layout \"@layout\"\n")
	writeAliasTestFile(t, filepath.Join(root, "pages", "@layout.gox"), "module Layout\n")
	if _, err := LoadGraph(entry); err == nil {
		t.Fatal("expected missing .gox extension to fail")
	}
}

func writeAliasTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadGraphResolvesWebSocketAliasWithinWebSocketTree(t *testing.T) {
	root := t.TempDir()
	writeAliasTestFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	entry := filepath.Join(root, "websocket", "chat", "rooms", "room.gox")
	local := filepath.Join(root, "websocket", "chat", "@shared", "events.gox")
	writeAliasTestFile(t, entry, `module room

import Events "@shared/events.gox"
import "github.com/gox-lang/gox"

func Room(channel *gox.Channel) gox.Channel {}
`)
	writeAliasTestFile(t, local, "module Events\n")
	writeAliasTestFile(t, filepath.Join(root, "pages", "@shared", "events.gox"), "module PageEvents\n")

	graph, err := LoadGraph(entry)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Entry.GoxImports) != 1 {
		t.Fatalf("imports = %#v", graph.Entry.GoxImports)
	}
	if got := graph.Entry.GoxImports[0].ResolvedPath; got != local {
		t.Fatalf("resolved = %q, want %q", got, local)
	}
}

func TestLoadGraphAssignsSharedModuleStructure(t *testing.T) {
	root := t.TempDir()
	writeAliasTestFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	entry := filepath.Join(root, "websocket", "chat", "index.gox")
	writeAliasTestFile(t, entry, `module chat

import "github.com/gox-lang/gox"

func Index(channel *gox.Channel) gox.Channel {}
`)
	graph, err := LoadGraph(entry)
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Entry.Structure.ChannelName; got != "Chat/Index" {
		t.Fatalf("channel structure = %#v", graph.Entry.Structure)
	}
}
