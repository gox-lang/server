package createproject

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/cli"
	clientcommand "github.com/gox-lang/server/internal/commands/client"
	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/projectdefaults"
	"github.com/gox-lang/server/internal/scaffold"
	"github.com/gox-lang/server/internal/toolchain"
)

type Command struct{}

func New() *Command {
	return &Command{}
}

func (c *Command) Name() string {
	return "createproject"
}

func (c *Command) Description() string {
	return "Create a new GOX project"
}

func (c *Command) Help() string {
	return `gox createproject

Create a new GOX project.

Usage:
  gox createproject <projectName>
  gox createproject [projectName] --demo

Arguments:
  projectName          Name of the project directory

Options:
  --demo              Create the GOX demo with all five client frameworks (default name: demo)
  -h, --help           Show help information`
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	name, demo, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := scaffold.ValidateProjectName(name); err != nil {
		return err
	}
	if err := scaffold.EnsureTargetDoesNotExist(filepath.Join(ctx.WorkDir, name)); err != nil {
		return err
	}

	tc := toolchain.New(buildinfo.GoVersion)

	paths, err := toolchain.Ensure(tc)
	if err != nil {
		return err
	}

	logs.Info("using Go toolchain: %s", paths.GoExec)

	nodePaths, err := toolchain.EnsureNode(toolchain.NewNode(buildinfo.NodeVersion))
	if err != nil {
		return err
	}
	logs.Info("using Node toolchain: %s", nodePaths.NodeExec)

	pnpmPaths, err := toolchain.EnsurePNPM(toolchain.NewPNPM(buildinfo.PNPMVersion), nodePaths)
	if err != nil {
		return err
	}
	logs.Info("using PNPM toolchain: %s", pnpmPaths.PNPMExec)

	opts := scaffold.Options{
		ProjectName:      name,
		TargetDir:        ctx.WorkDir,
		GoVersion:        tc.ModVersion,
		GoxModule:        buildinfo.GoxModule,
		GoxModuleVersion: buildinfo.GoxModuleVersion,
		Manifest:         projectdefaults.Manifest(),
	}

	opts.Manifest.Frameworks = deps.Frameworks{}

	prepare := func(projectOpts scaffold.Options) error {
		if err := scaffold.CreateProject(ctx, projectOpts); err != nil {
			return err
		}
		if err := deps.Install(projectOpts.ProjectPath(), paths.GoExec); err != nil {
			return err
		}
		if demo {
			projectCtx := ctx
			projectCtx.WorkDir = projectOpts.ProjectPath()
			if err := clientcommand.New().Run(projectCtx, []string{"add", "react", "preact", "solid", "vue", "svelte"}); err != nil {
				return fmt.Errorf("install demo frameworks: %w", err)
			}
		}
		return nil
	}
	if !demo {
		return prepare(opts)
	}
	if err := createDemo(opts, prepare, downloadDemo); err != nil {
		return err
	}
	output := ctx.Stdout
	if output == nil {
		output = os.Stdout
	}
	fmt.Fprintf(output, "Demo ready. Run:\n  cd %s\n  gox run\n", name)

	return nil
}

func parseArgs(args []string) (string, bool, error) {
	var name string
	var demo bool
	for _, arg := range args {
		if arg == "--demo" {
			if demo {
				return "", false, fmt.Errorf("--demo may only be specified once")
			}
			demo = true
		} else if strings.HasPrefix(arg, "-") {
			return "", false, fmt.Errorf("unknown option: %s", arg)
		} else {
			if name != "" || arg == "" {
				return "", false, fmt.Errorf("usage: gox createproject [projectName] --demo or gox createproject <projectName>")
			}
			name = arg
		}
	}
	if name == "" {
		if !demo {
			return "", false, fmt.Errorf("usage: gox createproject <projectName>")
		}
		name = "demo"
	}
	return name, demo, nil
}
