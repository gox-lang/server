package serverruntime

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

func TestClientValuesRouteServesSharedDOMNormalizer(t *testing.T) {
	writer, recorder := newTestGoxWriter(t, noCompressionConfig(), "", false)
	req, _ := http.NewRequest(http.MethodGet, "/_gox/client/values.js", nil)
	ctx := &gox.Context{
		Writer:   writer,
		Response: gox.NewResponse(),
		Request:  gox.NewRequest(gox.RequestInput{URL: gox.NewURL(gox.URLInput{Path: "/_gox/client/values.js"})}),
	}
	runtime := &Runtime{}
	if !runtime.clientValuesRoute(ctx, req) {
		t.Fatal("clientValuesRoute did not handle its fixed path")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if contentType, _ := ctx.Response.Headers.Get("Content-Type"); contentType != "text/javascript; charset=utf-8" {
		t.Fatalf("content type = %q", contentType)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "normalizeClientValue") {
		t.Fatalf("shared normalizer missing from response: %q", body)
	}
}

func newClientStaticRouteTestRuntime(t *testing.T, root string, result *goxcompiler.CompileResult) *Runtime {
	t.Helper()
	state, err := NewRuntimeState(server.Config{
		ProjectName: "client-static-route-test",
		Static:      server.StaticConfig{Path: "public"},
		Session: server.SessionConfig{
			TTL: 3 * time.Hour,
		},
	}, root, result)
	if err != nil {
		t.Fatalf("NewRuntimeState failed: %v", err)
	}
	r := &Runtime{ProjectRoot: root}
	r.runtimeState.Store(state)
	return r
}

func newClientStaticRouteContext(path string) (*gox.Context, *http.Request) {
	req, _ := http.NewRequest(http.MethodGet, path, nil)
	ctx := &gox.Context{Request: gox.NewRequest(gox.RequestInput{
		Method: http.MethodGet,
		URL: gox.NewURL(gox.URLInput{
			String: path,
			Path:   path,
		}),
		Headers: gox.NewCollection(map[string][]string{}),
		Route: gox.NewRoute(gox.RouteInput{
			Original: path,
			Target:   path,
			Pattern:  path,
		}),
	})}
	return ctx, req
}

func TestClientStaticRouteMarksClientAssetAndTargetsBuildClientFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, ".build", "client", "react", "prod", "menu.js")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("export default 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := newClientStaticRouteTestRuntime(t, root, &goxcompiler.CompileResult{
		ClientAssetMap: map[string]string{"/_gox/client/menu.js": file},
		ClientBundles: []goxcompiler.CompileClientBundle{{
			Name:      "menu",
			Framework: "react",
			Mode:      "prod",
			EntryURL:  "/_gox/client/menu.js",
			EntryFile: file,
			Assets:    []goxcompiler.CompileClientAsset{{Kind: "entry", URL: "/_gox/client/menu.js", File: file}},
		}},
	})
	ctx, req := newClientStaticRouteContext("/_gox/client/menu.js")

	runtime.clientStaticRoute(ctx, req)

	if !ctx.Request.Route.IsClientAsset() {
		t.Fatal("expected route to be marked client asset")
	}
	if !ctx.Request.Route.IsStatic() {
		t.Fatal("client asset route must be static")
	}
	if got := ctx.Request.Route.Target(); got != file {
		t.Fatalf("target = %q, want %q", got, file)
	}
	if got := ctx.Request.Route.Pattern(); got != "/_gox/client/menu.js" {
		t.Fatalf("pattern = %q", got)
	}
}

func TestClientStaticRouteDoesNotMutateMissingAsset(t *testing.T) {
	root := t.TempDir()
	runtime := newClientStaticRouteTestRuntime(t, root, nil)
	ctx, req := newClientStaticRouteContext("/_gox/client/missing.js")
	before := ctx.Request.Route

	runtime.clientStaticRoute(ctx, req)

	if ctx.Request.Route.IsClientAsset() || ctx.Request.Route.Target() != before.Target() || ctx.Request.Route.Pattern() != before.Pattern() {
		t.Fatalf("route mutated for missing client asset: %#v", ctx.Request.Route)
	}
}

func TestCleanBuildClientPathRejectsTraversalOutsideBuildClient(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "public", "style.css")
	if got, ok := cleanBuildClientPath(root, outside); ok {
		t.Fatalf("cleanBuildClientPath accepted outside path: %q", got)
	}

	inside := filepath.Join(root, ".build", "client", "react", "prod", "menu.js")
	if got, ok := cleanBuildClientPath(root, inside); !ok || got != inside {
		t.Fatalf("cleanBuildClientPath inside = %q, %v; want %q, true", got, ok, inside)
	}
}
