package clientbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeFrameworkVendorManifestForTest(t *testing.T, output string, files []string) {
	t.Helper()
	data, err := json.Marshal(frameworkVendorManifest{Version: 1, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, frameworkVendorManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeInstalledFrameworkVersionForTest(t *testing.T, root string, framework string, version string) {
	t.Helper()
	pkg := frameworkPackageName(framework)
	dir := filepath.Join(root, "client", framework, "node_modules", filepath.FromSlash(pkg))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"version":"` + version + `"}`)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFrameworkVendorOutputsExistRequiresCompleteSet(t *testing.T) {
	root := t.TempDir()
	const version = "19.1.1"
	writeInstalledFrameworkVersionForTest(t, root, "react", version)
	output := filepath.Join(root, ".build", "client", "react", ModeProd)
	if err := os.MkdirAll(filepath.Join(output, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if FrameworkVendorOutputsExist(root, "react") {
		t.Fatal("react vendors should not be ready before every facade exists")
	}

	files := make([]string, 0, len(frameworkModuleFacades("react"))+1)
	for _, facade := range frameworkModuleFacades("react") {
		name := filepath.ToSlash(filepath.FromSlash(facade.Name) + "." + version + ".js")
		path := filepath.Join(output, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("export {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, name)
	}

	chunkName := "chunks/" + version + "/shared-runtime.js"
	chunkPath := filepath.Join(output, filepath.FromSlash(chunkName))
	if err := os.MkdirAll(filepath.Dir(chunkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chunkPath, []byte("export {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files = append(files, chunkName)
	writeFrameworkVendorManifestForTest(t, output, files)

	if !FrameworkVendorOutputsExist(root, "react") {
		t.Fatal("react vendors should be ready when every manifest output exists")
	}
	if err := os.Remove(chunkPath); err != nil {
		t.Fatal(err)
	}
	if FrameworkVendorOutputsExist(root, "react") {
		t.Fatal("react vendors should be incomplete when a manifest chunk is missing")
	}
}

func TestFrameworkVendorOutputsExistRejectsMissingOrInvalidManifest(t *testing.T) {
	root := t.TempDir()
	const version = "3.5.21"
	writeInstalledFrameworkVersionForTest(t, root, "vue", version)
	output := filepath.Join(root, ".build", "client", "vue", ModeProd)
	for _, facade := range frameworkModuleFacades("vue") {
		path := filepath.Join(output, filepath.FromSlash(facade.Name)+"."+version+".js")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("export {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if FrameworkVendorOutputsExist(root, "vue") {
		t.Fatal("vue vendors should be incomplete without a vendor manifest")
	}
	if err := os.WriteFile(filepath.Join(output, frameworkVendorManifestName), []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if FrameworkVendorOutputsExist(root, "vue") {
		t.Fatal("vue vendors should be incomplete with an invalid vendor manifest")
	}
}

func TestSvelteRuntimeFlagsHaveStableVendorFacades(t *testing.T) {
	flags := []struct {
		specifier string
		name      string
		file      string
	}{
		{"svelte/internal/flags/legacy", "vendor/svelte-internal-flags-legacy", "legacy.js"},
		{"svelte/internal/flags/async", "vendor/svelte-internal-flags-async", "async.js"},
		{"svelte/internal/flags/tracing", "vendor/svelte-internal-flags-tracing", "tracing.js"},
	}
	entries := FrameworkImportMapEntries("svelte", "5.57.0")
	root := t.TempDir()
	for _, flag := range flags {
		t.Run(flag.specifier, func(t *testing.T) {
			wantURL := PublicClientPrefix + "/" + flag.name + ".5.57.0.js"
			found := false
			for _, entry := range entries {
				if entry.Specifier == flag.specifier {
					found = true
					if entry.URL != wantURL {
						t.Fatalf("flag import map URL = %q, want %q", entry.URL, wantURL)
					}
				}
			}
			if !found {
				t.Fatalf("missing %q from Svelte import map", flag.specifier)
			}

			source := frameworkModuleFacadeSource(root, "svelte", flag.name)
			wantSource := filepath.Join(root, "node_modules", "svelte", "src", "internal", "flags", flag.file)
			if !strings.Contains(source, wantSource) {
				t.Fatalf("flag facade missing %q:\n%s", wantSource, source)
			}
		})
	}
}

func TestPreactSharedRuntimeHasStableVendorFacades(t *testing.T) {
	modules := []struct {
		specifier string
		name      string
		parts     []string
	}{
		{"preact", "vendor/preact", []string{"preact", "dist", "preact.module.js"}},
		{"preact/jsx-runtime", "vendor/preact-jsx-runtime", []string{"preact", "jsx-runtime", "dist", "jsxRuntime.module.js"}},
		{"preact/jsx-dev-runtime", "vendor/preact-jsx-dev-runtime", []string{"preact", "jsx-runtime", "dist", "jsxRuntime.module.js"}},
		{"preact/hooks", "vendor/preact-hooks", []string{"preact", "hooks", "dist", "hooks.module.js"}},
		{"preact/compat", "vendor/preact-compat", []string{"preact", "compat", "dist", "compat.module.js"}},
	}
	entries := FrameworkImportMapEntries("preact", "10.27.2")
	root := t.TempDir()
	for _, module := range modules {
		t.Run(module.specifier, func(t *testing.T) {
			wantURL := PublicClientPrefix + "/" + module.name + ".10.27.2.js"
			found := false
			for _, entry := range entries {
				if entry.Specifier == module.specifier {
					found = true
					if entry.URL != wantURL {
						t.Fatalf("Preact import map URL = %q, want %q", entry.URL, wantURL)
					}
				}
			}
			if !found {
				t.Fatalf("missing %q from Preact import map", module.specifier)
			}

			source := frameworkModuleFacadeSource(root, "preact", module.name)
			wantSource := filepath.Join(append([]string{root, "node_modules"}, module.parts...)...)
			if !strings.Contains(source, wantSource) {
				t.Fatalf("Preact facade missing %q:\n%s", wantSource, source)
			}
		})
	}
}

func TestGroupViteAssetsPreservesModeAndProjectForVendorRecovery(t *testing.T) {
	root := t.TempDir()
	assets := []Asset{
		{
			Name:        "menu-prod",
			Framework:   "vue",
			Mode:        ModeProd,
			ProjectRoot: root,
			Path:        "client/vue/menu.vue",
		},
		{
			Name:        "menu-dev",
			Framework:   "vue",
			Mode:        ModeDev,
			Dev:         true,
			ProjectRoot: root,
			Path:        "client/vue/menu.vue",
		},
	}

	groups := groupViteAssets(assets)
	if len(groups) != 2 {
		t.Fatalf("expected 2 mode-separated groups, got %d", len(groups))
	}

	seen := map[string]bool{}
	for _, group := range groups {
		seen[group.Mode] = true
		if group.ProjectRoot != cleanCacheRoot(root) {
			t.Fatalf("group %s project root = %q, want %q", group.Mode, group.ProjectRoot, cleanCacheRoot(root))
		}
		if group.Framework != "vue" {
			t.Fatalf("group framework = %q, want vue", group.Framework)
		}
	}
	if !seen[ModeProd] || !seen[ModeDev] {
		t.Fatalf("expected both prod and dev groups, got %#v", seen)
	}
}

func TestEnsureFrameworkVendorSerializesSameProjectFramework(t *testing.T) {
	root := t.TempDir()
	const version = "3.5.42"
	writeInstalledFrameworkVersionForTest(t, root, "vue", version)

	var builds int32
	build := func(group viteAssetGroup) error {
		atomic.AddInt32(&builds, 1)
		// Keep the first build active long enough for the other callers to queue.
		time.Sleep(25 * time.Millisecond)
		files := make([]string, 0, len(frameworkModuleFacades("vue")))
		for _, facade := range frameworkModuleFacades("vue") {
			name := filepath.ToSlash(filepath.FromSlash(facade.Name) + "." + version + ".js")
			path := filepath.Join(group.OutputAbsDir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte("export {}\n"), 0o644); err != nil {
				return err
			}
			files = append(files, name)
		}
		data, err := json.Marshal(frameworkVendorManifest{Version: 1, Files: files})
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(group.OutputAbsDir, frameworkVendorManifestName), data, 0o644)
	}

	const callers = 16
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			<-start
			errs <- ensureFrameworkVendor(root, "vue", build)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&builds); got != 1 {
		t.Fatalf("vendor builds = %d, want 1", got)
	}
}

func TestFrameworkVendorBuildLocksAreScopedByProjectAndFramework(t *testing.T) {
	root := t.TempDir()
	vue := frameworkVendorBuildLock(root, "vue")
	react := frameworkVendorBuildLock(root, "react")
	otherVue := frameworkVendorBuildLock(filepath.Join(root, "other"), "vue")
	if vue == react {
		t.Fatal("different frameworks in one project must not share a vendor build lock")
	}
	if vue == otherVue {
		t.Fatal("the same framework in different projects must not share a vendor build lock")
	}
	if vue != frameworkVendorBuildLock(root, "VUE") {
		t.Fatal("the same project/framework must reuse its vendor build lock")
	}
}
