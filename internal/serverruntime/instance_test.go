package serverruntime

import (
	"encoding/base64"
	"testing"
)

func TestRuntimeInstanceIDIsCanonicalAndUnique(t *testing.T) {
	first, err := newRuntimeInstanceID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newRuntimeInstanceID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two GOX runtimes received the same instance ID")
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(raw) != runtimeInstanceIDSize {
		t.Fatalf("instance ID = %q, bytes=%d, err=%v", first, len(raw), err)
	}
}
