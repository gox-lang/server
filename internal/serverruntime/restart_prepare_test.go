package serverruntime

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/hotreload"

	"github.com/gox-lang/gox"
)

func TestPrepareRuntimeRestartPersistsAffectedRouteClosesSocketsAndReleasesPort(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "pages", "billing.gox")
	affected := &GoxRouteRuntime{RoutePath: "/billing", AbsPath: entry}
	popular := &GoxRouteRuntime{RoutePath: "/popular", AbsPath: filepath.Join(root, "pages", "popular.gox")}
	popular.WarmupHits.Add(50)

	runtime := &Runtime{
		dynamicWarmup: LoadDynamicWarmupCache(root),
		goxRoutes: map[string]*GoxRouteRuntime{
			"/billing": affected,
			"/popular": popular,
		},
	}
	restartCode := make(chan gox.WSCloseCode, 1)
	socket := &gox.Socket{}
	gox.BindSocketDisconnect(socket, func(code gox.WSCloseCode, _ string) {
		restartCode <- code
	})
	runtime.webSocketsOnce.Do(func() {
		runtime.webSockets = newWebSocketRegistry()
		runtime.webSockets.byID["socket"] = socket
	})

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.Listener.Addr().String()
	t.Cleanup(server.Close)
	err := runtime.prepareRuntimeRestart(server.Config, RestartRequest{
		Reason: RestartReasonGoSourceChanged,
		AffectedEntries: []hotreload.ReloadEntry{{
			Kind:         hotreload.ReloadPage,
			EntryAbsPath: entry,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-restartCode:
		if code != gox.WSCloseRestart {
			t.Fatalf("WebSocket close code = %d, want %d", code, gox.WSCloseRestart)
		}
	default:
		t.Fatal("local WebSocket was not closed for restart")
	}
	warmup := runtime.dynamicWarmup.WarmupPaths()
	if len(warmup.Pages) == 0 || warmup.Pages[0] != "/billing" {
		t.Fatalf("restart warmup pages = %#v, want affected route first", warmup.Pages)
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("HTTP port was not released: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPromoteRestartWarmupEntriesIncludesAffectedChannel(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "websocket", "billing.gox")
	route := &GoxRouteRuntime{
		RoutePath: channelRoutePrefix + "Billing",
		AbsPath:   entry,
	}
	runtime := &Runtime{
		channelDefinitions: map[string]*channelDefinitionRuntime{
			"Billing": {route: route},
		},
	}

	snapshot := runtime.promoteRestartWarmupEntries(dynamicWarmupSnapshot{}, []hotreload.ReloadEntry{{
		Kind:         hotreload.ReloadPage,
		EntryAbsPath: entry,
	}})
	if len(snapshot.Channels) != 1 || snapshot.Channels[0].Path != route.RoutePath {
		t.Fatalf("restart warmup channels = %#v, want %q", snapshot.Channels, route.RoutePath)
	}
	if len(snapshot.Pages) != 0 {
		t.Fatalf("restart warmup pages = %#v, want none", snapshot.Pages)
	}
}

func TestRestartRequestedErrorPreservesRequest(t *testing.T) {
	want := RestartRequest{
		Reason:         RestartReasonGoSourceChanged,
		ChangedRelPath: "billing/billing.go",
	}
	got, ok := RestartRequestFromError(&RestartRequestedError{Request: want})
	if !ok || got.Reason != want.Reason || got.ChangedRelPath != want.ChangedRelPath {
		t.Fatalf("RestartRequestFromError = %#v, %v", got, ok)
	}
}
