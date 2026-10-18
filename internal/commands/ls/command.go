package ls

import (
	"errors"
	"fmt"

	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/lsp"
)

type Command struct{}

func New() *Command { return &Command{} }

func (c *Command) Name() string { return "ls" }

func (c *Command) Description() string { return "Start the GOX language server" }

func (c *Command) Help() string {
	return `gox ls

Start the GOX language server.

Usage:
  gox ls

Options:
  -h, --help           Show help information`
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: gox ls")
	}
	if ctx.Stdin == nil || ctx.Stdout == nil {
		return fmt.Errorf("gox ls requires stdin and stdout")
	}
	logsWasEnabled := logs.Enabled()
	logs.SetEnabled(false)
	defer logs.SetEnabled(logsWasEnabled)

	err := lsp.NewServer().Serve(ctx.Stdin, ctx.Stdout)
	if errors.Is(err, lsp.ErrExitWithoutShutdown) {
		return cli.SilentExit(1)
	}
	return err
}
