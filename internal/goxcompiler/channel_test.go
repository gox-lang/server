package gox

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"strings"
	"testing"
)

func TestCompileChannelExportsFactoryAndStripsMarkerReturn(t *testing.T) {
	mod, err := ParseModule("websocket/chat/chat.gox", `module chat
import "github.com/gox-lang/gox"

func Chat(channel *gox.Channel) gox.Channel {
	channel.When("validate", func(socket *gox.Socket, token string) bool { return token != "" })
	channel.On("message", func(socket *gox.Socket, message string) {})
}`)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	if err != nil {
		t.Fatal(err)
	}
	file := &ast.File{Name: ast.NewIdent("main"), Decls: bundle.Decls}
	var output bytes.Buffer
	if err := format.Node(&output, token.NewFileSet(), file); err != nil {
		t.Fatal(err)
	}
	generated := output.String()
	if !strings.Contains(generated, "var GoxChannelHandler gox.ChannelHandler = GoxPage.Chat") {
		t.Fatalf("channel factory was not exported:\n%s", generated)
	}
	if strings.Contains(generated, "func (m *GoxModuleChat) Chat(channel *gox.Channel) gox.Channel") {
		t.Fatalf("channel marker return was not stripped:\n%s", generated)
	}
	if !strings.Contains(generated, `channel.OnRegistered("message", []string{"socket", "message"}, false`) {
		t.Fatalf("channel On signature was not recorded:\n%s", generated)
	}
}

func TestChannelRejectsInvalidAndMultipleEntrypoints(t *testing.T) {
	_, err := ParseModule("websocket/chat/chat.gox", `module chat
import "github.com/gox-lang/gox"
func Chat(socket *gox.Socket) gox.Channel {}`)
	if err == nil || !strings.Contains(err.Error(), "invalid Channel signature") {
		t.Fatalf("signature error = %v", err)
	}

	_, err = ParseModule("websocket/chat/chat.gox", `module chat
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel {}
func Chat(channel *gox.Channel) gox.Channel {}`)
	if err == nil || !strings.Contains(err.Error(), "more than one Channel entrypoint") {
		t.Fatalf("multiple error = %v", err)
	}
}

func TestChannelEntrypointUsesStructuralIdentity(t *testing.T) {
	mod, err := ParseModule("websocket/chat/index.gox", `module index
import "github.com/gox-lang/gox"
func Index(channel *gox.Channel) gox.Channel {
	channel.On("message", func(socket *gox.Socket, message string) {})
}`)
	if err != nil {
		t.Fatal(err)
	}
	if mod.Structure.ChannelName != "Chat/Index" || mod.Structure.ChannelFunction != "Index" {
		t.Fatalf("structure = %#v", mod.Structure)
	}
	if len(mod.WebSocketContracts) != 1 || mod.WebSocketContracts[0].Channel != "Chat/Index" {
		t.Fatalf("contracts = %#v", mod.WebSocketContracts)
	}
}

func TestChannelRejectsDeclarationOutsidePublicChannel(t *testing.T) {
	for _, path := range []string{
		"pages/chat.gox",
		"websocket/chat/chat.helper.gox",
		"websocket/@shared/chat.gox",
		"app/chat.gox",
	} {
		_, err := ParseModule(path, `module chat
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel {}`)
		if err == nil || !strings.Contains(err.Error(), "outside a public websocket Channel file") {
			t.Fatalf("%s: error = %v", path, err)
		}
	}
}

func TestPublicChannelRequiresEntrypoint(t *testing.T) {
	_, err := ParseModule("websocket/chat/index.gox", `module index
func Helper() {}`)
	if err == nil || !strings.Contains(err.Error(), "does not declare its Index entrypoint") {
		t.Fatalf("missing error = %v", err)
	}
}

func TestChannelEntrypointNameComesFromStructuralPath(t *testing.T) {
	_, err := ParseModule("websocket/chat/index.gox", `module index
import "github.com/gox-lang/gox"
func Chat(channel *gox.Channel) gox.Channel {}`)
	if err == nil || !strings.Contains(err.Error(), "does not match Index") {
		t.Fatalf("name error = %v", err)
	}
}
