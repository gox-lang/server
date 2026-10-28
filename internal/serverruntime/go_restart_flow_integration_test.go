package serverruntime

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/watcher"

	"github.com/gox-lang/gox"
)

func TestGoSourceChangeCompletesOrderedRuntimeRestart(t *testing.T) {
	root := t.TempDir()
	packageDir := filepath.Join(root, "billing")
	if err := os.MkdirAll(packageDir, 0755); err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(packageDir, "billing.go")
	if err := os.WriteFile(changed, []byte("package billing\n"), 0644); err != nil {
		t.Fatal(err)
	}

	entry := filepath.Join(root, "pages", "index.gox")
	index := hotreload.NewIndex()
	index.RegisterCompileResult(hotreload.ReloadPage, &goxcompiler.CompileResult{
		EntryPath:    "pages/index.gox",
		EntryAbsPath: entry,
		GoPackages: []goxcompiler.CompileGoPackage{{
			ImportPath: "noti-milo/billing",
			Dir:        packageDir,
			Files:      []string{changed},
		}},
	})

	warmup := LoadDynamicWarmupCache(root)
	runtime := &Runtime{
		ProjectRoot:   root,
		hotReload:     index,
		dynamicWarmup: warmup,
		goxRoutes: map[string]*GoxRouteRuntime{
			"/index": {RoutePath: "/index", AbsPath: entry},
		},
	}
	closed := make(chan gox.WSCloseCode, 1)
	socket := &gox.Socket{}
	gox.BindSocketDisconnect(socket, func(code gox.WSCloseCode, _ string) { closed <- code })
	runtime.webSocketsOnce.Do(func() {
		runtime.webSockets = newWebSocketRegistry()
		runtime.webSockets.byID["socket"] = socket
	})

	fileWatcher, err := watcher.New(root, watcher.WithDebounce(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer fileWatcher.Close()
	if err := runtime.subscribeHotReloadWatcher(fileWatcher); err != nil {
		t.Fatal(err)
	}
	if err := fileWatcher.WatchRecursive("."); err != nil {
		t.Fatal(err)
	}
	fileWatcher.Start()
	runtime.watcher = fileWatcher

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.Listener.Addr().String()
	defer server.Close()

	// A rejected edit must never reach shutdown: the old HTTP listener and
	// sockets remain usable until a safe candidate is available.
	if err := os.WriteFile(changed, []byte("package billing\nfunc Work(){go func(){panic(\"unsafe\")}()}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-runtime.restartRequests():
		t.Fatalf("unsafe edit requested restart: %#v", request)
	case <-time.After(150 * time.Millisecond):
	}
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatalf("active HTTP server stopped after rejected edit: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status = %d", response.StatusCode)
	}
	select {
	case <-closed:
		t.Fatal("rejected edit disconnected active sockets")
	default:
	}

	if err := os.WriteFile(changed, []byte("package billing\n\nconst Version = 2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var request RestartRequest
	select {
	case request = <-runtime.restartRequests():
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not request restart after Go source change")
	}
	restartErr := runtime.finishRuntimeRestart(server.Config, request)
	restarted, ok := RestartRequestFromError(restartErr)
	if !ok || restarted.ChangedRelPath != "billing/billing.go" {
		t.Fatalf("restart result = %#v, %v, error %v", restarted, ok, restartErr)
	}

	select {
	case code := <-closed:
		if code != gox.WSCloseRestart {
			t.Fatalf("WebSocket close code = %d, want %d", code, gox.WSCloseRestart)
		}
	default:
		t.Fatal("WebSocket was not closed before restart")
	}
	selection := warmup.WarmupPaths()
	if len(selection.Pages) != 1 || selection.Pages[0] != "/index" {
		t.Fatalf("restart warmup pages = %#v", selection.Pages)
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("HTTP port was not released: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
