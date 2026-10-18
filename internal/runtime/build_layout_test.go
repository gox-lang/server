package runtime

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigBuildPathsUseBuildConfigDirectory(t *testing.T) {
	root := t.TempDir()

	generated, artifact := configBuildPaths(root, "123")

	wantGenerated := filepath.Join(root, ".build", "config", "123", "config.go")
	wantArtifact := filepath.Join(root, ".build", "config", "123", "config.so")

	if generated != wantGenerated {
		t.Fatalf("generated path mismatch\nwant: %s\n got: %s", wantGenerated, generated)
	}
	if artifact != wantArtifact {
		t.Fatalf("artifact path mismatch\nwant: %s\n got: %s", wantArtifact, artifact)
	}
	if strings.Contains(generated, string(filepath.Separator)+"plugins"+string(filepath.Separator)) ||
		strings.Contains(artifact, string(filepath.Separator)+"plugins"+string(filepath.Separator)) {
		t.Fatalf("config build paths must not use .build/plugins: generated=%s artifact=%s", generated, artifact)
	}
}

func TestTestBuildPathsUseBuildCacheDirectory(t *testing.T) {
	root := t.TempDir()

	generated, artifact := testBuildPaths(root, "tests/example", "456")

	wantDir := filepath.Join(root, ".build", "cache", "tests", "tests-example-456")
	wantGenerated := filepath.Join(wantDir, "example.go")
	wantArtifact := filepath.Join(wantDir, "test.so")

	if generated != wantGenerated {
		t.Fatalf("generated path mismatch\nwant: %s\n got: %s", wantGenerated, generated)
	}
	if artifact != wantArtifact {
		t.Fatalf("artifact path mismatch\nwant: %s\n got: %s", wantArtifact, artifact)
	}
	if strings.Contains(generated, string(filepath.Separator)+"plugins"+string(filepath.Separator)) ||
		strings.Contains(artifact, string(filepath.Separator)+"plugins"+string(filepath.Separator)) {
		t.Fatalf("test build paths must not use .build/plugins: generated=%s artifact=%s", generated, artifact)
	}
}
