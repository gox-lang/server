package remove

import (
	"fmt"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/toolchain"
)

type Command struct{}

func New() *Command { return &Command{} }

func (c *Command) Name() string { return "remove" }

func (c *Command) Description() string { return "Remove a GOX project dependency" }

func (c *Command) Help() string {
	return `gox remove

Remove a GOX project dependency.

Usage:
  gox remove <package>

Options:
  -h, --help           Show help information`
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gox remove <package>")
	}
	if args[0] == buildinfo.GoxModule {
		return fmt.Errorf("cannot remove protected dependency %s", buildinfo.GoxModule)
	}
	tc := toolchain.New(buildinfo.GoVersion)
	paths, err := toolchain.Ensure(tc)
	if err != nil {
		return err
	}
	logs.Info("using Go toolchain: %s", paths.GoExec)
	return deps.RemoveAndInstall(ctx.WorkDir, paths.GoExec, args[0])
}
