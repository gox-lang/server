package list

import (
	"fmt"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/gocmd"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/toolchain"
)

type Command struct {
	listTransitive func(workDir string) ([]gocmd.Module, error)
}

func New() *Command {
	return &Command{listTransitive: listTransitiveModules}
}

func (c *Command) Name() string { return "list" }

func (c *Command) Description() string { return "List GOX project dependencies" }

func (c *Command) Help() string {
	return `gox list

List GOX project dependencies.

Usage:
  gox list
  gox list -t
  gox list --transitive

Options:
  -t, --transitive     Include transitive Go dependencies
  -h, --help           Show help information`
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	transitive, err := parseArgs(args)
	if err != nil {
		return err
	}
	if transitive {
		modules, err := c.listTransitive(ctx.WorkDir)
		if err != nil {
			return err
		}
		for _, module := range modules {
			fmt.Fprintf(ctx.Stdout, "%s\t%s\n", module.Path, module.Version)
		}
		return nil
	}

	cfg, err := deps.ReadGoxJSON(ctx.WorkDir)
	if err != nil {
		return err
	}
	for _, dep := range deps.SortedDependencies(cfg) {
		fmt.Fprintf(ctx.Stdout, "%s\t%s\n", dep.Path, dep.Version)
	}
	return nil
}

func parseArgs(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if len(args) == 1 && (args[0] == "-t" || args[0] == "--transitive") {
		return true, nil
	}
	return false, fmt.Errorf("usage: gox list [-t|--transitive]")
}

func listTransitiveModules(workDir string) ([]gocmd.Module, error) {
	tc := toolchain.New(buildinfo.GoVersion)
	paths, err := toolchain.Ensure(tc)
	if err != nil {
		return nil, err
	}
	logs.Info("using Go toolchain: %s", paths.GoExec)
	return gocmd.ListModules(paths.GoExec, workDir)
}
