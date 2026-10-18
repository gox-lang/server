package scaffold

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/projectdefaults"
)

func TestProjectFilesWritesCompleteGoxManifest(t *testing.T) {
	manifest := projectdefaults.Manifest()
	files := ProjectFiles(Options{
		ProjectName:      "example",
		GoVersion:        "1.24",
		GoxModule:        "github.com/gox-lang/gox",
		GoxModuleVersion: "v1.0.0",
		Manifest:         manifest,
	})

	var content string
	for _, file := range files {
		if file.Path == "gox.json" {
			content = file.Content
			break
		}
	}
	if content == "" {
		t.Fatal("gox.json was not generated")
	}

	var got deps.GoxJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid gox.json: %v", err)
	}
	if got.Toolchain != manifest.Toolchain {
		t.Fatalf("toolchain = %#v, want %#v", got.Toolchain, manifest.Toolchain)
	}
	if len(got.Frameworks) != 5 {
		t.Fatalf("framework count = %d, want 5", len(got.Frameworks))
	}
	if got.Dependencies[buildinfo.GoxModule] != buildinfo.GoxModuleVersion {
		t.Fatalf("GOX SDK dependency = %q, want %q", got.Dependencies[buildinfo.GoxModule], buildinfo.GoxModuleVersion)
	}
}

func TestProjectFilesLegacyOptionsStillGenerateDependencies(t *testing.T) {
	files := ProjectFiles(Options{
		GoxModule:        "github.com/gox-lang/gox",
		GoxModuleVersion: "v1.0.0",
	})

	var got deps.GoxJSON
	for _, file := range files {
		if file.Path != "gox.json" {
			continue
		}
		if err := json.Unmarshal([]byte(file.Content), &got); err != nil {
			t.Fatal(err)
		}
	}
	if got.Dependencies["github.com/gox-lang/gox"] != "v1.0.0" {
		t.Fatalf("dependencies = %#v", got.Dependencies)
	}
}

func TestProjectFilesGoModUsesPublishedSDKWithoutReplace(t *testing.T) {
	files := ProjectFiles(Options{
		ProjectName:      "demo",
		GoVersion:        "1.24",
		GoxModule:        "github.com/gox-lang/gox",
		GoxModuleVersion: "v1.0.0",
	})

	var goMod string
	for _, file := range files {
		if file.Path == "go.mod" {
			goMod = file.Content
			break
		}
	}
	if goMod == "" {
		t.Fatal("go.mod scaffold missing")
	}
	if !strings.Contains(goMod, "require github.com/gox-lang/gox v1.0.0") {
		t.Fatalf("go.mod missing published SDK requirement:\n%s", goMod)
	}
	if strings.Contains(goMod, "replace github.com/gox-lang/gox") {
		t.Fatalf("go.mod must not replace published SDK:\n%s", goMod)
	}
}
