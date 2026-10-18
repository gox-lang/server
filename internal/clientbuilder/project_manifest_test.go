package clientbuilder

import (
	"testing"

	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/projectdefaults"
)

func writeDefaultProjectManifest(t *testing.T, root string) {
	t.Helper()
	if err := deps.WriteGoxJSON(root, projectdefaults.Manifest()); err != nil {
		t.Fatal(err)
	}
}
