package serverruntime

import (
	"net/http/httptest"
	"strings"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"

	"github.com/gox-lang/gox"
)

const testClientKey = "react#menu/menu.jsx#dev"

func testGoxIslePolicy() *ClientAssetPolicy {
	return NewClientAssetPolicy(&goxcompiler.CompileResult{
		ClientRuntimeURL:   "/_gox/client/gox-isle-runtime.js",
		ClientBaseStyleURL: "/_gox/client/gox-isle-base.css",
		ClientComponents: []goxcompiler.CompileClientComponent{
			{
				Name:      "menuReact",
				Key:       testClientKey,
				Framework: "react",
				Entry:     "menu/menu.jsx",
				Path:      "menu/menu.jsx",
				Dev:       true,
				Mode:      "dev",
			},
		},
		ClientAssetMap: map[string]string{
			"/_gox/client/gox-isle-runtime.js": "/tmp/gox-isle-runtime.js",
			"/_gox/client/gox-isle-base.css":   "/tmp/gox-isle-base.css",
			"/_gox/client/menu.js":             "/tmp/menu.js",
			"/_gox/client/menu.css":            "/tmp/menu.css",
		},
		ClientBundles: []goxcompiler.CompileClientBundle{
			{
				Name:      "menuReact",
				Framework: "react",
				Entry:     "menu/menu.jsx",
				Path:      "menu/menu.jsx",
				Mode:      "dev",
				EntryURL:  "/_gox/client/menu.js",
				EntryFile: "/project/menu/menu.jsx",
				Assets: []goxcompiler.CompileClientAsset{
					{Kind: "entry", URL: "/_gox/client/menu.js", File: "/tmp/menu.js"},
					{Kind: "css", URL: "/_gox/client/menu.css", File: "/tmp/menu.css"},
				},
				Graph: goxcompiler.CompileClientDependencyGraph{EntryFile: "/project/menu/menu.jsx"},
			},
		},
	})
}

func newClientIslandTestContext(t testing.TB) (*gox.Context, *GoxWriter, *httptest.ResponseRecorder) {
	t.Helper()
	writer, rec := newTestGoxWriter(t, noCompressionConfig(), "", false)
	ctx := &gox.Context{Writer: writer}
	gox.BindClientIslandRuntime(ctx, testGoxIslePolicy())
	return ctx, writer, rec
}

func TestWriteClientRuntimeWritesOnce(t *testing.T) {
	ctx, writer, rec := newClientIslandTestContext(t)
	gox.WriteClientRuntime(ctx)
	gox.WriteClientRuntime(ctx)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<script type="module" src="/_gox/client/gox-isle-runtime.js"></script>`,
		`<link rel="stylesheet" href="/_gox/client/gox-isle-base.css">`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	if count := strings.Count(body, "gox-isle-runtime.js"); count != 1 {
		t.Fatalf("runtime count = %d, want 1: %s", count, body)
	}
}

func TestWriteClientIslandWritesStylesAttrsAndChildren(t *testing.T) {
	ctx, writer, rec := newClientIslandTestContext(t)
	gox.WriteClientIsland(ctx, testClientKey, func(ctx *gox.Context) {
		ctx.Writer.WriteRawString(` data-hydrate="hover" data-isolate="true"`)
		gox.WriteClientIslandDataAssets(ctx)
		ctx.Writer.WriteRawString(` data-props="{&#34;title&#34;:&#34;Menu&#34;}"`)
	}, func(ctx *gox.Context) {
		ctx.Writer.WriteRawString(`<div>SSR</div>`)
	})
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<script type="module" src="/_gox/client/gox-isle-runtime.js"></script>`,
		`<link rel="stylesheet" href="/_gox/client/gox-isle-base.css">`,
		`<link rel="stylesheet" href="/_gox/client/menu.css">`,
		`<gox-isle data-script="/_gox/client/menu.js" data-hydrate="hover" data-isolate="true"`,
		`data-assets="[&#34;/_gox/client/gox-isle-base.css&#34;,&#34;/_gox/client/menu.css&#34;]"`,
		`data-props="{&#34;title&#34;:&#34;Menu&#34;}"`,
		`<div>SSR</div></gox-isle>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestWriteClientIslandDeduplicatesRuntimeAndStyles(t *testing.T) {
	ctx, writer, rec := newClientIslandTestContext(t)
	for i := 0; i < 2; i++ {
		gox.WriteClientIsland(ctx, testClientKey, nil, nil)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	body := rec.Body.String()
	if count := strings.Count(body, "gox-isle-runtime.js"); count != 1 {
		t.Fatalf("runtime count = %d, want 1: %s", count, body)
	}
	if count := strings.Count(body, `href="/_gox/client/menu.css"`); count != 1 {
		t.Fatalf("style count = %d, want 1: %s", count, body)
	}
	if count := strings.Count(body, `<gox-isle `); count != 2 {
		t.Fatalf("island count = %d, want 2: %s", count, body)
	}
}

func TestImportMapApplyCSPBeforeWriteDynamicNonce(t *testing.T) {
	writer, rec := newTestGoxWriter(t, noCompressionConfig(), "", false)
	writer.res.response.ImportMap.Set("three", "/three.js")
	writer.res.response.ImportMap.Runtime().ApplyCSP("")
	writer.res.response.ImportMap.Runtime().WriteCompiled(writer, nil, nil, "")
	securitySource := writer.res.response.ImportMap.Runtime().SecuritySource()
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if securitySource == "" || !strings.Contains(csp, "script-src 'self' "+securitySource) {
		t.Fatalf("CSP missing security source %q: %q", securitySource, csp)
	}
}

func TestImportMapApplyCSPBeforeWriteStaticHash(t *testing.T) {
	writer, rec := newTestGoxWriter(t, noCompressionConfig(), "", false)
	const hash = "'sha256-testhash'"
	writer.res.response.ImportMap.Runtime().ApplyCSP(hash)
	writer.res.response.ImportMap.Runtime().WriteCompiled(writer, [][2]string{{"react", "/react.js"}}, []byte(`<script type="importmap">{"imports":{"react":"/react.js"}}</script>`), hash)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, `script-src 'self' `+hash) {
		t.Fatalf("CSP missing static hash %q: %q", hash, csp)
	}
}
