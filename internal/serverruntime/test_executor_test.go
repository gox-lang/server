package serverruntime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
	sdk "github.com/gox-lang/gox/test"
)

func TestBuildActionHTTPRequestUsesPositionalURLEncodedFields(t *testing.T) {
	input, err := buildActionHTTPRequestInput(sdk.NewActionRequest{URL: "/action", Method: "Update", Params: sdk.ActionParams{"George", 35, true}})
	if err != nil {
		t.Fatal(err)
	}
	request, err := buildTestHTTPRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("0") != "George" || values.Get("1") != "35" || values.Get("2") != "true" {
		t.Fatalf("values = %#v", values)
	}
	if request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %q", request.Header.Get("Content-Type"))
	}
}

func TestBuildActionHTTPRequestUsesMultipartForFiles(t *testing.T) {
	file, _ := gox.NewFile(gox.FileConfig{Name: "avatar.txt", Type: "text/plain", Content: strings.NewReader("George")})
	input, err := buildActionHTTPRequestInput(sdk.NewActionRequest{URL: "/action", Method: "Upload", Params: sdk.ActionParams{"George", file}})
	if err != nil {
		t.Fatal(err)
	}
	request, err := buildTestHTTPRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/form-data;") {
		t.Fatalf("content type = %q", request.Header.Get("Content-Type"))
	}
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	if request.FormValue("0") != "George" || len(request.MultipartForm.File["1"]) != 1 || request.MultipartForm.File["1"][0].Filename != "avatar.txt" {
		t.Fatalf("multipart = %#v", request.MultipartForm)
	}
}

var benchmarkAllocSink any

func benchmarkAllocatingGoxHandler(ctx *gox.Context) {
	//line pages/index.gox:9:1
	buf := make([]byte, 256)
	//line internal/serverruntime/test_executor_test.go:1:1
	benchmarkAllocSink = buf
	ctx.Writer.WriteRawString("allocs")
}

