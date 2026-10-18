package deps

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadGoxJSONMissingReturnsEmptyDependencies(t *testing.T) {
	cfg, err := ReadGoxJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Dependencies) != 0 {
		t.Fatalf("dependencies = %#v, want empty", cfg.Dependencies)
	}
}

func TestAddAndRemoveDependency(t *testing.T) {
	root := t.TempDir()
	if err := AddDependency(root, "github.com/redis/go-redis/v9", "latest"); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadGoxJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dependencies["github.com/redis/go-redis/v9"]; got != "latest" {
		t.Fatalf("dependency version = %q, want latest", got)
	}

	if err := RemoveDependency(root, "github.com/redis/go-redis/v9"); err != nil {
		t.Fatal(err)
	}
	cfg, err = ReadGoxJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Dependencies["github.com/redis/go-redis/v9"]; ok {
		t.Fatalf("dependency was not removed: %#v", cfg.Dependencies)
	}
}

func TestWriteDepsGoIncludesRequiredAndConfiguredImports(t *testing.T) {
	root := t.TempDir()
	cfg := GoxJSON{Dependencies: map[string]string{
		"github.com/redis/go-redis/v9": "latest",
	}}
	if err := WriteDepsGo(root, cfg, "github.com/gox-lang/gox"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, DepsGoFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{generatedMarker, `package main`, `_ "github.com/gox-lang/gox"`, `_ "github.com/redis/go-redis/v9"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("deps.go missing %q:\n%s", want, text)
		}
	}
}

func TestWriteDepsGoDoesNotOverwriteUserFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DepsGoFile), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := WriteDepsGo(root, NewGoxJSON(), "github.com/gox-lang/gox")
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("err = %v, want refusing to overwrite", err)
	}
}

func TestReadGoxJSONReadsToolchainAndFrameworks(t *testing.T) {
	root := t.TempDir()
	content := `{
  "dependencies": {
    "github.com/gox-lang/gox": "v1.0.0"
  },
  "toolchain": {
    "node": "22.18.0",
    "pnpm": "11.9.0",
    "vite": "8.1.0",
    "typescript": "6.0.3"
  },
  "frameworks": {
    "react": {
      "react": "19.2.0",
      "react-dom": "19.2.0",
      "@vitejs/plugin-react": "6.0.3"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(root, GoxJSONFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ReadGoxJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Toolchain.Node != "22.18.0" || cfg.Toolchain.PNPM != "11.9.0" || cfg.Toolchain.Vite != "8.1.0" || cfg.Toolchain.TypeScript != "6.0.3" {
		t.Fatalf("toolchain = %#v", cfg.Toolchain)
	}
	if got := cfg.Frameworks["react"]["@vitejs/plugin-react"]; got != "6.0.3" {
		t.Fatalf("react plugin version = %q, want 6.0.3", got)
	}
}

func TestDependencyChangesPreserveToolchainAndFrameworks(t *testing.T) {
	root := t.TempDir()
	cfg := GoxJSON{
		Dependencies: map[string]string{
			"github.com/gox-lang/gox": "v1.0.0",
		},
		Toolchain: Toolchain{
			Node:       "22.18.0",
			PNPM:       "11.9.0",
			Vite:       "8.1.0",
			TypeScript: "6.0.3",
		},
		Frameworks: Frameworks{
			"react": {
				"react":                "19.2.0",
				"react-dom":            "19.2.0",
				"@vitejs/plugin-react": "6.0.3",
				"@types/react":         "19.2.0",
				"@types/react-dom":     "19.2.0",
			},
		},
	}
	if err := WriteGoxJSON(root, cfg); err != nil {
		t.Fatal(err)
	}

	if err := AddDependency(root, "github.com/redis/go-redis/v9", "v9.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDependency(root, "github.com/redis/go-redis/v9"); err != nil {
		t.Fatal(err)
	}

	got, err := ReadGoxJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Toolchain != cfg.Toolchain {
		t.Fatalf("toolchain changed: got %#v, want %#v", got.Toolchain, cfg.Toolchain)
	}
	if !reflect.DeepEqual(got.Frameworks, cfg.Frameworks) {
		t.Fatalf("frameworks changed: got %#v, want %#v", got.Frameworks, cfg.Frameworks)
	}
	if !reflect.DeepEqual(got.Dependencies, cfg.Dependencies) {
		t.Fatalf("dependencies changed unexpectedly: got %#v, want %#v", got.Dependencies, cfg.Dependencies)
	}
}

func TestReadLegacyGoxJSONInitializesNewSections(t *testing.T) {
	root := t.TempDir()
	content := `{
  "dependencies": {
    "github.com/gox-lang/gox": "v1.0.0"
  }
}
`
	if err := os.WriteFile(filepath.Join(root, GoxJSONFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := ReadGoxJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Frameworks == nil {
		t.Fatal("frameworks is nil")
	}
	if cfg.Dependencies == nil {
		t.Fatal("dependencies is nil")
	}
	if cfg.Toolchain != (Toolchain{}) {
		t.Fatalf("toolchain = %#v, want zero value", cfg.Toolchain)
	}
}
