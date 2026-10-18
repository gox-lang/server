package install

import (
	"fmt"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/projecttoolchain"
	"github.com/gox-lang/server/internal/toolchain"
)

type Command struct{}

func New() *Command { return &Command{} }

func (c *Command) Name() string { return "install" }

func (c *Command) Description() string { return "Install GOX project dependencies" }

func (c *Command) Help() string {
	return `gox install

Install GOX project dependencies.

Usage:
  gox install

Options:
  -h, --help           Show help information`
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: gox install")
	}
	tc := toolchain.New(buildinfo.GoVersion)
	paths, err := toolchain.Ensure(tc)
	if err != nil {
		return err
	}
	logs.Info("using Go toolchain: %s", paths.GoExec)
	if err := deps.Install(ctx.WorkDir, paths.GoExec); err != nil {
		return err
	}

	nodePaths, pnpmPaths, err := projecttoolchain.Ensure(ctx.WorkDir)
	if err != nil {
		return err
	}
	logs.Info("using Node toolchain: %s", nodePaths.NodeExec)
	logs.Info("using PNPM toolchain: %s", pnpmPaths.PNPMExec)

	return nil
}