func newTestExecutorRuntime(t testing.TB) *Runtime {
	t.Helper()

	logs.SetEnabled(false)
	t.Cleanup(func() { logs.SetEnabled(true) })

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "public"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := server.Config{
		ProjectName: "executor-test",
		HTTP: server.HTTPConfig{
			MethodsAllowed: []string{http.MethodGet, http.MethodPost},
			Compression:    server.CompressionConfig{},
		},
		Static: server.StaticConfig{Path: "public"},
		Session: server.SessionConfig{
			TTL: 3 * time.Hour,
		},
	}

	state, err := NewRuntimeState(cfg, root, nil)
	if err != nil {
		t.Fatalf("NewRuntimeState failed: %v", err)
	}

	rt := &Runtime{
		Config:           cfg,
		Out:              io.Discard,
		ProjectRoot:      root,
		rateLimitStore:   NewRateLimitStore(),
		staticBuildGroup: newStaticSingleflightGroup(),
		goxBuildGroup:    newStaticSingleflightGroup(),
		cache404:         &Cache404{items: make(map[string]struct{})},
		goxRoutes:        make(map[string]*GoxRouteRuntime),
	}
	rt.runtimeState.Store(state)
	if state.Session != nil {
		storage, _, err := OpenSessionRuntime(nil, state.Session, "test-instance")
		if err != nil {
			t.Fatalf("OpenSessionRuntime failed: %v", err)
		}
		rt.sessionStorage = storage
	}
	t.Cleanup(func() {
		if rt.sessionStorage != nil {
			_ = rt.sessionStorage.Close()
		}
		if rt.rateLimitStore != nil {
			rt.rateLimitStore.Close()
		}
	})

	rt.goxRoutes["/session"] = &GoxRouteRuntime{
		RoutePath: "/session",
		State:     GoxRouteLoaded,
		HTTPHandlers: map[string]func(*gox.Context){
			http.MethodGet: func(ctx *gox.Context) {
				var userID string
				ok, err := ctx.Session.Get("userID", &userID)
				if err != nil || !ok {
					ctx.Response.Status.SetCode(401)
					ctx.Writer.WriteRawString("missing session")
					return
				}
				ctx.Writer.WriteRawString("user:" + userID)
			},
		},
		Actions: map[string]gox.ActionHandler{
			"WhoAmI": func(ctx *gox.Context) {
				var userID string
				ok, err := ctx.Session.Get("userID", &userID)
				if err != nil || !ok {
					ctx.Response.Headers.Set("Action-Response", "error")
					ctx.Response.Headers.Set("Content-Type", "text/plain; charset=utf-8")
					ctx.Writer.WriteTextString("missing session")
					return
				}
				ctx.Response.Headers.Set("Action-Response", "success")
				ctx.Response.Headers.Set("Content-Type", "application/json; charset=utf-8")
				ctx.Writer.WriteRawAny(map[string]any{"userID": userID})
			},
		},
	}

	rt.goxRoutes["/cookies"] = &GoxRouteRuntime{
		RoutePath: "/cookies",
		State:     GoxRouteLoaded,
		HTTPHandlers: map[string]func(*gox.Context){
			http.MethodPost: func(ctx *gox.Context) {
				ctx.Response.Cookies.Set(gox.Cookie{Name: "gox-test-cookie", Value: "cookie-value", Path: "/"})
				ctx.Writer.WriteRawString("cookie set")
			},
			http.MethodGet: func(ctx *gox.Context) {
				value, ok := ctx.Request.Cookies.Get("gox-test-cookie")
				if !ok {
					value, ok = ctx.Request.Cookies.Get("gox-test-action-cookie")
				}
				if value == "" {
					ctx.Response.Status.SetCode(401)
					ctx.Writer.WriteRawString("missing cookie")
					return
				}
				ctx.Writer.WriteRawString("cookie:" + value)
			},
		},
		Actions: map[string]gox.ActionHandler{
			"SetCookie": func(ctx *gox.Context) {
				ctx.Response.Cookies.Set(gox.Cookie{Name: "gox-test-action-cookie", Value: "action-cookie-value", Path: "/"})
				ctx.Response.Headers.Set("Action-Response", "success")
				ctx.Response.Headers.Set("Content-Type", "application/json; charset=utf-8")
				ctx.Writer.WriteRawAny(map[string]any{"ok": true})
			},
		},
	}

	rt.goxRoutes["/pipeline"] = &GoxRouteRuntime{
		RoutePath: "/pipeline",
		State:     GoxRouteLoaded,
		HTTPHandlers: map[string]func(*gox.Context){
			http.MethodPost: func(ctx *gox.Context) {
				ctx.Response.Cookies.Set(gox.Cookie{Name: "gox-pipeline-state", Value: "ready", Path: "/"})
				ctx.Writer.WriteRawString("ready")
			},
			http.MethodGet: func(ctx *gox.Context) {
				value, ok := ctx.Request.Cookies.Get("gox-pipeline-state")
				if !ok || value != "ready" {
					ctx.Response.Status.SetCode(409)
					ctx.Writer.WriteRawString("pipeline out of order")
					return
				}
				ctx.Response.Cookies.Set(gox.Cookie{Name: "gox-pipeline-state", Value: "done", Path: "/"})
				ctx.Writer.WriteRawString("done")
			},
		},
	}

	rt.goxRoutes["/index"] = &GoxRouteRuntime{
		RoutePath: "/index",
		State:     GoxRouteLoaded,
		HTTPHandlers: map[string]func(*gox.Context){
			http.MethodGet: func(ctx *gox.Context) {
				ctx.Response.Headers.Set("Content-Type", "text/html; charset=utf-8")
				ctx.Writer.WriteRawString("<h1>Hello Executor</h1>")
			},
			http.MethodPost: func(ctx *gox.Context) {
				ctx.Response.Headers.Set("X-Test-Method", ctx.Request.Method())
				ctx.Writer.WriteRawString("posted")
			},
		},
		Actions: map[string]gox.ActionHandler{
			"Sum": func(ctx *gox.Context) {
				var a, b int
				spec := &gox.ActionSpec{Name: "Sum", Params: []gox.ActionParamSpec{{Name: "a", Type: gox.ActionNumber}, {Name: "b", Type: gox.ActionNumber}}}
				if err := gox.ValidateActionParams(ctx, spec, &a, &b); err != nil {
					ctx.Response.Headers.Set("Action-Response", "error")
					ctx.Response.Headers.Set("Content-Type", "text/plain; charset=utf-8")
					ctx.Writer.WriteTextString(err.Error())
					return
				}
				ctx.Response.Headers.Set("Action-Response", "success")
				ctx.Response.Headers.Set("Content-Type", "application/json; charset=utf-8")
				ctx.Writer.WriteRawAny(map[string]any{"sum": a + b})
			},
			"Boom": func(ctx *gox.Context) {
				ctx.Response.Headers.Set("Action-Response", "error")
				ctx.Response.Headers.Set("Content-Type", "text/plain; charset=utf-8")
				ctx.Writer.WriteTextString("boom")
			},
		},
	}

	rt.goxRoutes["/action-allocs"] = &GoxRouteRuntime{
		RoutePath: "/action-allocs",
		RelPath:   "pages/index.gox",
		State:     GoxRouteLoaded,
		Actions: map[string]gox.ActionHandler{
			"Alloc": func(ctx *gox.Context) {
				_ = make([]byte, 4096)
				gox.BenchmarkHandlerSection(func() {
					name := ctx.Request.URL.Path()
					ctx.Response.Headers.Set("Action-Response", "success")
					ctx.Response.Headers.Set("Content-Type", "text/plain; charset=utf-8")
					ctx.Writer.WriteTextString("Hello " + name)
				})
				_ = make([]byte, 4096)
			},
		},
	}

	rt.goxRoutes["/allocs"] = &GoxRouteRuntime{
		RoutePath: "/allocs",
		RelPath:   "pages/index.gox",
		State:     GoxRouteLoaded,
		HTTPHandlers: map[string]func(*gox.Context){
			http.MethodGet: benchmarkAllocatingGoxHandler,
		},
	}

	rt.goxRoutes["/files"] = &GoxRouteRuntime{RoutePath: "/files", State: GoxRouteLoaded,
		HTTPHandlers: map[string]func(*gox.Context){http.MethodPost: func(ctx *gox.Context) {
			name, _ := ctx.Request.Form.Fields.Get("name")
			file, ok := ctx.Request.Form.Files.Get("avatar")
			if !ok {
				ctx.Response.Status.SetCode(400)
				return
			}
			ctx.Writer.WriteRawString(name + ":" + file.Name() + ":" + strconv.FormatInt(file.Size(), 10))
		}, http.MethodGet: func(ctx *gox.Context) {
			file, _ := gox.NewFile(gox.FileConfig{Name: "report.txt", Type: "text/plain", Content: strings.NewReader("George")})
			if err := gox.WritePageFile(ctx, file); err != nil {
				panic(err)
			}
		}},
		Actions: map[string]gox.ActionHandler{
			"Upload": func(ctx *gox.Context) {
				var name string
				var file gox.File
				spec := &gox.ActionSpec{Name: "Upload", Params: []gox.ActionParamSpec{{Name: "name", Type: gox.ActionString}, {Name: "avatar", Type: gox.ActionFile}}}
				if err := gox.ValidateActionParams(ctx, spec, &name, &file); err != nil {
					ctx.Response.Headers.Set("Action-Response", "error")
					ctx.Writer.WriteTextString(err.Error())
					return
				}
				ctx.Response.Headers.Set("Action-Response", "success")
				ctx.Response.Headers.Set("Content-Type", "application/json")
				ctx.Writer.WriteRawAny(map[string]any{"name": name, "file": file.Name(), "size": file.Size()})
			},
			"Download": func(ctx *gox.Context) {
				file, _ := gox.NewFile(gox.FileConfig{Name: "report.txt", Type: "text/plain", Content: strings.NewReader("George")})
				if err := gox.WriteActionFile(ctx, file); err != nil {
					panic(err)
				}
			},
			"DownloadMany": func(ctx *gox.Context) {
				one, _ := gox.NewFile(gox.FileConfig{Name: "one.txt", Type: "text/plain", Content: strings.NewReader("one")})
				two, _ := gox.NewFile(gox.FileConfig{Name: "two.txt", Type: "text/plain", Content: strings.NewReader("two")})
				if err := gox.WriteActionFiles(ctx, []gox.File{one, two}); err != nil {
					panic(err)
				}
			},
		},
	}

	return rt
}

