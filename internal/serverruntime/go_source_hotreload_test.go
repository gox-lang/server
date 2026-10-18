package serverruntime

import (
	"path/filepath"
	"testing"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/hotreload"
	"github.com/gox-lang/server/internal/watcher"
)

func TestGoSourceHotReloadRequestsRestartForAffectedEntries(t *testing.T) {
	for _, op := range []watcher.Op{watcher.OpCreate, watcher.OpWrite, watcher.OpRemove, watcher.OpRename} {
		t.Run(op.String(), func(t *testing.T) {
			root := t.TempDir()
			entry := filepath.Join(root, "pages", "index.gox")
			packageDir := filepath.Join(root, "billing")
			changed := filepath.Join(packageDir, "invoice.go")
			index := hotreload.NewIndex()
			index.RegisterCompileResult(hotreload.ReloadPage, &goxcompiler.CompileResult{
				EntryPath:    "pages/index.gox",
				EntryAbsPath: entry,
				GoPackages: []goxcompiler.CompileGoPackage{{
					ImportPath: "noti-milo/billing",
					Dir:        packageDir,
					Files:      []string{filepath.Join(packageDir, "billing.go")},
				}},
			})
			runtime := &Runtime{ProjectRoot: root, hotReload: index}

			runtime.handleGoSourceHotReloadEvent(watcher.Event{
				Path: changed,
				Rel:  "billing/invoice.go",
				Op:   op,
			})

			var request RestartRequest
			select {
			case request = <-runtime.restartRequests():
			default:
				t.Fatal("Go source change did not request a restart")
			}
			if request.Reason != RestartReasonGoSourceChanged {
				t.Fatalf("restart reason = %q", request.Reason)
			}
			if request.ChangedPath != changed || request.ChangedRelPath != "billing/invoice.go" {
				t.Fatalf("changed source = %q (%q)", request.ChangedPath, request.ChangedRelPath)
			}
			if len(request.AffectedEntries) != 1 || request.AffectedEntries[0].EntryAbsPath != entry {
				t.Fatalf("affected entries = %#v", request.AffectedEntries)
			}
		})
	}
}

func TestGoSourceHotReloadDoesNotRestartForUnrelatedPackage(t *testing.T) {
	root := t.TempDir()
	runtime := &Runtime{ProjectRoot: root, hotReload: hotreload.NewIndex()}
	runtime.handleGoSourceHotReloadEvent(watcher.Event{
		Path: filepath.Join(root, "unrelated", "file.go"),
		Rel:  "unrelated/file.go",
		Op:   watcher.OpWrite,
	})

	select {
	case request := <-runtime.restartRequests():
		t.Fatalf("unexpected restart request: %#v", request)
	default:
	}
}

func TestShouldIgnoreGoSourceHotReloadEvent(t *testing.T) {
	ignored := []watcher.Event{
		{Rel: "billing/billing_test.go", Op: watcher.OpWrite},
		{Rel: ".build/pages/index/generated.go", Op: watcher.OpWrite},
		{Rel: ".git/generated.go", Op: watcher.OpWrite},
		{Rel: "node_modules/package/file.go", Op: watcher.OpWrite},
		{Rel: ".toolchain/go/src/runtime/runtime.go", Op: watcher.OpWrite},
		{Rel: "vendor/module/file.go", Op: watcher.OpWrite},
		{Rel: "billing/billing.go", Op: watcher.OpChmod},
		{Rel: "billing/billing.go", Op: watcher.OpUnknown},
		{Rel: "billing", Op: watcher.OpWrite, IsDir: true},
	}
	for _, event := range ignored {
		if !shouldIgnoreGoSourceHotReloadEvent(event) {
			t.Fatalf("event was not ignored: %#v", event)
		}
	}

	for _, event := range []watcher.Event{
		{Rel: "billing/billing.go", Op: watcher.OpCreate},
		{Rel: "billing/billing.go", Op: watcher.OpWrite},
		{Rel: "billing/billing.go", Op: watcher.OpRemove},
		{Rel: "billing/billing.go", Op: watcher.OpRename},
	} {
		if shouldIgnoreGoSourceHotReloadEvent(event) {
			t.Fatalf("project Go source event was ignored: %#v", event)
		}
	}
}
