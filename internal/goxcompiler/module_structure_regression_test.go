package gox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModuleStructureProjectTreeRegression(t *testing.T) {
	root := t.TempDir()
	writeStructureRegressionFile(t, filepath.Join(root, "go.mod"), "module example.com/structure\n\ngo 1.24\n")

	writeStructureRegressionFile(t, filepath.Join(root, "pages", "@shared", "socket.gox"), `module SocketShared

func Label() string { return "shared" }
`)
	page := filepath.Join(root, "pages", "index.gox")
	writeStructureRegressionFile(t, page, `module index

import Shared "@shared/socket.gox"
import "github.com/gox-lang/gox"

func Index(ctx *gox.Context) gox.Page {
    target, found := gox.Sockets.GetByID("socket-1")
    if found {
        target.Emit("profile", Shared.Label())
    }
    return "ok"
}

func Login(value string) gox.Action { return value }
`)
	pageGraph, err := LoadGraph(page)
	if err != nil {
		t.Fatalf("valid pages tree rejected: %v", err)
	}
	if len(pageGraph.Modules) != 2 {
		t.Fatalf("pages graph modules = %d, want 2", len(pageGraph.Modules))
	}

	writeStructureRegressionFile(t, filepath.Join(root, "websocket", "@shared", "events.gox"), `module Events

func Name() string { return "message" }
`)
	socket := filepath.Join(root, "websocket", "index.gox")
	writeStructureRegressionFile(t, socket, `module websocket

import Events "@shared/events.gox"
import "github.com/gox-lang/gox"

func Index(socket *gox.Socket) gox.WebSocket {
    socket.On(Events.Name(), func(value string) {})
}
`)
	socketGraph, err := LoadGraph(socket)
	if err != nil {
		t.Fatalf("valid websocket tree rejected: %v", err)
	}
	if len(socketGraph.Modules) != 2 {
		t.Fatalf("websocket graph modules = %d, want 2", len(socketGraph.Modules))
	}

	channel := filepath.Join(root, "websocket", "chat", "moderation.gox")
	writeStructureRegressionFile(t, channel, `module moderation

import "github.com/gox-lang/gox"

func Moderation(channel *gox.Channel) gox.Channel {
    channel.On("message", func(socket *gox.Socket, message string) {})
}
`)
	channelGraph, err := LoadGraph(channel)
	if err != nil {
		t.Fatalf("valid Channel rejected: %v", err)
	}
	if got := channelGraph.Entry.Structure.ChannelName; got != "Chat/Moderation" {
		t.Fatalf("Channel identity = %q, want Chat/Moderation", got)
	}
}

func TestModuleStructureTreesDoNotCrossResolveAliases(t *testing.T) {
	root := t.TempDir()
	writeStructureRegressionFile(t, filepath.Join(root, "go.mod"), "module example.com/structure\n\ngo 1.24\n")
	writeStructureRegressionFile(t, filepath.Join(root, "pages", "@shared", "helper.gox"), "module PageHelper\n")

	entry := filepath.Join(root, "websocket", "chat", "chat.gox")
	writeStructureRegressionFile(t, entry, `module chat

import Helper "@shared/helper.gox"
import "github.com/gox-lang/gox"

func Chat(channel *gox.Channel) gox.Channel {}
`)
	if _, err := LoadGraph(entry); err == nil {
		t.Fatal("websocket tree resolved an alias from pages tree")
	}
}

func writeStructureRegressionFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
