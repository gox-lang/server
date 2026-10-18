package deps

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gox-lang/server/internal/buildinfo"
	"golang.org/x/mod/modfile"
)

// The SDK compiled into the host, the SDK selected for generated plugins and
// the official adapter must agree. In a standalone server checkout only the
// host/runtime check applies; release validation supplies the adapter path.
func TestSDKVersionCompatibility(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate source checkout")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	check := func(t *testing.T, path string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		mod, err := modfile.Parse(path, data, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, replacement := range mod.Replace {
			if replacement.Old.Path == buildinfo.GoxModule {
				t.Fatalf("%s replaces the SDK; validate against the published SDK without local replacements", path)
			}
		}
		for _, requirement := range mod.Require {
			if requirement.Mod.Path == buildinfo.GoxModule {
				if requirement.Mod.Version != buildinfo.GoxModuleVersion {
					t.Fatalf("%s requires SDK %s; GOX Server uses %s", path, requirement.Mod.Version, buildinfo.GoxModuleVersion)
				}
				return
			}
		}
		t.Fatalf("%s does not require the GOX SDK", path)
	}
	check(t, filepath.Join(root, "go.mod"))
	t.Run("RedisAdapter", func(t *testing.T) {
		adapter := os.Getenv("GOX_REDIS_ADAPTER_DIR")
		if adapter == "" {
			adapter = filepath.Join(root, "..", "redisadapter")
			if _, err := os.Stat(filepath.Join(adapter, "go.mod")); os.IsNotExist(err) {
				t.Skip("set GOX_REDIS_ADAPTER_DIR for cross-repository release validation")
			}
		}
		check(t, filepath.Join(adapter, "go.mod"))
	})
}
