package scaffold

import (
	"strings"
	"testing"
)

func TestProjectFilesIncludeServerLifecycleScaffold(t *testing.T) {
	files := ProjectFiles(Options{
		ProjectName:      "demo",
		GoVersion:        "1.26",
		GoxModule:        "github.com/gox-lang/gox",
		GoxModuleVersion: "v0.1.0",
	})

	contents := map[string]string{}
	for _, file := range files {
		contents[file.Path] = file.Content
	}

	startup, ok := contents["app/startup.gox"]
	if !ok {
		t.Fatal("startup scaffold missing")
	}
	if !strings.Contains(startup, "module startup") || !strings.Contains(startup, "func OnStartServer() error") {
		t.Fatalf("invalid startup scaffold:\n%s", startup)
	}

	shutdown, ok := contents["app/shutdown.gox"]
	if !ok {
		t.Fatal("shutdown scaffold missing")
	}
	if !strings.Contains(shutdown, "module shutdown") || !strings.Contains(shutdown, "func OnStopServer() error") {
		t.Fatalf("invalid shutdown scaffold:\n%s", shutdown)
	}

	config := contents["app/config.gox"]
	for _, want := range []string{
		`"startup.gox"`,
		`"shutdown.gox"`,
		"OnStartServer: startup.OnStartServer",
		"OnStopServer:  shutdown.OnStopServer",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config scaffold missing %q:\n%s", want, config)
		}
	}
}
