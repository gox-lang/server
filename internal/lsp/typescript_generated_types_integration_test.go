package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/clienttypes"
	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestGeneratedTypesCompileAsTypeScriptContracts(t *testing.T) {
	tsc, err := exec.LookPath("tsc")
	if err != nil {
		t.Skip("tsc is not available on PATH")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/types\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := clienttypes.EnsureStatic(root); err != nil {
		t.Fatal(err)
	}

	sources := map[string]string{
		filepath.Join(root, "pages", "index.gox"): `module index
import "github.com/gox-lang/gox"
type User struct { ID int ` + "`json:\"id\"`" + `; Name string ` + "`json:\"name\"`" + ` }
func Login(email string, password string) gox.Action { return User{ID: 1, Name: email} }
`,
		filepath.Join(root, "websocket", "index.gox"): `module websocket
import "github.com/gox-lang/gox"
type Profile struct { Name string ` + "`json:\"name\"`" + ` }
type Theme struct { Name string ` + "`json:\"name\"`" + ` }
func Index(socket *gox.Socket) gox.WebSocket {
    socket.On("profile", func(id string) gox.EventReply { return Profile{Name: id} })
    socket.Emit("notice", "ready")
    var theme Theme
    socket.Emit("theme", 7).Await(&theme)
}
`,
		filepath.Join(root, "websocket", "chat.gox"): `module chat
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel {
    channel.On("message", func(socket *gox.Socket, text string) gox.EventReply { return true })
    channel.Emit("presence", 7)
}
`,
	}

	for path, source := range sources {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		mod, err := goxcompiler.ParseProjectModule(root, path, source)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if err := goxcompiler.UpdateActionTypesForModule(root, mod); err != nil {
			t.Fatalf("actions %s: %v", path, err)
		}
		if err := goxcompiler.UpdateWebSocketEventTypesForModule(root, mod); err != nil {
			t.Fatalf("websocket %s: %v", path, err)
		}
	}

	client := `import Actions, { GoxActionError } from "@gox/actions"
import { Socket } from "@gox/websocket"

const actionResult: Promise<[{ id: number; name: string } | null, GoxActionError | null]> = Actions.Login("a@example.com", "secret")
void actionResult
// @ts-expect-error Action arguments come from the GOX declaration
Actions.Login(42, "secret")

const socket = new Socket()
const profile: Promise<{ name: string } | undefined> = socket.emit("profile", "user-1")
void profile
// @ts-expect-error known Socket events cannot fall through to the generic emit overload
socket.emit("profile", 42)
socket.on("notice", (message) => { const value: string = message; void value })
socket.on("theme", async (id) => { const value: number = id; void value; return { name: "dark" } })
// @ts-expect-error Await contracts require the client handler reply type
socket.on("theme", () => "wrong")

const chat = new socket.Channel("Chat#general")
const reply: Promise<boolean | undefined> = chat.emit("message", "hello").await()
void reply
// @ts-expect-error known Channel events cannot fall through to the generic emit overload
chat.emit("message", 42)
chat.on("presence", (id) => { const value: number = id; void value })
// @ts-expect-error known event parameter types must not use the fallback
chat.on("presence", (id: string) => {})

const dynamicEvent: string = "runtime-event"
socket.on(dynamicEvent, () => "dynamic")
chat.on(dynamicEvent, () => "dynamic")
socket.on("custom-event", () => "custom")
chat.on("custom-event", () => "custom")
const dynamicSocketReply: Promise<number | undefined> = socket.emit<number>(dynamicEvent, 42)
void dynamicSocketReply
const dynamicChannelReply = chat.emit<number>(dynamicEvent, 42)
void dynamicChannelReply
`
	if err := os.WriteFile(filepath.Join(root, "client.ts"), []byte(client), 0o644); err != nil {
		t.Fatal(err)
	}
	config := `{"compilerOptions":{"strict":true,"noEmit":true,"target":"ES2022","module":"ESNext","moduleResolution":"Bundler","lib":["ES2022","DOM"]},"include":["client.ts",".build/**/*.d.ts"]}`
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(tsc, "-p", filepath.Join(root, "tsconfig.json"), "--pretty", "false")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated GOX declarations do not type-check with TypeScript: %v\n%s", err, output)
	}
}
