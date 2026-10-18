package tidy

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

func (c *Command) Name() string { return "tidy" }

func (c *Command) Description() string { return "Synchronize Go module files from gox.json" }

func (c *Command) Help() string {
	return `gox tidy

Synchronize Go module files from gox.json.

Usage:
  gox tidy

Options:
  -h, --help            Show help information`
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: gox tidy")
	}

	tc := toolchain.New(buildinfo.GoVersion)
	paths, err := toolchain.Ensure(tc)
	if err != nil {
		return err
	}
	logs.Info("using Go toolchain: %s", paths.GoExec)
	return deps.Tidy(ctx.WorkDir, paths.GoExec)
}
