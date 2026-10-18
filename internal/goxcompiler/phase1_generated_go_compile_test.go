package gox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type phase1GeneratedGoFixture struct {
	entryRel string
	source   string
	files    map[string]string
}

// phase1CompileAndGoTest is the recovery harness for compiler contract tests.
// It intentionally goes past Compile: the generated Go is compiled by the real
// Go toolchain in an isolated temporary module that mirrors this repository's
// dependency graph. Future recovery phases should use this helper whenever a
// contract can be broken by invalid generated Go.
func phase1CompileAndGoTest(t testing.TB, fixture phase1GeneratedGoFixture) string {
	t.Helper()

	root := t.TempDir()
	phase1CopyModuleFiles(t, root)
	for rel, contents := range fixture.files {
		phase1WriteFile(t, filepath.Join(root, filepath.FromSlash(rel)), contents)
	}

	entry := filepath.Join(root, filepath.FromSlash(fixture.entryRel))
	phase1WriteFile(t, entry, fixture.source)

	outDir := filepath.Join(root, ".build", "phase1")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create generated-Go directory: %v", err)
	}
	outPath := filepath.Join(outDir, "generated.go")

	result, err := Compile(entry, outPath)
	if err != nil {
		t.Fatalf("Compile(%s): %v", fixture.entryRel, err)
	}
	if result == nil || !result.Written {
		t.Fatalf("Compile(%s) did not report a written bundle: %#v", fixture.entryRel, result)
	}

	generated, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read generated Go: %v", err)
	}
	if !strings.HasPrefix(string(generated), "package main") {
		t.Fatalf("generated bundle does not start with package main:\n%s", phase1Truncate(string(generated), 4000))
	}

	phase1GoTestGeneratedPackage(t, outDir, string(generated))
	return string(generated)
}

func phase1GoTestGeneratedPackage(t testing.TB, dir, generated string) {
	t.Helper()

	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate Go toolchain: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, goExe, "test", "-mod=readonly", "-run", "^$", ".")
	cmd.Dir = dir
	cmd.Env = phase1EnvWith(os.Environ(), "GOWORK", "off")
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("Go compiler timed out for generated bundle after 2m\n%s", string(output))
	}
	if err != nil {
		t.Fatalf("generated Go does not compile: %v\n--- go test output ---\n%s\n--- generated Go ---\n%s", err, string(output), phase1Truncate(generated, 20000))
	}
}

func phase1CopyModuleFiles(t testing.TB, dstRoot string) {
	t.Helper()

	srcRoot := phase1RepositoryRoot(t)
	for _, name := range []string{"go.mod", "go.sum"} {
		src := filepath.Join(srcRoot, name)
		data, err := os.ReadFile(src)
		if err != nil {
			if name == "go.sum" && os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read repository %s: %v", name, err)
		}
		phase1WriteFile(t, filepath.Join(dstRoot, name), string(data))
	}
}

func phase1RepositoryRoot(t testing.TB) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if readErr == nil && strings.Contains(string(data), "module github.com/gox-lang/server") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate github.com/gox-lang/server module root from %s", dir)
		}
		dir = parent
	}
}

func phase1WriteFile(t testing.TB, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s parent: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func phase1EnvWith(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, fmt.Sprintf("%s=%s", key, value))
}

func phase1Truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "\n... <truncated>"
}

func TestPhase1GeneratedGoCompileHarness(t *testing.T) {
	tests := []struct {
		name    string
		fixture phase1GeneratedGoFixture
	}{
		{
			name: "Page",
			fixture: phase1GeneratedGoFixture{
				entryRel: "pages/index.gox",
				source: `module index

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <main>Hello</main>
}
`,
			},
		},
		{
			name: "ComponentWithoutPropsOrChildren",
			fixture: phase1GeneratedGoFixture{
				entryRel: "pages/index.gox",
				source: `module index

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <main><&Card /></main>
}

func Card() gox.Component {
	return <article>Card</article>
}
`,
			},
		},
		{
			name: "Action",
			fixture: phase1GeneratedGoFixture{
				entryRel: "pages/index.gox",
				source: `module index

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <main>Hello</main>
}

func Save(name string) gox.Action {
	return name
}
`,
			},
		},
		{
			name: "WebSocket",
			fixture: phase1GeneratedGoFixture{
				entryRel: "websocket/index.gox",
				source: `module websocket

import "github.com/gox-lang/gox"

func Index(socket *gox.Socket) gox.WebSocket {
}
`,
			},
		},
		{
			name: "Channel",
			fixture: phase1GeneratedGoFixture{
				entryRel: "websocket/chat/chat.gox",
				source: `module chat

import "github.com/gox-lang/gox"

func Chat(channel *gox.Channel) gox.Channel {
}
`,
			},
		},
		{
			name: "ImportedGoxModule",
			fixture: phase1GeneratedGoFixture{
				entryRel: "pages/index.gox",
				source: `module index

import Helper "./helper.gox"

func GET() string {
	return "ok"
}
`,
				files: map[string]string{
					"pages/helper.gox": `module helper

func Message() string {
	return "hello"
}
`,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase1CompileAndGoTest(t, tt.fixture)
		})
	}
}