func TestTestExecutorHTTPMultipartUpload(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)
	file, _ := gox.NewFile(gox.FileConfig{Name: "avatar.txt", Type: "text/plain", Content: strings.NewReader("George")})
	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{{Request: sdk.NewRequest{Method: sdk.POST, URL: "/files", Body: sdk.RequestBody.FormData(sdk.FormData{"name": "George", "avatar": file})}, Expected: sdk.NewResponseExpected{Status: 200, Body: sdk.ExpectedBody.Equal("George:avatar.txt:6")}}})
	if !match {
		t.Fatalf("result = %s", result.Error())
	}
}

func TestTestExecutorHTTPFileResponse(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)
	expected, _ := gox.NewFile(gox.FileConfig{Name: "report.txt", Type: "text/plain", Content: strings.NewReader("George")})
	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{{Request: sdk.NewRequest{Method: sdk.GET, URL: "/files"}, Expected: sdk.NewResponseExpected{Status: 200, Body: sdk.ExpectedBody.Equal(expected)}}})
	if !match {
		t.Fatalf("result = %s", result.Error())
	}
}

func TestTestExecutorActionUploadsAndDownloadsFile(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)
	upload, _ := gox.NewFile(gox.FileConfig{Name: "avatar.txt", Type: "text/plain", Content: strings.NewReader("George")})
	expected, _ := gox.NewFile(gox.FileConfig{Name: "report.txt", Type: "text/plain", Content: strings.NewReader("George")})
	one, _ := gox.NewFile(gox.FileConfig{Name: "one.txt", Type: "text/plain", Content: strings.NewReader("one")})
	two, _ := gox.NewFile(gox.FileConfig{Name: "two.txt", Type: "text/plain", Content: strings.NewReader("two")})
	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{Request: sdk.NewActionRequest{URL: "/files", Method: "Upload", Params: sdk.ActionParams{"George", upload}}, Expected: sdk.NewResponseActionExpected{Error: sdk.ExpectedActionError.Nil(), Result: sdk.ExpectedActionResult.Partial(map[string]any{"file": "avatar.txt", "size": 6})}},
		{Request: sdk.NewActionRequest{URL: "/files", Method: "Download"}, Expected: sdk.NewResponseActionExpected{Error: sdk.ExpectedActionError.Nil(), Result: sdk.ExpectedActionResult.Exact(expected)}},
		{Request: sdk.NewActionRequest{URL: "/files", Method: "DownloadMany"}, Expected: sdk.NewResponseActionExpected{Error: sdk.ExpectedActionError.Nil(), Result: sdk.ExpectedActionResult.Exact([]gox.File{one, two})}},
	})
	if !match {
		t.Fatalf("result = %s", result.Error())
	}
}

