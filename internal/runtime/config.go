package runtime

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"plugin"
	"strconv"
	"time"

	"github.com/gox-lang/gox/server"

	"github.com/gox-lang/server/internal/deps"
	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/project"
	"github.com/gox-lang/server/internal/toolchain"
)

type LoadedConfig struct {
	BuildDir        string
	BuildID         string
	GeneratedGoPath string
	ArtifactPath    string
	CompileBytes    int64
	CompileResult   *goxcompiler.CompileResult
	Plugin          *plugin.Plugin
	Config          server.Config
}

func configBuildPaths(rootDir, buildID string) (generatedPath string, artifactPath string) {
	buildDir := filepath.Join(rootDir, ".build", "config", buildID)
	return filepath.Join(buildDir, "config.go"), filepath.Join(buildDir, "config.so")
}

func CompileConfigPlugin(p *project.Project, paths toolchain.Paths, out io.Writer) (*LoadedConfig, error) {
	if err := deps.EnsureSDK(p.RootDir, paths.GoExec); err != nil {
		return nil, err
	}
	entryPath := filepath.Join(p.RootDir, "app", "config.gox")
	buildID := strconv.FormatInt(time.Now().UnixNano(), 10)
	generatedPath, artifactPath := configBuildPaths(p.RootDir, buildID)

	if err := os.MkdirAll(filepath.Dir(generatedPath), 0o755); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		return nil, err
	}

	entryRel := p.Rel(entryPath)

	logs.Info("compiling %s", entryRel)

	result, err := goxcompiler.Compile(entryPath, generatedPath)

	if err != nil {
		return nil, fmt.Errorf("%s compile failed: %w", entryRel, err)
	}

	logs.Info("generated Go for %s: %s", entryRel, p.Rel(generatedPath))

	logs.Info("building %s runtime artifact: %s", entryRel, p.Rel(artifactPath))
	cmd := toolchain.Command(paths, "build", "-buildmode=plugin", "-o", artifactPath, generatedPath)
	cmd.Dir = p.RootDir
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s build failed: %w", entryRel, err)
	}

	logs.Info("loading %s", entryRel)

	plug, err := plugin.Open(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("%s load failed: %w", entryRel, err)
	}

	sym, err := plug.Lookup("SetGoxConfig")
	if err != nil {
		return nil, fmt.Errorf("%s must export func SetGoxConfig() server.Config: %w", entryRel, err)
	}

	getConfig, ok := sym.(func() server.Config)
	if !ok {
		return nil, fmt.Errorf("%s must export func SetGoxConfig() server.Config", entryRel)
	}

	config := getConfig()

	logs.Info("%s loaded", entryRel)

	return &LoadedConfig{
		BuildDir:        filepath.Dir(generatedPath),
		BuildID:         buildID,
		GeneratedGoPath: generatedPath,
		ArtifactPath:    artifactPath,
		CompileBytes:    result.OutputBytes,
		CompileResult:   result,
		Plugin:          plug,
		Config:          config,
	}, nil
}
