package websocket

import (
	"github.com/gox-lang/gox"
	"strings"
	"testing"
)

type testIslandAssets struct{}

func (testIslandAssets) ClientRuntimeHTML() []byte {
	return []byte("<script src=\"/runtime.js\"></script>")
}
func (testIslandAssets) ResolveClientIsland(key string) (gox.ClientIslandSnapshot, bool) {
	return gox.ClientIslandSnapshot{ScriptURL: "/counter.js"}, key == "counter"
}

func TestEncodeContextTemplateIncludesClientAssets(t *testing.T) {
	render := func(ctx *gox.Context) {
		gox.WriteClientIsland(ctx, "counter", func(ctx *gox.Context) {
			gox.WriteClientPropsAttribute(ctx.Writer, map[string]any{"name": "<George>"})
		}, nil)
	}
	for i := 0; i < 2; i++ {
		args, transfers, err := EncodeValuesWithTransfers([]any{render}, func() uint64 { return 1 }, testIslandAssets{})
		if err != nil {
			t.Fatal(err)
		}
		if len(transfers) != 0 || len(args) != 1 || args[0].Type != ArgumentHTML {
			t.Fatalf("args=%v transfers=%v", args, transfers)
		}
		html := string(args[0].Data)
		for _, want := range []string{"/runtime.js", "<gox-isle", "/counter.js", "data-props=", "</gox-isle>"} {
			if !strings.Contains(html, want) {
				t.Fatalf("missing %q: %s", want, html)
			}
		}
		if strings.Contains(html, "<George>") {
			t.Fatalf("unescaped props: %s", html)
		}
	}
}
