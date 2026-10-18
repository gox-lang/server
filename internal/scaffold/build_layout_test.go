package scaffold

import "testing"

func TestProjectDirsUseNormalizedBuildLayout(t *testing.T) {
	dirs := map[string]bool{}
	for _, dir := range ProjectDirs() {
		dirs[dir] = true
	}

	for _, want := range []string{
		".build", "app", "pages", "public", "tests",
	} {
		if !dirs[want] {
			t.Fatalf("ProjectDirs missing %q", want)
		}
	}

	for _, old := range []string{
		"components", "client", "websocket", "tests/results", "tests/snapshots",
		".build/client",
		".build/generated/go",
		".build/generated/js",
		".build/generated/types",
		".build/public",
		".build/plugins",
	} {
		if dirs[old] {
			t.Fatalf("ProjectDirs should not include legacy dir %q", old)
		}
	}
}
