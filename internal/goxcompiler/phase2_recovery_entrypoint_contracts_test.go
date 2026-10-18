package gox

import "testing"

func TestPhase2RecoveryPhase10PageContracts(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	t.Run("HTTPMethodMustBeUppercase", func(t *testing.T) {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Get(ctx *gox.Context) gox.Page { return "ok" }
`)
		phase2RequireError(t, err)
	})

	t.Run("ContextParameterMustBeNamedCtx", func(t *testing.T) {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func GET(c *gox.Context) gox.Page { return "ok" }
`)
		phase2RequireError(t, err)
	})

	t.Run("PageEntrypointCannotLiveInImportedPrivateModule", func(t *testing.T) {
		err := phase2CompileExpectError(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index
import Helper "./index.helper.gox"
import "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page { return Helper.Message() }
`,
			files: map[string]string{
				"pages/index.helper.gox": `module helper
import "github.com/gox-lang/gox"
func Message() string { return "hello" }
func GET(ctx *gox.Context) gox.Page { return "invalid" }
`,
			},
		})
		phase2RequireError(t, err)
	})
}

func TestPhase2RecoveryPhase11ActionContracts(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	t.Run("HTTPMethodNameIsReserved", func(t *testing.T) {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func GET() gox.Action { return nil }
`)
		phase2RequireError(t, err)
	})

	t.Run("VariadicActionRejected", func(t *testing.T) {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Save(values ...string) gox.Action { return values }
`)
		phase2RequireError(t, err)
	})
}

func TestPhase2RecoveryPhase12WebSocketAndChannelContracts(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	t.Run("WebSocketInlineOnHandlerCannotBeVariadic", func(t *testing.T) {
		_, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("message", func(values ...string) {})
}
`)
		phase2RequireError(t, err)
	})

	t.Run("WebSocketNamedOnHandlerCannotBeVariadic", func(t *testing.T) {
		_, err := ParseModule("websocket/index.gox", `module websocket
import "github.com/gox-lang/gox"
func handle(values ...string) {}
func Index(socket *gox.Socket) gox.WebSocket {
	socket.On("message", handle)
}
`)
		phase2RequireError(t, err)
	})

	t.Run("ChannelOnHandlerCannotBeVariadic", func(t *testing.T) {
		_, err := ParseModule("websocket/chat/chat.gox", `module chat
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel {
	channel.On("message", func(socket *gox.Socket, values ...string) {})
}
`)
		phase2RequireError(t, err)
	})
}

func TestPhase2RecoveryPhase13ConfigContract(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	err := phase2CompileExpectError(t, phase1GeneratedGoFixture{
		entryRel: "app/config.gox",
		source: `module app
import "github.com/gox-lang/gox/server"
func SetGoxConfig(value string) server.Config { return server.Config{} }
`,
	})
	phase2RequireError(t, err)
}
