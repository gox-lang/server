package serverruntime

import (
	"errors"
	"testing"

	"github.com/gox-lang/gox/server"
)

func TestRuntimeRunOnStartServer(t *testing.T) {
	called := 0
	rt := &Runtime{
		Config: server.Config{
			OnStartServer: func() error {
				called++
				return nil
			},
		},
	}

	if err := rt.runOnStartServer(); err != nil {
		t.Fatalf("runOnStartServer failed: %v", err)
	}
	if called != 1 {
		t.Fatalf("OnStartServer called %d times, want 1", called)
	}
}

func TestRuntimeRunOnStartServerError(t *testing.T) {
	want := errors.New("start failed")
	rt := &Runtime{
		Config: server.Config{
			OnStartServer: func() error { return want },
		},
	}

	if err := rt.runOnStartServer(); !errors.Is(err, want) {
		t.Fatalf("runOnStartServer error = %v, want %v", err, want)
	}
}

func TestRuntimeCloseRunsOnStopServerOnce(t *testing.T) {
	called := 0
	rt := &Runtime{
		Config: server.Config{
			OnStopServer: func() error {
				called++
				return nil
			},
		},
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
	if called != 1 {
		t.Fatalf("OnStopServer called %d times, want 1", called)
	}
}

func TestRuntimeCloseSkipsOnStopServerWhenRequested(t *testing.T) {
	called := 0
	rt := &Runtime{
		Config: server.Config{
			OnStopServer: func() error {
				called++
				return nil
			},
		},
	}

	if err := rt.close(false); err != nil {
		t.Fatalf("close(false) failed: %v", err)
	}
	if called != 0 {
		t.Fatalf("OnStopServer called %d times, want 0", called)
	}
}

func TestRuntimeRunOnStartServerPanicFailsStartupWithoutEscaping(t *testing.T) {
	rt := &Runtime{
		Config: server.Config{
			OnStartServer: func() error { panic("start panic") },
		},
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("runOnStartServer panic escaped: %v", recovered)
		}
	}()

	if err := rt.runOnStartServer(); err == nil {
		t.Fatal("runOnStartServer error = nil, want startup failure after panic")
	}
}

func TestRuntimeCloseOnStopServerPanicContinuesCleanup(t *testing.T) {
	unbound := false
	rt := &Runtime{
		Config: server.Config{
			OnStopServer: func() error { panic("stop panic") },
		},
		unbindServerInfo: func() {
			unbound = true
		},
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Close panic escaped: %v", recovered)
		}
	}()

	if err := rt.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if !unbound {
		t.Fatal("cleanup after OnStopServer was not executed")
	}
}
