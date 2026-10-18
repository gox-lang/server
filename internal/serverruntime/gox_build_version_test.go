package serverruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func TestMoveGoxGeneratedArtifactsMovesSourceMapWithGeneratedGo(t *testing.T) {
	root := t.TempDir()
	oldGeneratedPath := filepath.Join(root, "provisional.go")
	generatedPath := filepath.Join(root, "version.go")
	oldSourceMapPath := goxcompiler.BundleSourceMapPath(oldGeneratedPath)
	for path, contents := range map[string]string{
		oldGeneratedPath: "package pages\n",
		oldSourceMapPath: "{\"version\":1,\"generatedUri\":\"provisional.go\",\"sources\":[],\"mappings\":[]}\n",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result := &goxcompiler.CompileResult{OutPath: oldGeneratedPath, OutAbsPath: oldGeneratedPath, SourceMapPath: oldSourceMapPath}
	if err := moveGoxGeneratedArtifacts(oldGeneratedPath, generatedPath, oldSourceMapPath, result); err != nil {
		t.Fatal(err)
	}
	newSourceMapPath := goxcompiler.BundleSourceMapPath(generatedPath)
	for _, path := range []string{generatedPath, newSourceMapPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected current artifact %s: %v", path, err)
		}
	}
	for _, path := range []string{oldGeneratedPath, oldSourceMapPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected provisional artifact removed %s: %v", path, err)
		}
	}
	if result.OutPath != generatedPath || result.OutAbsPath != generatedPath || result.SourceMapPath != newSourceMapPath {
		t.Fatalf("compile result paths were not updated: %#v", result)
	}
	mapData, err := os.ReadFile(newSourceMapPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mapData), `"generatedUri": "`+generatedPath+`"`) {
		t.Fatalf("source map generated URI was not updated: %s", mapData)
	}
}

func TestGoxDependencyBuildVersionUsesContentNotModTime(t *testing.T) {
	root := t.TempDir()
	entryPath := filepath.Join(root, "pages", "index.gox")
	modulePath := filepath.Join(root, "pages", "shared.gox")
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, []byte("module page\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modulePath, []byte("module shared\nconst value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixed := time.Unix(1_700_000_000, 123)
	for _, path := range []string{entryPath, modulePath} {
		if err := os.Chtimes(path, fixed, fixed); err != nil {
			t.Fatal(err)
		}
	}
	deps := []GoxRouteDependency{
		{AbsPath: entryPath, ModTime: fixed, IsEntry: true},
		{AbsPath: modulePath, ModTime: fixed},
	}

	_, first, err := goxDependencyBuildVersion(deps)
	if err != nil {
		t.Fatal(err)
	}
	later := fixed.Add(10 * time.Hour)
	for _, path := range []string{entryPath, modulePath} {
		if err := os.Chtimes(path, later, later); err != nil {
			t.Fatal(err)
		}
	}
	deps[0].ModTime = later
	deps[1].ModTime = later
	_, touched, err := goxDependencyBuildVersion(deps)
	if err != nil {
		t.Fatal(err)
	}
	if touched != first {
		t.Fatalf("modtime-only change altered build version: %q != %q", touched, first)
	}

	if err := os.WriteFile(modulePath, []byte("module shared\nconst value = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(modulePath, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	deps[1].ModTime = fixed
	_, changed, err := goxDependencyBuildVersion(deps)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("content change with restored modtime did not alter build version")
	}
}

func TestGoxDependencyBuildVersionDoesNotAffectStaticETag(t *testing.T) {
	root := t.TempDir()
	goxPath := filepath.Join(root, "pages", "index.gox")
	staticPath := filepath.Join(root, "public", "index.js")
	for _, path := range []string{goxPath, staticPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(goxPath, []byte("module page\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staticPath, []byte("export const value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	staticInfo, err := os.Stat(staticPath)
	if err != nil {
		t.Fatal(err)
	}
	etag := buildStaticETag(staticInfo, staticIdentityEncoding, 0)
	deps := []GoxRouteDependency{{AbsPath: goxPath, IsEntry: true}}
	_, first, err := goxDependencyBuildVersion(deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goxPath, []byte("module page\nconst changed = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, second, err := goxDependencyBuildVersion(deps)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("gox content change did not alter build version")
	}
	if got := buildStaticETag(staticInfo, staticIdentityEncoding, 0); got != etag {
		t.Fatalf("plugin build version affected static ETag: %q != %q", got, etag)
	}
}
