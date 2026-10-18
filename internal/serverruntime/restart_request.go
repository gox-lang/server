package serverruntime

import (
	"errors"
	"fmt"
	"sync"

	"github.com/gox-lang/server/internal/hotreload"
)

type RestartReason string

const RestartReasonGoSourceChanged RestartReason = "go-source-changed"
const RestartReasonHTTPConfigChanged RestartReason = "http-config-changed"

type RestartRequest struct {
	Reason          RestartReason
	ChangedPath     string
	ChangedRelPath  string
	AffectedEntries []hotreload.ReloadEntry
}

type RestartRequestedError struct {
	Request RestartRequest
}

func (err *RestartRequestedError) Error() string {
	if err == nil {
		return "Gox restart requested"
	}
	if err.Request.ChangedRelPath != "" {
		return fmt.Sprintf("Gox restart requested after %s", err.Request.ChangedRelPath)
	}
	return "Gox restart requested"
}

func RestartRequestFromError(err error) (RestartRequest, bool) {
	var restart *RestartRequestedError
	if !errors.As(err, &restart) || restart == nil {
		return RestartRequest{}, false
	}
	return restart.Request, true
}

type runtimeRestartCoordinator struct {
	mu        sync.Mutex
	requested bool
	requests  chan RestartRequest
}

func newRuntimeRestartCoordinator() *runtimeRestartCoordinator {
	return &runtimeRestartCoordinator{requests: make(chan RestartRequest, 1)}
}

func (coordinator *runtimeRestartCoordinator) request(request RestartRequest) bool {
	if coordinator == nil {
		return false
	}

	request.AffectedEntries = append([]hotreload.ReloadEntry(nil), request.AffectedEntries...)

	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.requested {
		return false
	}
	coordinator.requested = true
	coordinator.requests <- request
	return true
}

func (coordinator *runtimeRestartCoordinator) channel() <-chan RestartRequest {
	if coordinator == nil {
		return nil
	}
	return coordinator.requests
}

func (r *Runtime) runtimeRestartCoordinator() *runtimeRestartCoordinator {
	if r == nil {
		return nil
	}
	r.restartOnce.Do(func() {
		r.restartCoordinator = newRuntimeRestartCoordinator()
	})
	return r.restartCoordinator
}

func (r *Runtime) requestRestart(request RestartRequest) bool {
	coordinator := r.runtimeRestartCoordinator()
	return coordinator != nil && coordinator.request(request)
}

func (r *Runtime) restartRequests() <-chan RestartRequest {
	coordinator := r.runtimeRestartCoordinator()
	if coordinator == nil {
		return nil
	}
	return coordinator.channel()
}
