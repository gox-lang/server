package serverruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

func TestRuntimeSessionRegistryGetsSavesAndDestroysExistingSession(t *testing.T) {
	storage, err := server.NewMemorySessionAdapter().OpenSessionStorage(context.Background(), server.SessionStorageConfig{TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{sessionStorage: storage}
	runtime.bindSessions()
	t.Cleanup(func() {
		runtime.unbindSessions()
		_ = storage.Close()
	})

	now := time.Now()
	instance, err := storage.CreateSession(context.Background(), &server.SessionMeta{
		ID: "external", Token: "token", TTL: time.Hour,
		CreatedAt: now, UpdatedAt: now, ExpireAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Set("name", "George"); err != nil {
		t.Fatal(err)
	}

	session, err := gox.Sessions.Get("external")
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if ok, err := session.Get("name", &name); err != nil || !ok || name != "George" {
		t.Fatalf("name = %q, ok=%v, err=%v", name, ok, err)
	}
	if err := session.Set("active", true); err != nil {
		t.Fatal(err)
	}
	if err := session.Save(); err != nil {
		t.Fatal(err)
	}
	if err := session.Rotate(); !errors.Is(err, server.ErrSessionNotAvailable) {
		t.Fatalf("Rotate() error = %v", err)
	}
	if err := session.Destroy(); err != nil {
		t.Fatal(err)
	}
	if _, err := gox.Sessions.Get("external"); !errors.Is(err, server.ErrSessionNotAvailable) {
		t.Fatalf("Get() after Destroy error = %v", err)
	}
}