func TestTestExecutorHTTPMatch(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Headers: sdk.ExpectedHeaders.Contains(sdk.Map{
					"Content-Type": "text/html; charset=utf-8",
				}),
				Body: sdk.ExpectedBody.Contains("Hello Executor"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
	if len(result.Steps) != 1 {
		t.Fatalf("steps len = %d, want 1", len(result.Steps))
	}
}

func TestTestExecutorHTTPMismatch(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"},
			Expected: sdk.NewResponseExpected{
				Status: 404,
			},
		},
	})

	if match || result.Match {
		t.Fatalf("match = true, want false: %s", result.Error())
	}
	if got := result.Steps[0].Status.Response; got != 200 {
		t.Fatalf("status response = %#v, want 200", got)
	}
}

func TestTestExecutorRegistersSDKExecutor(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	sdk.SetExecutor(NewTestExecutor(rt))
	t.Cleanup(func() { sdk.SetExecutor(nil) })

	result, match := sdk.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Body:   sdk.ExpectedBody.Contains("Hello Executor"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorActionMatch(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewActionRequest{
				URL:    "/index",
				Method: "Sum",
				Params: sdk.ActionParams{10, 20},
			},
			Expected: sdk.NewResponseActionExpected{
				Error:  sdk.ExpectedActionError.Nil(),
				Result: sdk.ExpectedActionResult.Partial(map[string]any{"sum": 30}),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
	if !result.Steps[0].Action.Match {
		t.Fatalf("action match = false: %#v", result.Steps[0].Action)
	}
}

func TestTestExecutorActionMismatch(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewActionRequest{
				URL:    "/index",
				Method: "Sum",
				Params: sdk.ActionParams{10, 20},
			},
			Expected: sdk.NewResponseActionExpected{
				Error:  sdk.ExpectedActionError.Nil(),
				Result: sdk.ExpectedActionResult.Partial(map[string]any{"sum": 40}),
			},
		},
	})

	if match || result.Match {
		t.Fatalf("match = true, want false")
	}
	if got := result.Steps[0].Action.Result.Fields["sum"].Response; got == nil {
		t.Fatalf("missing action result sum response: %#v", result.Steps[0].Action.Result)
	}
}

func TestTestExecutorActionError(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewActionRequest{
				URL:    "/index",
				Method: "Boom",
			},
			Expected: sdk.NewResponseActionExpected{
				Error: sdk.ExpectedActionError.Exact("boom"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
	if !result.Steps[0].Action.Error.Match {
		t.Fatalf("action error match = false: %#v", result.Steps[0].Action.Error)
	}
}

func TestTestExecutorConfigSessionHTTP(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{
		Session: sdk.Session{
			"userID": "123",
		},
	}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/session"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Body:   sdk.ExpectedBody.Contains("user:123"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorConfigSessionAction(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{
		Session: sdk.Session{
			"userID": "456",
		},
	}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewActionRequest{
				URL:    "/session",
				Method: "WhoAmI",
			},
			Expected: sdk.NewResponseActionExpected{
				Error:  sdk.ExpectedActionError.Nil(),
				Result: sdk.ExpectedActionResult.Partial(map[string]any{"userID": "456"}),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorCookieJarHTTPPipeline(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.POST, URL: "/cookies"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Body:   sdk.ExpectedBody.Contains("cookie set"),
			},
		},
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/cookies"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Body:   sdk.ExpectedBody.Contains("cookie:cookie-value"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps len = %d, want 2", len(result.Steps))
	}
}

