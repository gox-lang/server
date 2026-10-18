package gox

import (
	"go/ast"
	"strings"
	"testing"
)

func TestWebSocketContractsCollectSocketOnEmitAndAwait(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
type Theme struct { Name string }
func Index(socket *gox.Socket) gox.WebSocket {
 socket.On("login", func(user string, age int) gox.EventReply { return true })
 socket.Emit("registered", user, 7)
 var theme Theme
 socket.Emit("theme", user).Await(&theme)
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 3 {
		t.Fatalf("contracts = %d, want 3", len(mod.WebSocketContracts))
	}

	on := findWSContract(t, mod, WebSocketContractClientEmit, "login")
	if on.Scope != WebSocketContractSocket || !on.HasReply || len(on.Arguments) != 2 {
		t.Fatalf("login contract = %#v", on)
	}
	if on.Arguments[0].Name != "user" || on.Arguments[1].Name != "age" {
		t.Fatalf("login args = %#v", on.Arguments)
	}

	emit := findWSContract(t, mod, WebSocketContractClientOn, "registered")
	if emit.HasReply || len(emit.Arguments) != 2 {
		t.Fatalf("registered contract = %#v", emit)
	}

	await := findWSContract(t, mod, WebSocketContractClientOn, "theme")
	if !await.HasReply || await.ReplyTarget == nil || len(await.Arguments) != 1 {
		t.Fatalf("theme contract = %#v", await)
	}
	star, ok := await.ReplyTarget.(*ast.UnaryExpr)
	if !ok || star.Op.String() != "&" {
		t.Fatalf("reply target = %T, want &target", await.ReplyTarget)
	}
}

func TestWebSocketContractsSeparateChannelAndStripSocketContext(t *testing.T) {
	mod, err := ParseModule("websocket/chat/chat.gox", `module chat
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel {
 channel.On("message", func(socket *gox.Socket, message string, count int) gox.EventReply { return true })
 channel.Emit("message", "hello")
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 2 {
		t.Fatalf("contracts = %d, want 2", len(mod.WebSocketContracts))
	}
	on := findWSContract(t, mod, WebSocketContractClientEmit, "message")
	if on.Scope != WebSocketContractChannel || on.Channel != "Chat" {
		t.Fatalf("channel identity = %#v", on)
	}
	if len(on.Arguments) != 2 || on.Arguments[0].Name != "message" || on.Arguments[1].Name != "count" {
		t.Fatalf("client args = %#v", on.Arguments)
	}
	if !on.HasReply {
		t.Fatal("channel On EventReply was not recorded")
	}
	emit := findWSContract(t, mod, WebSocketContractClientOn, "message")
	if emit.Scope != WebSocketContractChannel || emit.Channel != "Chat" || emit.HasReply {
		t.Fatalf("channel emit = %#v", emit)
	}
}

func TestWebSocketContractsSupportNamedOnHandler(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func login(user string, password string) gox.EventReply { return true }
func Index(socket *gox.Socket) gox.WebSocket { socket.On("login", login) }`)
	if err != nil {
		t.Fatal(err)
	}
	contract := findWSContract(t, mod, WebSocketContractClientEmit, "login")
	if len(contract.Arguments) != 2 || contract.Arguments[0].Name != "user" || contract.Arguments[1].Name != "password" || !contract.HasReply {
		t.Fatalf("contract = %#v", contract)
	}
}

func TestWebSocketContractsIgnoreDynamicEventNames(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
 event := "dynamic"
 socket.On(event, func(value string) {})
 socket.Emit(event, value)
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 0 {
		t.Fatalf("contracts = %#v, want none for non-literal event names", mod.WebSocketContracts)
	}
}

func findWSContract(t *testing.T, mod *Module, direction WebSocketContractDirection, event string) WebSocketContract {
	t.Helper()
	for _, contract := range mod.WebSocketContracts {
		if contract.Direction == direction && contract.Event == event {
			return contract
		}
	}
	t.Fatalf("contract %s %q not found: %#v", direction, event, mod.WebSocketContracts)
	return WebSocketContract{}
}

func TestWebSocketContractsCollectGlobalAndSelectedSocketEmitsOutsideWebSocketEntrypoint(t *testing.T) {
	mod, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Notify() gox.Action {
 gox.Sockets.Emit("broadcast", "ready")
 target := gox.Sockets.GetByID("abc")
 target.Emit("direct", 42)
 var accepted bool
 target.Emit("confirm", "value").Await(&accepted)
 return true
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 3 {
		t.Fatalf("contracts = %d, want 3: %#v", len(mod.WebSocketContracts), mod.WebSocketContracts)
	}
	if c := findWSContract(t, mod, WebSocketContractClientOn, "broadcast"); c.Scope != WebSocketContractSocket || c.HasReply {
		t.Fatalf("broadcast = %#v", c)
	}
	if c := findWSContract(t, mod, WebSocketContractClientOn, "direct"); c.Scope != WebSocketContractSocket || c.HasReply {
		t.Fatalf("direct = %#v", c)
	}
	if c := findWSContract(t, mod, WebSocketContractClientOn, "confirm"); !c.HasReply || c.ReplyTarget == nil {
		t.Fatalf("confirm = %#v", c)
	}
}

func TestWebSocketContractsSocketOnRequiresGlobalStructuralAuthority(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
 socket.On("authorized", func(value string) {})
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 1 || mod.WebSocketContracts[0].Event != "authorized" {
		t.Fatalf("initial contracts = %#v", mod.WebSocketContracts)
	}

	// Contract collection must enforce the structural authority itself instead
	// of depending only on validation having run before it.
	mod.Structure.Kind = "other"
	if contracts := collectWebSocketContracts(mod); len(contracts) != 0 {
		t.Fatalf("contracts without global WebSocket authority = %#v", contracts)
	}
}

func TestWebSocketContractsIgnoreSocketOnOutsideGlobalEntrypointFunction(t *testing.T) {
	mod, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func helper(socket *gox.Socket) {
 socket.On("helper", func(value string) {})
}
func Index(socket *gox.Socket) gox.WebSocket {
 socket.On("entrypoint", func(value string) {})
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 1 || mod.WebSocketContracts[0].Event != "entrypoint" {
		t.Fatalf("contracts = %#v", mod.WebSocketContracts)
	}
}

func TestWebSocketContractsChannelRequiresPublicStructuralAuthority(t *testing.T) {
	mod, err := ParseModule("websocket/chat/chat.gox", `module chat
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel {
 channel.On("message", func(socket *gox.Socket, message string) {})
 channel.Emit("message", "hello")
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 2 {
		t.Fatalf("initial contracts = %#v", mod.WebSocketContracts)
	}

	mod.Structure.Kind = "other"
	if contracts := collectWebSocketContracts(mod); len(contracts) != 0 {
		t.Fatalf("contracts without public Channel authority = %#v", contracts)
	}
}

func TestWebSocketContractsChannelRequiresStructuralEntrypointIdentity(t *testing.T) {
	mod, err := ParseModule("websocket/chat/index.gox", `module index
import "github.com/gox-lang/gox"
func Index(channel *gox.Channel) gox.Channel {
 channel.On("message", func(socket *gox.Socket, message string) {})
 channel.Emit("message", "hello")
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 2 {
		t.Fatalf("initial contracts = %#v", mod.WebSocketContracts)
	}
	for _, contract := range mod.WebSocketContracts {
		if contract.Scope != WebSocketContractChannel || contract.Channel != "Chat/Index" {
			t.Fatalf("contract identity = %#v", contract)
		}
	}

	mod.Structure.ChannelFunction = "Chat"
	if contracts := collectWebSocketContracts(mod); len(contracts) != 0 {
		t.Fatalf("contracts with mismatched structural entrypoint = %#v", contracts)
	}
}

func TestWebSocketContractsUseCanonicalHierarchicalChannelIdentity(t *testing.T) {
	mod, err := ParseModule("websocket/game/room/private.gox", `module private
import "github.com/gox-lang/gox"
func Private(channel *gox.Channel) gox.Channel {
 channel.On("join", func(socket *gox.Socket, room string) {})
 channel.Emit("joined", room)
}`)
	if err != nil {
		t.Fatal(err)
	}
	if mod.Structure.ChannelName != "Game/Room/Private" {
		t.Fatalf("channel structure = %#v", mod.Structure)
	}
	if len(mod.WebSocketContracts) != 2 {
		t.Fatalf("contracts = %#v", mod.WebSocketContracts)
	}
	for _, contract := range mod.WebSocketContracts {
		if contract.Scope != WebSocketContractChannel || contract.Channel != "Game/Room/Private" {
			t.Fatalf("contract identity = %#v", contract)
		}
		if strings.Contains(contract.Channel, "#") {
			t.Fatalf("static Channel contract contains instance separator: %#v", contract)
		}
	}
}

func TestWebSocketContractsIgnoreChannelEventsOutsideChannelEntrypointFunction(t *testing.T) {
	mod, err := ParseModule("websocket/chat/chat.gox", `module chat
import "github.com/gox-lang/gox"
func helper(channel *gox.Channel) {
 channel.On("helper", func(socket *gox.Socket, value string) {})
 channel.Emit("helper", value)
}
func Chat(channel *gox.Channel) gox.Channel {
 channel.On("entrypoint", func(socket *gox.Socket, value string) {})
 channel.Emit("entrypoint", value)
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 2 {
		t.Fatalf("contracts = %#v", mod.WebSocketContracts)
	}
	for _, contract := range mod.WebSocketContracts {
		if contract.Event != "entrypoint" || contract.Channel != "Chat" {
			t.Fatalf("unexpected contract = %#v", contract)
		}
	}
}

func TestWebSocketContractsCollectDirectSelectedSocketEmitOutsideEntrypoint(t *testing.T) {
	mod, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Notify() gox.Action {
 gox.Sockets.GetByID("abc").Emit("direct-chain", 42)
 var accepted bool
 gox.Sockets.GetByID("abc").Emit("confirm-chain", "value").Await(&accepted)
 return true
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 2 {
		t.Fatalf("contracts = %d, want 2: %#v", len(mod.WebSocketContracts), mod.WebSocketContracts)
	}
	if c := findWSContract(t, mod, WebSocketContractClientOn, "direct-chain"); c.Scope != WebSocketContractSocket || c.HasReply {
		t.Fatalf("direct-chain = %#v", c)
	}
	if c := findWSContract(t, mod, WebSocketContractClientOn, "confirm-chain"); !c.HasReply || c.ReplyTarget == nil {
		t.Fatalf("confirm-chain = %#v", c)
	}
}

func TestWebSocketContractsCollectServerSocketDeclaredWithVarOutsideEntrypoint(t *testing.T) {
	mod, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Notify() gox.Action {
 var target = gox.Sockets.GetByID("abc")
 target.Emit("var-direct", 42)
 return true
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 1 || mod.WebSocketContracts[0].Event != "var-direct" {
		t.Fatalf("contracts = %#v", mod.WebSocketContracts)
	}
}

func TestWebSocketContractsServerSocketAuthorityIsFunctionLocal(t *testing.T) {
	mod, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func First() gox.Action {
 target := gox.Sockets.GetByID("abc")
 target.Emit("authorized", 1)
 return true
}
func Second(target *gox.Socket) gox.Action {
 target.Emit("not-authorized", 2)
 return true
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 1 {
		t.Fatalf("contracts = %#v, want only function-local selected socket emission", mod.WebSocketContracts)
	}
	if mod.WebSocketContracts[0].Event != "authorized" {
		t.Fatalf("contract = %#v", mod.WebSocketContracts[0])
	}
}

func TestWebSocketContractsIgnoreArbitrarySocketEmitOutsideEntrypoint(t *testing.T) {
	mod, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Notify(socket *gox.Socket) gox.Action {
 socket.Emit("unproven", 42)
 return true
}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.WebSocketContracts) != 0 {
		t.Fatalf("contracts = %#v, want none for unproven socket receiver", mod.WebSocketContracts)
	}
}
