package deps

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAddThenInstallFailureRestoresDependencyState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell script")
	}
	root := t.TempDir()
	originalJSON := []byte("{\n  \"dependencies\": {},\n  \"toolchain\": {\"node\": \"\", \"pnpm\": \"\", \"vite\": \"\", \"typescript\": \"\"},\n  \"frameworks\": {}\n}\n")
	if err := os.WriteFile(filepath.Join(root, GoxJSONFile), originalJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	originalMod := []byte("module example.com/app\n\ngo 1.24\n")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), originalMod, 0o644); err != nil {
		t.Fatal(err)
	}

	fakeGo := writeFailingGo(t, root)
	if err := AddAndInstall(root, fakeGo, "example.com/bad", "v1.0.0"); err == nil {
		t.Fatal("AddAndInstall() succeeded; want failure")
	}

	gotJSON, err := os.ReadFile(filepath.Join(root, GoxJSONFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(originalJSON) {
		t.Fatalf("gox.json changed after failed add/install:\n%s", gotJSON)
	}
	gotMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotMod) != string(originalMod) {
		t.Fatalf("go.mod changed after failed add/install:\n%s", gotMod)
	}
	if _, err := os.Stat(filepath.Join(root, DepsGoFile)); !os.IsNotExist(err) {
		t.Fatalf("deps.go exists after failed add/install; want original absence")
	}
	if _, err := os.Stat(filepath.Join(root, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("go.sum exists after failed add/install; want original absence")
	}
}

func TestRemoveThenInstallFailureRestoresDependencyState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell script")
	}
	root := t.TempDir()
	cfg := NewGoxJSON()
	cfg.Dependencies["example.com/dep"] = "v1.0.0"
	if err := WriteGoxJSON(root, cfg); err != nil {
		t.Fatal(err)
	}
	if err := WriteDepsGo(root, cfg, "github.com/gox-lang/gox"); err != nil {
		t.Fatal(err)
	}
	originalJSON, _ := os.ReadFile(filepath.Join(root, GoxJSONFile))
	originalDeps, _ := os.ReadFile(filepath.Join(root, DepsGoFile))
	originalMod := []byte("module example.com/app\n\ngo 1.24\n\nrequire example.com/dep v1.0.0\n")
	originalSum := []byte("original sum\n")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), originalMod, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), originalSum, 0o644); err != nil {
		t.Fatal(err)
	}

	fakeGo := writeFailingGo(t, root)
	if err := RemoveAndInstall(root, fakeGo, "example.com/dep"); err == nil {
		t.Fatal("RemoveAndInstall() succeeded; want failure")
	}

	assertFileEquals(t, filepath.Join(root, GoxJSONFile), originalJSON)
	assertFileEquals(t, filepath.Join(root, DepsGoFile), originalDeps)
	assertFileEquals(t, filepath.Join(root, "go.mod"), originalMod)
	assertFileEquals(t, filepath.Join(root, "go.sum"), originalSum)
}

func writeFailingGo(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "fake-go")
	script := "#!/bin/sh\n" +
		"printf 'mutated by failed go command\\n' > go.mod\n" +
		"printf 'mutated sum\\n' > go.sum\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFileEquals(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s changed after failed operation:\n%s", filepath.Base(path), got)
	}
}