func TestTestExecutorCookieJarActionToHTTPPipeline(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewActionRequest{URL: "/cookies", Method: "SetCookie"},
			Expected: sdk.NewResponseActionExpected{
				Error:  sdk.ExpectedActionError.Nil(),
				Result: sdk.ExpectedActionResult.Partial(map[string]any{"ok": true}),
			},
		},
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/cookies"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Body:   sdk.ExpectedBody.Contains("cookie:action-cookie-value"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorRequestCookies(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{
				Method: sdk.GET,
				URL:    "/cookies",
				Cookies: sdk.RequestCookies{
					"gox-test-cookie": "cookie-value",
				},
			},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Body:   sdk.ExpectedBody.Contains("cookie:cookie-value"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorExpectedCookies(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.POST, URL: "/cookies"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Cookies: sdk.ExpectedCookies.Contains(sdk.Map{
					"gox-test-cookie": "cookie-value",
				}),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorExpectedCookiesNotContains(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.POST, URL: "/cookies"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Cookies: sdk.ExpectedCookies.NotContains(sdk.Map{
					"debug-cookie": "",
				}),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorSnapshot(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "snapshots", "index.html"), []byte("<h1>Hello Executor</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor := NewTestExecutorWithOptions(rt, TestExecutorOptions{OutputRoot: root})

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Body:   sdk.ExpectedBody.Snapshot("index.html"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorOverrideConfig(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	base := rt.Config
	called := false

	executor := NewTestExecutorWithOptions(rt, TestExecutorOptions{
		BaseConfig: &base,
		RuntimeFactory: func(cfg server.Config) (*Runtime, error) {
			called = true
			if cfg.ProjectName != "overridden-project" {
				t.Fatalf("ProjectName = %q, want overridden-project", cfg.ProjectName)
			}
			return rt, nil
		},
	})

	result, match := executor.Execute(sdk.Config{
		OverrideConfig: func(cfg *server.Config) {
			cfg.ProjectName = "overridden-project"
		},
	}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Body:   sdk.ExpectedBody.Contains("Hello Executor"),
			},
		},
	})

	if !called {
		t.Fatal("runtime factory was not called")
	}
	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorCustomMatchers(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Headers: sdk.ExpectedHeaders.Custom(map[string]sdk.ExpectedStringRule{
					"Content-Type": sdk.Expect.Contains("text/html"),
					"X-Debug":      sdk.Expect.NotExists(),
				}),
				Body: sdk.ExpectedBody.Custom(
					sdk.Expect.Contains("Hello"),
					sdk.Expect.NotContains("panic"),
				),
			},
		},
	})
	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorCustomCookieMatcher(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.POST, URL: "/cookies"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Cookies: sdk.ExpectedCookies.Custom(map[string]sdk.ExpectedStringRule{
					"gox-test-cookie": sdk.Expect.Equal("cookie-value"),
					"debug":           sdk.Expect.NotExists(),
				}),
			},
		},
	})
	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}

