package serverruntime

import (
	"crypto/rand"
	"errors"
	"sync"
	"time"
)

const clusterCorrelationSize = 18

var (
	errClusterRequestClosed   = errors.New("distributed socket request coordinator is closed")
	errClusterRequestReloaded = errors.New("distributed socket request canceled by cluster reload")
	errClusterRequestTimeout  = errors.New("distributed socket request timed out")
	errClusterCorrelation     = errors.New("distributed socket correlation ID is invalid")
)

type clusterRequestResult struct {
	payload []byte
	err     error
}

type clusterPendingRequest struct {
	key    [clusterCorrelationSize]byte
	result chan clusterRequestResult
}

// clusterRequestCoordinator owns requests initiated by this runtime. A
// correlation is removed before its result is delivered, so a response,
// timeout, cancellation, and shutdown can have exactly one winner.
type clusterRequestCoordinator struct {
	mu      sync.Mutex
	closed  bool
	pending map[[clusterCorrelationSize]byte]*clusterPendingRequest
}

func newClusterRequestCoordinator() *clusterRequestCoordinator {
	return &clusterRequestCoordinator{pending: make(map[[clusterCorrelationSize]byte]*clusterPendingRequest)}
}

func (coordinator *clusterRequestCoordinator) register() ([]byte, *clusterPendingRequest, error) {
	if coordinator == nil {
		return nil, nil, errClusterRequestClosed
	}
	for {
		var key [clusterCorrelationSize]byte
		if _, err := rand.Read(key[:]); err != nil {
			return nil, nil, err
		}
		pending, added, err := coordinator.registerKey(key)
		if err != nil {
			return nil, nil, err
		}
		if added {
			return append([]byte(nil), key[:]...), pending, nil
		}
	}
}

func (coordinator *clusterRequestCoordinator) registerKey(key [clusterCorrelationSize]byte) (*clusterPendingRequest, bool, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.closed {
		return nil, false, errClusterRequestClosed
	}
	if _, exists := coordinator.pending[key]; exists {
		return nil, false, nil
	}
	pending := &clusterPendingRequest{key: key, result: make(chan clusterRequestResult, 1)}
	coordinator.pending[key] = pending
	return pending, true, nil
}

func (coordinator *clusterRequestCoordinator) deliver(correlation, payload []byte) bool {
	key, ok := clusterCorrelationKey(correlation)
	if !ok {
		return false
	}
	pending := coordinator.take(key, nil)
	if pending == nil {
		return false
	}
	pending.result <- clusterRequestResult{payload: append([]byte(nil), payload...)}
	return true
}

func (coordinator *clusterRequestCoordinator) cancel(pending *clusterPendingRequest, cause error) bool {
	if pending == nil {
		return false
	}
	removed := coordinator.take(pending.key, pending)
	if removed == nil {
		return false
	}
	removed.result <- clusterRequestResult{err: cause}
	return true
}

func (coordinator *clusterRequestCoordinator) wait(pending *clusterPendingRequest, timeout time.Duration) clusterRequestResult {
	if pending == nil {
		return clusterRequestResult{err: errClusterCorrelation}
	}
	if timeout <= 0 {
		if coordinator.cancel(pending, errClusterRequestTimeout) {
			return <-pending.result
		}
		return <-pending.result
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-pending.result:
		return result
	case <-timer.C:
		if coordinator.cancel(pending, errClusterRequestTimeout) {
			return <-pending.result
		}
		return <-pending.result
	}
}

func (coordinator *clusterRequestCoordinator) take(key [clusterCorrelationSize]byte, expected *clusterPendingRequest) *clusterPendingRequest {
	if coordinator == nil {
		return nil
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	pending := coordinator.pending[key]
	if pending == nil || (expected != nil && pending != expected) {
		return nil
	}
	delete(coordinator.pending, key)
	return pending
}

func (coordinator *clusterRequestCoordinator) close() {
	if coordinator == nil {
		return
	}
	coordinator.mu.Lock()
	if coordinator.closed {
		coordinator.mu.Unlock()
		return
	}
	coordinator.closed = true
	pending := coordinator.pending
	coordinator.pending = make(map[[clusterCorrelationSize]byte]*clusterPendingRequest)
	coordinator.mu.Unlock()
	for _, request := range pending {
		request.result <- clusterRequestResult{err: errClusterRequestClosed}
	}
}

func (coordinator *clusterRequestCoordinator) cancelAll(cause error) {
	if coordinator == nil {
		return
	}
	coordinator.mu.Lock()
	pending := coordinator.pending
	coordinator.pending = make(map[[clusterCorrelationSize]byte]*clusterPendingRequest)
	coordinator.mu.Unlock()
	for _, request := range pending {
		request.result <- clusterRequestResult{err: cause}
	}
}

func (coordinator *clusterRequestCoordinator) count() int {
	if coordinator == nil {
		return 0
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return len(coordinator.pending)
}

func clusterCorrelationKey(value []byte) ([clusterCorrelationSize]byte, bool) {
	var key [clusterCorrelationSize]byte
	if len(value) != len(key) {
		return key, false
	}
	copy(key[:], value)
	return key, true
}

func (r *Runtime) clusterRequestCoordinator() *clusterRequestCoordinator {
	if r == nil {
		return nil
	}
	r.clusterRequestsOnce.Do(func() {
		r.clusterRequests = newClusterRequestCoordinator()
	})
	return r.clusterRequests
}
