package serverruntime

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gox-lang/server/internal/hotreload"
)

func TestRuntimeRestartRequestAcceptsOnlyFirstRequest(t *testing.T) {
	runtime := &Runtime{}
	first := RestartRequest{
		Reason:         RestartReasonGoSourceChanged,
		ChangedPath:    "/project/billing/billing.go",
		ChangedRelPath: "billing/billing.go",
		AffectedEntries: []hotreload.ReloadEntry{{
			Kind:         hotreload.ReloadPage,
			EntryPath:    "pages/index.gox",
			EntryAbsPath: "/project/pages/index.gox",
		}},
	}
	if !runtime.requestRestart(first) {
		t.Fatal("first restart request was rejected")
	}
	if runtime.requestRestart(RestartRequest{ChangedPath: "/project/profile/profile.go"}) {
		t.Fatal("second restart request was accepted")
	}

	request := <-runtime.restartRequests()
	if request.Reason != first.Reason || request.ChangedPath != first.ChangedPath || request.ChangedRelPath != first.ChangedRelPath {
		t.Fatalf("restart request = %#v, want %#v", request, first)
	}
	if len(request.AffectedEntries) != 1 || request.AffectedEntries[0] != first.AffectedEntries[0] {
		t.Fatalf("affected entries = %#v", request.AffectedEntries)
	}
	select {
	case unexpected := <-runtime.restartRequests():
		t.Fatalf("received duplicate restart request: %#v", unexpected)
	default:
	}
}

func TestRuntimeRestartRequestCopiesAffectedEntries(t *testing.T) {
	runtime := &Runtime{}
	entries := []hotreload.ReloadEntry{{EntryPath: "pages/index.gox"}}
	if !runtime.requestRestart(RestartRequest{AffectedEntries: entries}) {
		t.Fatal("restart request was rejected")
	}
	entries[0].EntryPath = "pages/changed.gox"

	request := <-runtime.restartRequests()
	if got := request.AffectedEntries[0].EntryPath; got != "pages/index.gox" {
		t.Fatalf("affected entry changed after request: %q", got)
	}
}

func TestRuntimeRestartRequestHasSingleConcurrentWinner(t *testing.T) {
	runtime := &Runtime{}
	const requests = 64
	var accepted atomic.Int32
	var wait sync.WaitGroup
	wait.Add(requests)
	for index := 0; index < requests; index++ {
		go func(index int) {
			defer wait.Done()
			if runtime.requestRestart(RestartRequest{ChangedPath: fmt.Sprintf("/project/package/file-%d.go", index)}) {
				accepted.Add(1)
			}
		}(index)
	}
	wait.Wait()

	if got := accepted.Load(); got != 1 {
		t.Fatalf("accepted requests = %d, want 1", got)
	}
	request := <-runtime.restartRequests()
	if request.ChangedPath == "" {
		t.Fatal("winning restart request lost its changed path")
	}
}
