package clientbuilder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureFrameworkWorkspacePreservesUserPackageJSONFields(t *testing.T) {
	dir := t.TempDir()
	writeDefaultProjectManifest(t, dir)
	root := filepath.Join(dir, "client", "react")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	pkgPath := filepath.Join(root, "package.json")
	content := `{
  "name": "custom-client",
  "private": false,
  "type": "module",
  "scripts": {"lint": "eslint .", "custom": "node custom.js"},
  "exports": {".": "./src/index.ts"},
  "goxUserField": {"enabled": true, "nested": [1, 2, 3]},
  "dependencies": {"axios": "^1.7.0", "react": "0.0.1"},
  "devDependencies": {"eslint": "^9.0.0", "vite": "0.0.1"}
}`
	if err := os.WriteFile(pkgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureFrameworkWorkspace(dir, "react"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(pkgPath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["name"] != "custom-client" {
		t.Fatalf("name = %#v", got["name"])
	}
	scripts, ok := got["scripts"].(map[string]any)
	if !ok || scripts["lint"] != "eslint ." || scripts["custom"] != "node custom.js" {
		t.Fatalf("scripts changed: %#v", got["scripts"])
	}
	if _, ok := got["exports"].(map[string]any); !ok {
		t.Fatalf("exports lost: %#v", got["exports"])
	}
	custom, ok := got["goxUserField"].(map[string]any)
	if !ok || custom["enabled"] != true {
		t.Fatalf("custom field changed: %#v", got["goxUserField"])
	}
	dependencies := got["dependencies"].(map[string]any)
	devDependencies := got["devDependencies"].(map[string]any)
	if dependencies["axios"] != "^1.7.0" || devDependencies["eslint"] != "^9.0.0" {
		t.Fatalf("user dependencies changed: dependencies=%#v devDependencies=%#v", dependencies, devDependencies)
	}
}

func TestEnsureFrameworkWorkspaceSynchronizesOnlyManagedPackages(t *testing.T) {
	dir := t.TempDir()
	writeDefaultProjectManifest(t, dir)
	root := filepath.Join(dir, "client", "react")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	pkgPath := filepath.Join(root, "package.json")
	content := `{
  "private": true,
  "type": "module",
  "dependencies": {
    "axios": "^1.7.0",
    "react": "0.0.1",
    "vite": "user-wrong-section"
  },
  "devDependencies": {
    "eslint": "^9.0.0",
    "react-dom": "user-wrong-section",
    "vite": "0.0.1"
  }
}`
	if err := os.WriteFile(pkgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureFrameworkWorkspace(dir, "react"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(pkgPath)
	if err != nil {
		t.Fatal(err)
	}
	var got packageJSON
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	managedDeps, managedDevDeps, err := ProjectManagedDependencies(dir, "react")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range managedDeps {
		if got.Dependencies[name] != want {
			t.Fatalf("dependency %s = %q, want %q", name, got.Dependencies[name], want)
		}
		if _, exists := got.DevDependencies[name]; exists {
			t.Fatalf("managed dependency %s remained in devDependencies", name)
		}
	}
	for name, want := range managedDevDeps {
		if got.DevDependencies[name] != want {
			t.Fatalf("devDependency %s = %q, want %q", name, got.DevDependencies[name], want)
		}
		if _, exists := got.Dependencies[name]; exists {
			t.Fatalf("managed devDependency %s remained in dependencies", name)
		}
	}
	if got.Dependencies["axios"] != "^1.7.0" || got.DevDependencies["eslint"] != "^9.0.0" {
		t.Fatalf("user packages changed: dependencies=%#v devDependencies=%#v", got.Dependencies, got.DevDependencies)
	}
}
