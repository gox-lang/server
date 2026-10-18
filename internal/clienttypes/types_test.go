package clienttypes

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestEnsureStaticWritesWebSocketDeclarationInBuildRoot(t *testing.T) {
	root := t.TempDir()
	if err := EnsureStatic(root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "websocket.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `declare module "@gox/websocket"`) {
		t.Fatalf("websocket.d.ts is not the GOX websocket declaration")
	}
	if _, err := os.Stat(filepath.Join(root, ".build", "types")); !os.IsNotExist(err) {
		t.Fatalf("legacy .build/types directory must not be created")
	}
}

func TestEnsureStaticConcurrent(t *testing.T) {
	root := t.TempDir()
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- EnsureStatic(root)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ".build", "websocket.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `declare module "@gox/websocket"`) {
		t.Fatalf("concurrent static generation produced an invalid declaration")
	}
}
