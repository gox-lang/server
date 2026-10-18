package get

import "testing"

func TestSplitPackageVersion(t *testing.T) {
	tests := []struct {
		input   string
		path    string
		version string
	}{
		{"github.com/redis/go-redis/v9", "github.com/redis/go-redis/v9", "latest"},
		{"github.com/redis/go-redis/v9@v9.1.0", "github.com/redis/go-redis/v9", "v9.1.0"},
	}
	for _, tt := range tests {
		path, version := splitPackageVersion(tt.input)
		if path != tt.path || version != tt.version {
			t.Fatalf("splitPackageVersion(%q) = %q, %q; want %q, %q", tt.input, path, version, tt.path, tt.version)
		}
	}
}
