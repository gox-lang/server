package version

import (
	"fmt"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/cli"
)

type Command struct{}

func New() *Command { return &Command{} }

func (c *Command) Name() string { return "version" }

func (c *Command) Description() string { return "Show the GOX Server version" }

func (c *Command) Help() string {
	return `gox version

Show the GOX Server version.

Usage:
  gox version

Options:
  -h, --help           Show help information`
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: gox version")
	}

	fmt.Fprintf(ctx.Stdout, "GOX Server %s\n", buildinfo.Version())
	return nil
}