func TestTestExecutorConfigSessionDoesNotCallOnCreate(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	calls := 0
	state := rt.state()
	if state == nil || state.Session == nil {
		t.Fatal("session policy is nil")
	}
	state.Session.onCreate = func(session *server.Session) {
		calls++
	}

	executor := NewTestExecutor(rt)
	result, match := executor.Execute(sdk.Config{
		Session: sdk.Session{
			"userID": "123",
		},
	}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/session"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Body:   sdk.ExpectedBody.Contains("user:123"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
	if calls != 0 {
		t.Fatalf("OnCreate calls = %d, want 0 for Config.Session bootstrap", calls)
	}
}

func TestTestExecutorRequestSessionCreationCallsOnCreate(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	calls := 0
	state := rt.state()
	if state == nil || state.Session == nil {
		t.Fatal("session policy is nil")
	}
	state.Session.onCreate = func(session *server.Session) {
		calls++
	}

	executor := NewTestExecutor(rt)
	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"},
			Expected: sdk.NewResponseExpected{
				Status: 200,
				Body:   sdk.ExpectedBody.Contains("Hello Executor"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
	if calls != 1 {
		t.Fatalf("OnCreate calls = %d, want 1 for natural request session creation", calls)
	}
}

func TestTestExecutorBenchmarkModeIgnoresFunctionalMatchers(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	sdk.SetRuntimeContext(sdk.RuntimeContext{Mode: sdk.ModeBench})
	defer sdk.ClearRuntimeContext()

	result, match := executor.Execute(sdk.Config{Warmup: 2, Iterations: 3}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"},
			Expected: sdk.NewResponseExpected{
				Status: 404,
				Headers: sdk.ExpectedHeaders.Contains(sdk.Map{
					"X-Never": "present",
				}),
				Cookies: sdk.ExpectedCookies.Contains(sdk.Map{
					"missing": "cookie",
				}),
				Body: sdk.ExpectedBody.Contains("not in body"),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("benchmark match = %v result = %s", match, result.Error())
	}
	if result.Mode != sdk.ModeBench {
		t.Fatalf("mode = %q, want %q", result.Mode, sdk.ModeBench)
	}
	if result.Benchmark == nil {
		t.Fatal("benchmark result is nil")
	}
	if result.Benchmark.Warmup != 2 || result.Benchmark.Iterations != 3 {
		t.Fatalf("warmup/iterations = %d/%d, want 2/3", result.Benchmark.Warmup, result.Benchmark.Iterations)
	}
	if got := result.Benchmark.Status[200]; got != 3 {
		t.Fatalf("status[200] = %d, want 3", got)
	}
	if result.Benchmark.Status[404] != 0 {
		t.Fatalf("status[404] = %d, want 0", result.Benchmark.Status[404])
	}
	if len(result.Benchmark.Steps) != 1 {
		t.Fatalf("benchmark steps = %d, want 1", len(result.Benchmark.Steps))
	}
	step := result.Benchmark.Steps[0]
	if step.Iterations != 3 {
		t.Fatalf("step iterations = %d, want 3", step.Iterations)
	}
	if step.Status[200] != 3 {
		t.Fatalf("step status[200] = %d, want 3", step.Status[200])
	}
	if step.NsPerOp <= 0 || result.Benchmark.NsPerOp <= 0 {
		t.Fatalf("ns/op not populated: step=%f total=%f", step.NsPerOp, result.Benchmark.NsPerOp)
	}
	if step.BytesRecvPerOp <= 0 || result.Benchmark.BytesRecvPerOp <= 0 {
		t.Fatalf("bytes received/op not populated: step=%f total=%f", step.BytesRecvPerOp, result.Benchmark.BytesRecvPerOp)
	}
	if result.Steps[0].Status.Rule != "" || result.Steps[0].Body.Rule != "" || result.Steps[0].Headers.Rule != "" || result.Steps[0].Cookies.Rule != "" {
		t.Fatalf("benchmark step contains functional matcher data: %#v", result.Steps[0])
	}
	if result.Steps[0].Response.StatusCode != 0 || result.Steps[0].Response.Body != "" {
		t.Fatalf("benchmark step contains response snapshot: %#v", result.Steps[0].Response)
	}
}

func TestTestExecutorBenchmarkDefaultsAndSampleClamp(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.ExecuteBenchmark(sdk.Config{BenchSampleRate: 2}, []sdk.ExecutePipeLine{
		{Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"}},
	})

	if !match || !result.Match {
		t.Fatalf("benchmark match = %v result = %s", match, result.Error())
	}
	if result.Benchmark == nil {
		t.Fatal("benchmark result is nil")
	}
	if result.Benchmark.Warmup != 100 {
		t.Fatalf("warmup = %d, want default 100", result.Benchmark.Warmup)
	}
	if result.Benchmark.Iterations != 1000 {
		t.Fatalf("iterations = %d, want default 1000", result.Benchmark.Iterations)
	}
	if result.Benchmark.SampleRate != 1 {
		t.Fatalf("sample rate = %f, want clamped 1", result.Benchmark.SampleRate)
	}
	if result.Benchmark.AllocationSamples != 1000 {
		t.Fatalf("allocation samples = %d, want 1000", result.Benchmark.AllocationSamples)
	}
	if got := result.Benchmark.Status[200]; got != 1000 {
		t.Fatalf("status[200] = %d, want 1000", got)
	}
}

func TestTestExecutorBenchmarkActionCountsStatus(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.ExecuteBenchmark(sdk.Config{Warmup: 1, Iterations: 4}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewActionRequest{
				URL:    "/index",
				Method: "Sum",
				Params: sdk.ActionParams{10, 20},
			},
			Expected: sdk.NewResponseActionExpected{
				Result: sdk.ExpectedActionResult.Partial(map[string]any{"sum": 999}),
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("benchmark match = %v result = %s", match, result.Error())
	}
	if result.Benchmark == nil {
		t.Fatal("benchmark result is nil")
	}
	if got := result.Benchmark.Status[200]; got != 4 {
		t.Fatalf("status[200] = %d, want 4", got)
	}
	if len(result.Benchmark.Steps) != 1 || result.Benchmark.Steps[0].Type != "action" {
		t.Fatalf("unexpected benchmark steps: %#v", result.Benchmark.Steps)
	}
	if result.Steps[0].Action.Match || result.Steps[0].Action.Result.Rule != "" {
		t.Fatalf("benchmark action should not evaluate action matchers: %#v", result.Steps[0].Action)
	}
}

func TestTestExecutorBenchmarkHandlerAllocs(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.ExecuteBenchmark(sdk.Config{Warmup: 1, Iterations: 5, BenchSampleRate: 1}, []sdk.ExecutePipeLine{
		{Request: sdk.NewRequest{Method: sdk.GET, URL: "/allocs"}},
	})

	if !match || !result.Match {
		t.Fatalf("benchmark match = %v result = %s", match, result.Error())
	}
	if result.Benchmark == nil {
		t.Fatal("benchmark result is nil")
	}
	if result.Benchmark.HandlerAllocsOp <= 0 {
		t.Fatalf("handler_allocs/op = %f, want > 0", result.Benchmark.HandlerAllocsOp)
	}
	if result.Benchmark.HandlerBytesOp <= 0 {
		t.Fatalf("handler_bytes/op = %f, want > 0", result.Benchmark.HandlerBytesOp)
	}
	step := result.Benchmark.Steps[0]
	if step.HandlerAllocsOp <= 0 || step.HandlerBytesOp <= 0 {
		t.Fatalf("step handler alloc metrics not populated: allocs=%f bytes=%f", step.HandlerAllocsOp, step.HandlerBytesOp)
	}
	if step.AllocationSamples != 5 || result.Benchmark.AllocationSamples != 5 {
		t.Fatalf("allocation samples step=%d total=%d, want 5", step.AllocationSamples, result.Benchmark.AllocationSamples)
	}
	if len(result.Benchmark.AllocationSites) > 0 {
		if len(step.AllocationSites) == 0 {
			t.Fatalf("step allocation sites not populated")
		}
		if result.Benchmark.AllocationSites[0].Allocs == 0 || result.Benchmark.AllocationSites[0].Bytes == 0 {
			t.Fatalf("allocation site totals not populated: %#v", result.Benchmark.AllocationSites[0])
		}
		if result.Benchmark.AllocationSites[0].File != "pages/index.gox" || result.Benchmark.AllocationSites[0].Line != 9 {
			t.Fatalf("allocation site = %#v, want pages/index.gox:9", result.Benchmark.AllocationSites[0])
		}
	}
}

func TestBenchmarkHandlerAllocSampling(t *testing.T) {
	if !benchmarkShouldSampleHandlerAllocs(0, 0.5) {
		t.Fatal("iteration 0 should be sampled at 50%")
	}
	if benchmarkShouldSampleHandlerAllocs(1, 0.5) {
		t.Fatal("iteration 1 should not be sampled at 50%")
	}
	if got := benchmarkSampledIterations(10, 0.5); got != 5 {
		t.Fatalf("sampled iterations = %d, want 5", got)
	}
	if got := benchmarkSampledIterations(10, 1); got != 10 {
		t.Fatalf("sampled iterations = %d, want 10", got)
	}
}

func TestBenchmarkTransferBytesIncludeHTTPMetadata(t *testing.T) {
	req, err := buildTestHTTPRequest(sdk.NewRequest{
		Method: sdk.POST,
		URL:    "/index?from=test",
		Headers: sdk.RequestHeaders{
			"X-Bench": "phase3",
		},
		Cookies: sdk.RequestCookies{
			"session": "abc",
		},
		Body: sdk.RequestBody.RawWithType("hello", "text/plain"),
	})
	if err != nil {
		t.Fatal(err)
	}

	bytes := estimateHTTPRequestBytes(req)
	if bytes <= int64(len("hello")) {
		t.Fatalf("request transfer bytes = %d, want more than body size", bytes)
	}
	if bytes < int64(len("POST /index?from=test HTTP/1.1\r\n")) {
		t.Fatalf("request transfer bytes = %d, missing request line", bytes)
	}

	rec := httptest.NewRecorder()
	rec.Header().Set("X-Bench", "phase3")
	rec.WriteHeader(http.StatusCreated)
	_, _ = rec.WriteString("created")
	res := rec.Result()
	defer res.Body.Close()

	responseBytes := estimateHTTPResponseBytes(res, int64(rec.Body.Len()))
	if responseBytes <= int64(rec.Body.Len()) {
		t.Fatalf("response transfer bytes = %d, want more than body size", responseBytes)
	}
}

func TestTestExecutorBenchmarkBypassesRateLimit(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	policy, err := NewRateLimitPolicy(server.RateLimitConfig{
		Enabled: true,
		Rules: []server.RateLimitRule{{
			Name:    "bench-limit",
			Enabled: true,
			Methods: []string{server.RateLimitAll},
			Match:   "*",
			Limits: []server.RateLimitLimit{{
				Name:    "ip",
				Enabled: true,
				By:      []server.RateLimitFilter{server.RateLimitFilterIP},
				Strategy: server.RateLimitFixedWindow{
					Limit:  1,
					Window: time.Hour,
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("NewRateLimitPolicy failed: %v", err)
	}
	state := *rt.state()
	state.RateLimit = policy
	rt.runtimeState.Store(&state)

	executor := NewTestExecutor(rt)
	result, match := executor.ExecuteBenchmark(sdk.Config{Warmup: 2, Iterations: 5}, []sdk.ExecutePipeLine{
		{Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"}},
	})

	if !match || !result.Match {
		t.Fatalf("benchmark match = %v result = %s", match, result.Error())
	}
	if result.Benchmark == nil {
		t.Fatal("benchmark result is nil")
	}
	if got := result.Benchmark.Status[200]; got != 5 {
		t.Fatalf("status[200] = %d, want 5; status=%#v", got, result.Benchmark.Status)
	}
	if got := result.Benchmark.Status[429]; got != 0 {
		t.Fatalf("benchmark should bypass rateLimit, status[429] = %d", got)
	}
}

func TestTestExecutorBenchmarkActionHandlerAllocsAreStrict(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.ExecuteBenchmark(sdk.Config{Warmup: 1, Iterations: 5, BenchSampleRate: 1}, []sdk.ExecutePipeLine{
		{Request: sdk.NewActionRequest{URL: "/action-allocs", Method: "Alloc", Params: sdk.ActionParams{"y"}}},
	})

	if !match || !result.Match {
		t.Fatalf("benchmark match = %v result = %s", match, result.Error())
	}
	if result.Benchmark == nil || len(result.Benchmark.Steps) != 1 {
		t.Fatalf("benchmark result missing: %#v", result.Benchmark)
	}
	step := result.Benchmark.Steps[0]
	if step.HandlerAllocsOp <= 0 {
		t.Fatalf("handler_allocs/op = %f, want > 0", step.HandlerAllocsOp)
	}
	if step.HandlerBytesOp <= 0 {
		t.Fatalf("handler_bytes/op = %f, want > 0", step.HandlerBytesOp)
	}
	if step.HandlerBytesOp >= 4096 {
		t.Fatalf("handler_bytes/op = %f, wrapper allocations leaked into action measurement", step.HandlerBytesOp)
	}
}

func TestTestExecutorBenchmarkMultiStepAggregatesGlobalAndStepMetrics(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.ExecuteBenchmark(sdk.Config{Warmup: 1, Iterations: 4, BenchSampleRate: 0.5}, []sdk.ExecutePipeLine{
		{Request: sdk.NewRequest{Method: sdk.GET, URL: "/allocs"}},
		{Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"}},
	})

	if !match || !result.Match {
		t.Fatalf("benchmark match = %v result = %s", match, result.Error())
	}
	if result.Benchmark == nil {
		t.Fatal("benchmark result is nil")
	}
	bench := result.Benchmark
	if len(bench.Steps) != 2 {
		t.Fatalf("benchmark steps = %d, want 2", len(bench.Steps))
	}
	if len(result.Steps) != 2 {
		t.Fatalf("execute steps = %d, want 2", len(result.Steps))
	}
	if got := bench.Status[200]; got != 8 {
		t.Fatalf("status[200] = %d, want 8", got)
	}
	if bench.AllocationSamples != 4 {
		t.Fatalf("allocation samples = %d, want 4", bench.AllocationSamples)
	}
	if bench.Steps[0].AllocationSamples != 2 || bench.Steps[1].AllocationSamples != 2 {
		t.Fatalf("step allocation samples = %d/%d, want 2/2", bench.Steps[0].AllocationSamples, bench.Steps[1].AllocationSamples)
	}
	if bench.Steps[0].HandlerAllocsOp <= 0 {
		t.Fatalf("first step handler_allocs/op = %f, want > 0", bench.Steps[0].HandlerAllocsOp)
	}
	if bench.Steps[1].HandlerAllocsOp < 0 {
		t.Fatalf("second step handler_allocs/op = %f, want non-negative", bench.Steps[1].HandlerAllocsOp)
	}
	expectedGlobalAllocs := (bench.Steps[0].HandlerAllocsOp + bench.Steps[1].HandlerAllocsOp) / 2
	if bench.HandlerAllocsOp != expectedGlobalAllocs {
		t.Fatalf("global handler_allocs/op = %f, want weighted multi-step average %f", bench.HandlerAllocsOp, expectedGlobalAllocs)
	}
	if bench.NsPerOp <= 0 || bench.Steps[0].NsPerOp <= 0 || bench.Steps[1].NsPerOp <= 0 {
		t.Fatalf("ns/op not populated: global=%f step1=%f step2=%f", bench.NsPerOp, bench.Steps[0].NsPerOp, bench.Steps[1].NsPerOp)
	}
}

func TestTestExecutorBenchmarkRepeatsWholePipelineInOrder(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	executor := NewTestExecutor(rt)

	result, match := executor.ExecuteBenchmark(sdk.Config{Warmup: 1, Iterations: 4, BenchSampleRate: 0}, []sdk.ExecutePipeLine{
		{Request: sdk.NewRequest{Method: sdk.POST, URL: "/pipeline"}},
		{Request: sdk.NewRequest{Method: sdk.GET, URL: "/pipeline"}},
	})

	if !match || !result.Match {
		t.Fatalf("benchmark match = %v result = %s", match, result.Error())
	}
	if result.Benchmark == nil || len(result.Benchmark.Steps) != 2 {
		t.Fatalf("benchmark result missing steps: %#v", result.Benchmark)
	}
	if got := result.Benchmark.Status[200]; got != 8 {
		t.Fatalf("status[200] = %d, want 8; status=%v", got, result.Benchmark.Status)
	}
	if got := result.Benchmark.Status[409]; got != 0 {
		t.Fatalf("status[409] = %d, want 0; benchmark did not repeat pipeline in order", got)
	}
	if got := result.Benchmark.Steps[0].Status[200]; got != 4 {
		t.Fatalf("step1 status[200] = %d, want 4", got)
	}
	if got := result.Benchmark.Steps[1].Status[200]; got != 4 {
		t.Fatalf("step2 status[200] = %d, want 4", got)
	}
}

func TestTestExecutorRequestSessionOnCreatePanicReturnsInternalServerError(t *testing.T) {
	rt := newTestExecutorRuntime(t)
	state := rt.state()
	if state == nil || state.Session == nil {
		t.Fatal("session policy is nil")
	}
	state.Session.onCreate = func(*server.Session) {
		panic("session create panic")
	}

	executor := NewTestExecutor(rt)
	result, match := executor.Execute(sdk.Config{}, []sdk.ExecutePipeLine{
		{
			Request: sdk.NewRequest{Method: sdk.GET, URL: "/index"},
			Expected: sdk.NewResponseExpected{
				Status: http.StatusInternalServerError,
			},
		},
	})

	if !match || !result.Match {
		t.Fatalf("match = %v result = %s", match, result.Error())
	}
}
