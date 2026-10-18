package gox

import (
	"testing"

	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/projectdefaults"
)

func writeProjectManifest(t *testing.T, root string) {
	t.Helper()
	if err := deps.WriteGoxJSON(root, projectdefaults.Manifest()); err != nil {
		t.Fatalf("write gox.json: %v", err)
	}
}
