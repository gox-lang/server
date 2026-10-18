package get

import (
	"fmt"
	"strings"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/toolchain"
)

type Command struct{}

func New() *Command { return &Command{} }

func (c *Command) Name() string { return "get" }

func (c *Command) Description() string { return "Add a GOX project dependency" }

func (c *Command) Help() string {
	return `gox get

Add a GOX project dependency.

Usage:
  gox get <package>[@version]

Options:
  -h, --help           Show help information`
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gox get <package>[@version]")
	}
	path, version := splitPackageVersion(args[0])
	tc := toolchain.New(buildinfo.GoVersion)
	paths, err := toolchain.Ensure(tc)
	if err != nil {
		return err
	}
	logs.Info("using Go toolchain: %s", paths.GoExec)
	return deps.AddAndInstall(ctx.WorkDir, paths.GoExec, path, version)
}

func splitPackageVersion(input string) (string, string) {
	input = strings.TrimSpace(input)
	at := strings.LastIndex(input, "@")
	if at <= 0 {
		return input, "latest"
	}
	return input[:at], input[at+1:]
}
