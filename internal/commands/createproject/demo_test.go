package createproject

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gox-lang/server/internal/scaffold"
)

func archiveFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, contents := range files {
		f, err := w.Create("demo-main/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func writeFixture(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func assertFile(t *testing.T, root, name, expected string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || string(data) != expected {
		t.Fatalf("%s: %q, %v", name, data, err)
	}
}
func TestDemoOverlayPreservesScaffold(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"gox.json", "go.mod", "app/app.gox", "client/react/package.json"} {
		writeFixture(t, root, name, "generated")
	}
	writeFixture(t, root, "pages/index.gox", "old")
	data := archiveFixture(t, map[string]string{
		"pages/index.gox": "demo", "pages/.error.gox": "error", "LICENSE": "MIT", "app/routes.gox": "routes",
		"client/react/playground/Counter.jsx": "counter", "gox.json": "wrong", "go.mod": "wrong", "app/app.gox": "wrong",
		"client/react/package.json": "wrong", ".git/config": "wrong", "public/.env": "wrong",
	})
	if err := extractDemo(data, root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gox.json", "go.mod", "app/app.gox", "client/react/package.json"} {
		assertFile(t, root, name, "generated")
	}
	assertFile(t, root, "pages/index.gox", "demo")
	assertFile(t, root, "pages/.error.gox", "error")
	assertFile(t, root, "client/react/playground/Counter.jsx", "counter")
	for _, name := range []string{".git/config", "public/.env"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s", name)
		}
	}
}
func TestDemoRejectsInvalidArchives(t *testing.T) {
	for _, name := range []string{"../escape", "pages/../../escape", "/absolute", `pages\escape`, "C:/escape"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			data := archiveFixture(t, map[string]string{"pages/index.gox": "demo", "LICENSE": "MIT", name: "invalid"})
			if err := extractDemo(data, root); err == nil {
				t.Fatal("accepted unsafe path")
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 0 {
				t.Fatal("wrote before validation")
			}
		})
	}
	for _, data := range [][]byte{[]byte("not a zip"), archiveFixture(t, map[string]string{"README.md": "incomplete"})} {
		if err := extractDemo(data, t.TempDir()); err == nil {
			t.Fatal("accepted invalid archive")
		}
	}
}
func TestDemoHTTP(t *testing.T) {
	data := archiveFixture(t, map[string]string{"pages/index.gox": "demo", "LICENSE": "MIT"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer server.Close()
	root := t.TempDir()
	if err := fetchDemo(server.Client(), server.URL, root); err != nil {
		t.Fatal(err)
	}
	assertFile(t, root, "pages/index.gox", "demo")
	if err := fetchDemo(server.Client(), server.URL+"/missing", root); err == nil {
		t.Fatal("accepted HTTP error")
	}
}
func TestDemoTransaction(t *testing.T) {
	for _, scenario := range []string{"success", "prepare failure", "download failure", "destination race"} {
		t.Run(scenario, func(t *testing.T) {
			parent := t.TempDir()
			opts := scaffold.Options{TargetDir: parent, ProjectName: "my-demo"}
			prepare := func(staged scaffold.Options) error {
				if staged.ProjectName != "my-demo" {
					t.Fatal("lost project name")
				}
				writeFixture(t, staged.ProjectPath(), "gox.json", "my-demo")
				if scenario == "prepare failure" {
					return errors.New("install failed")
				}
				return nil
			}
			download := func(root string) error {
				assertFile(t, root, "gox.json", "my-demo")
				if scenario == "download failure" {
					return errors.New("offline")
				}
				if scenario == "destination race" {
					writeFixture(t, opts.ProjectPath(), "keep", "untouched")
				}
				writeFixture(t, root, "pages/index.gox", "demo")
				return nil
			}
			err := createDemo(opts, prepare, download)
			if (err == nil) != (scenario == "success") {
				t.Fatalf("unexpected error: %v", err)
			}
			if scenario == "success" {
				assertFile(t, opts.ProjectPath(), "pages/index.gox", "demo")
			}
			if scenario == "destination race" {
				assertFile(t, opts.ProjectPath(), "keep", "untouched")
			}
			entries, _ := os.ReadDir(parent)
			for _, entry := range entries {
				if entry.Name() != "my-demo" {
					t.Fatalf("left staging %s", entry.Name())
				}
			}
			if scenario == "prepare failure" || scenario == "download failure" {
				if len(entries) != 0 {
					t.Fatal("left failed project")
				}
			}
		})
	}
}
func TestDemoExistingPathsNeverTouched(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			opts := scaffold.Options{TargetDir: parent, ProjectName: "demo"}
			switch kind {
			case "directory":
				if err := os.Mkdir(opts.ProjectPath(), 0755); err != nil {
					t.Fatal(err)
				}
			case "file":
				writeFixture(t, parent, "demo", "keep")
			case "symlink":
				if err := os.Symlink(filepath.Join(parent, "missing"), opts.ProjectPath()); err != nil {
					t.Skip(err)
				}
			}
			err := createDemo(opts, func(scaffold.Options) error { t.Fatal("prepare called"); return nil }, func(string) error { t.Fatal("download called"); return nil })
			if err == nil {
				t.Fatal("accepted existing path")
			}
			if _, err := os.Lstat(opts.ProjectPath()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
