package serverruntime

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/toolchain"
	"github.com/gox-lang/server/internal/watcher"
)

func TestGoSourcePreflightBuildsDependentsWithoutLoading(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("plugins unavailable")
	}
	root := t.TempDir()
	write := func(rel, source string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		write(name, string(data))
	}
	write("gox.json", "{}")
	changed := filepath.Join(root, "billing", "billing.go")
	write("billing/billing.go", `package billing
func Send(string) {}
func init(){panic("preflight must not execute init")}
`)
	config := `module config
import (
 "github.com/gox-lang/gox/server"
 "github.com/gox-lang/server/billing"
)
func SetGoxConfig() server.Config {
 var send func(string) = billing.Send
 _ = send
 panic("preflight must not execute configuration")
}
`
	write("app/config.gox", config)
	write("pages/index.gox", `module index
import (
 "github.com/gox-lang/server/billing"
 "github.com/gox-lang/gox"
)
func GET(ctx *gox.Context) gox.Page {
 billing.Send("test")
 return <p>Working</p>
}
`)
	index := hotreload.NewIndex()
	entries := []hotreload.ReloadEntry{
		{Kind: hotreload.ReloadConfig, EntryPath: "app/config.gox", EntryAbsPath: filepath.Join(root, "app", "config.gox")},
		{Kind: hotreload.ReloadPage, EntryPath: "pages/index.gox", EntryAbsPath: filepath.Join(root, "pages", "index.gox")},
	}
	for _, entry := range entries {
		index.RegisterCompileResult(entry.Kind, &goxcompiler.CompileResult{
			EntryPath: entry.EntryPath, EntryAbsPath: entry.EntryAbsPath,
			GoPackages: []goxcompiler.CompileGoPackage{{ImportPath: "github.com/gox-lang/server/billing", Dir: filepath.Dir(changed), Files: []string{changed}}},
		})
	}
	r := &Runtime{ProjectRoot: root, hotReload: index, paths: toolchain.Paths{GoExec: filepath.Join(runtime.GOROOT(), "bin", "go"), GoRoot: runtime.GOROOT()}}
	event := watcher.Event{Path: changed, Rel: "billing/billing.go", Op: watcher.OpWrite}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "active") }))
	defer server.Close()
	assertRejected := func() {
		t.Helper()
		r.handleGoSourceHotReloadEvent(event)
		select {
		case req := <-r.restartRequests():
			t.Fatalf("broken candidate requested restart: %+v", req)
		default:
		}
		response, err := server.Client().Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatal("active server unavailable")
		}
		if candidates, _ := filepath.Glob(filepath.Join(root, ".build", "restart-check-*")); len(candidates) != 0 {
			t.Fatal("candidate artifacts leaked")
		}
	}
	// This Go package compiles, but its consumers do not: the original failure.
	write("billing/billing.go", `package billing
func Send(string,int) {}
`)
	assertRejected()
	// Fix configuration only: a failing page must still veto restart.
	write("app/config.gox", strings.Replace(config, "func(string)", "func(string,int)", 1))
	assertRejected()
	write("app/config.gox", config)
	write("billing/billing.go", `package billing
func Send(string) {}
func init(){panic("not loaded")}
`)
	r.handleHotReloadEvent(watcher.Event{Path: entries[0].EntryAbsPath, Rel: "app/config.gox", Op: watcher.OpWrite})
	select {
	case req := <-r.restartRequests():
		if len(req.AffectedEntries) != 2 {
			t.Fatalf("affected entries: %+v", req)
		}
	default:
		t.Fatal("corrected sources did not restart")
	}
	if _, err := os.Stat(filepath.Join(root, "client")); !os.IsNotExist(err) {
		t.Fatal("validation published client assets")
	}
}

func TestGoSourcePreflightRetainsFailuresAndRejectsChangedSources(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "code.go")
	if err := os.WriteFile(source, []byte(`package code
`), 0644); err != nil {
		t.Fatal(err)
	}
	first := hotreload.ReloadEntry{EntryAbsPath: filepath.Join(root, "a.gox")}
	second := hotreload.ReloadEntry{EntryAbsPath: filepath.Join(root, "b.gox")}
	r := &Runtime{ProjectRoot: root}
	r.goPreflight.validate = func([]hotreload.ReloadEntry) error { return errors.New("compile failed") }
	if _, err := r.validateGoSourceRestart([]hotreload.ReloadEntry{first}); err == nil {
		t.Fatal("failure accepted")
	}
	r.goPreflight.validate = func(entries []hotreload.ReloadEntry) error {
		if len(entries) != 2 {
			t.Fatal("lost previously failed dependency")
		}
		return os.WriteFile(source, []byte(`package code
const Changed=true
`), 0644)
	}
	if _, err := r.validateGoSourceRestart([]hotreload.ReloadEntry{second}); err == nil {
		t.Fatal("mixed source generation accepted")
	}
	r.goPreflight.validate = func(entries []hotreload.ReloadEntry) error { return nil }
	if entries, err := r.validateGoSourceRestart(nil); err != nil || len(entries) != 2 {
		t.Fatalf("retry: %v %v", entries, err)
	}
}

func TestGoSourcePreflightContainsCompilerPanic(t *testing.T) {
	r := &Runtime{ProjectRoot: t.TempDir()}
	r.goPreflight.validate = func([]hotreload.ReloadEntry) error { panic("compiler failure") }
	if _, err := r.validateGoSourceRestart([]hotreload.ReloadEntry{{EntryAbsPath: "entry.gox"}}); err == nil {
		t.Fatal("compiler panic did not become a controlled error")
	}
	r.goPreflight.validate = func([]hotreload.ReloadEntry) error { return nil }
	if _, err := r.validateGoSourceRestart(nil); err != nil {
		t.Fatal(err)
	}
}

func TestGoSourcePreflightConcurrentEditsInvalidateEarlierCandidate(t *testing.T) {
	r := &Runtime{ProjectRoot: t.TempDir()}
	entered, release := make(chan struct{}), make(chan struct{})
	first, second := make(chan error, 1), make(chan error, 1)
	calls := 0
	r.goPreflight.validate = func(entries []hotreload.ReloadEntry) error {
		calls++
		if calls == 1 {
			close(entered)
			<-release
		} else if len(entries) != 2 {
			return errors.New("missing concurrent dependency")
		}
		return nil
	}
	go func() {
		_, err := r.validateGoSourceRestart([]hotreload.ReloadEntry{{EntryAbsPath: "a.gox"}})
		first <- err
	}()
	<-entered
	go func() {
		_, err := r.validateGoSourceRestart([]hotreload.ReloadEntry{{EntryAbsPath: "b.gox"}})
		second <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.goPreflight.mu.Lock()
		generation := r.goPreflight.generation
		r.goPreflight.mu.Unlock()
		if generation == 2 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("concurrent event not registered")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-first; err == nil {
		t.Fatal("stale candidate accepted")
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}
