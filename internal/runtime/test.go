package runtime

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"plugin"
	"strconv"
	"strings"
	"time"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/project"
	"github.com/gox-lang/server/internal/toolchain"
)

type LoadedTest struct {
	SourcePath      string
	GeneratedGoPath string
	ArtifactPath    string
	CompileResult   *goxcompiler.CompileResult
	Plugin          *plugin.Plugin
	Tests           map[string]func()
}

func CompileTestPlugin(p *project.Project, paths toolchain.Paths, sourcePath string, out io.Writer) (*LoadedTest, error) {
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, err
	}

	buildID := strconv.FormatInt(time.Now().UnixNano(), 10)
	base := trimGoxExt(p.Rel(absSource))
	generatedPath, artifactPath := testBuildPaths(p.RootDir, base, buildID)
	buildCompleted := false
	defer func() {
		if !buildCompleted {
			_ = os.RemoveAll(filepath.Dir(artifactPath))
		}
	}()

	if err := os.MkdirAll(filepath.Dir(generatedPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		return nil, err
	}

	entryRel := p.Rel(absSource)
	logs.Info("compiling %s", entryRel)

	result, err := goxcompiler.Compile(absSource, generatedPath)
	if err != nil {
		return nil, fmt.Errorf("%s compile failed: %w", entryRel, err)
	}
	if err := validateGeneratedTestFunctions(generatedPath); err != nil {
		return nil, fmt.Errorf("%s compile failed: %w", entryRel, err)
	}

	logs.Info("generated Go for %s: %s", entryRel, p.Rel(generatedPath))
	logs.Info("building %s test artifact: %s", entryRel, p.Rel(artifactPath))

	cmd := toolchain.Command(paths, "build", "-buildmode=plugin", "-o", artifactPath, generatedPath)
	cmd.Dir = p.RootDir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s build failed: %w", entryRel, err)
	}

	plug, err := plugin.Open(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("%s load failed: %w", entryRel, err)
	}

	sym, err := plug.Lookup("GoxTests")
	if err != nil {
		return nil, fmt.Errorf("%s must export GoxTests: %w", entryRel, err)
	}

	testsPtr, ok := sym.(*map[string]func())
	if !ok || testsPtr == nil {
		return nil, fmt.Errorf("%s GoxTests has invalid type", entryRel)
	}

	loaded := &LoadedTest{
		SourcePath:      absSource,
		GeneratedGoPath: generatedPath,
		ArtifactPath:    artifactPath,
		CompileResult:   result,
		Plugin:          plug,
		Tests:           *testsPtr,
	}
	buildCompleted = true
	return loaded, nil
}

func validateGeneratedTestFunctions(path string) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return err
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || !strings.HasSuffix(fn.Name.Name, "Test") {
			continue
		}
		params := 0
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				if len(field.Names) == 0 {
					params++
				} else {
					params += len(field.Names)
				}
			}
		}
		results := 0
		if fn.Type.Results != nil {
			for _, field := range fn.Type.Results.List {
				if len(field.Names) == 0 {
					results++
				} else {
					results += len(field.Names)
				}
			}
		}
		if params != 0 || results != 0 {
			return fmt.Errorf("%s must have signature func()", fn.Name.Name)
		}
	}
	return nil
}

func testBuildPaths(rootDir, base, buildID string) (generatedPath string, artifactPath string) {
	buildDir := filepath.Join(rootDir, ".build", "cache", "tests", fmt.Sprintf("%s-%s", safeArtifactName(base), buildID))
	return filepath.Join(buildDir, filepath.Base(base)+".go"), filepath.Join(buildDir, "test.so")
}

// CleanupTestBuildArtifacts removes the unique generated source and plugin
// directory after the plugin has been loaded into the current process.
func CleanupTestBuildArtifacts(loaded *LoadedTest) error {
	if loaded == nil || loaded.ArtifactPath == "" {
		return nil
	}
	return os.RemoveAll(filepath.Dir(loaded.ArtifactPath))
}

func trimGoxExt(path string) string {
	return filepath.ToSlash(path[:len(path)-len(filepath.Ext(path))])
}

func safeArtifactName(path string) string {
	name := filepath.ToSlash(path)
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "test"
	}
	return string(out)
}
