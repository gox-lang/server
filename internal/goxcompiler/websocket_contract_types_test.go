package gox

import "testing"

func TestWebSocketContractTypesInferOnArgumentsAndReplyReturns(t *testing.T) {
	src := `module websocket
import "github.com/gox-lang/gox"
type Profile struct { Name string ` + "`json:\"name\"`" + `; Hidden string ` + "`json:\"-\"`" + ` }
func Index(socket *gox.Socket) gox.WebSocket {socket.On("profile",func(id string, active bool) gox.EventReply{if active{return Profile{Name:id}};return nil})}`
	mod, err := ParseModule("websocket/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	d := findWSContractType(t, collectModuleWebSocketContractTypes(mod), WebSocketContractClientEmit, "profile")
	if len(d.Arguments) != 2 || d.Arguments[0].Type != "string" || d.Arguments[1].Type != "boolean" {
		t.Fatalf("arguments = %#v", d.Arguments)
	}
	want := []string{"null", "{ name: string }"}
	if len(d.ReplyTypes) != len(want) {
		t.Fatalf("reply types = %#v", d.ReplyTypes)
	}
	for i := range want {
		if d.ReplyTypes[i] != want[i] {
			t.Fatalf("reply types = %#v, want %#v", d.ReplyTypes, want)
		}
	}
}
func TestWebSocketContractTypesInferEmitArgumentsAndAwaitReply(t *testing.T) {
	src := `module websocket
import "github.com/gox-lang/gox"
type Theme struct { Name string ` + "`json:\"name\"`" + ` }
func Index(socket *gox.Socket) gox.WebSocket {count:=7;socket.Emit("count",count,"ready");var theme Theme;socket.Emit("theme",count).Await(&theme)}`
	mod, err := ParseModule("websocket/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	types := collectModuleWebSocketContractTypes(mod)
	count := findWSContractType(t, types, WebSocketContractClientOn, "count")
	if len(count.Arguments) != 2 || count.Arguments[0].Type != "number" || count.Arguments[1].Type != "string" {
		t.Fatalf("count args = %#v", count.Arguments)
	}
	theme := findWSContractType(t, types, WebSocketContractClientOn, "theme")
	if !theme.HasReply || len(theme.ReplyTypes) != 1 || theme.ReplyTypes[0] != "{ name: string }" {
		t.Fatalf("theme = %#v", theme)
	}
}
func TestWebSocketContractTypesChannelDropsServerSocketAndKeepsTypes(t *testing.T) {
	src := `module chat
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel {channel.On("message",func(socket *gox.Socket,text string,ids []int) gox.EventReply{return true})}`
	mod, err := ParseModule("websocket/chat/chat.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	d := findWSContractType(t, collectModuleWebSocketContractTypes(mod), WebSocketContractClientEmit, "message")
	if d.Scope != WebSocketContractChannel || d.Channel != "Chat" || len(d.Arguments) != 2 || d.Arguments[0].Type != "string" || d.Arguments[1].Type != "Array<number>" || len(d.ReplyTypes) != 1 || d.ReplyTypes[0] != "boolean" {
		t.Fatalf("channel = %#v", d)
	}
}
func findWSContractType(t *testing.T, all []webSocketContractType, direction WebSocketContractDirection, event string) webSocketContractType {
	t.Helper()
	for _, d := range all {
		if d.Direction == direction && d.Event == event {
			return d
		}
	}
	t.Fatalf("typed contract %s %q not found: %#v", direction, event, all)
	return webSocketContractType{}
}
