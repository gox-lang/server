package update

import (
	"context"
	"fmt"
	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/selfupdate"
)

type Command struct{}

func New() *Command                  { return &Command{} }
func (*Command) Name() string        { return "update" }
func (*Command) Description() string { return "Update GOX from the official stable release" }
func (*Command) Help() string {
	return "gox update\n\nDownload, verify and install the latest stable GOX release.\nProject dependencies and running applications are not modified.\n"
}
func (*Command) Run(ctx cli.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: gox update")
	}
	manager, err := selfupdate.New(buildinfo.Version())
	if err != nil {
		return err
	}
	release, err := manager.Check(context.Background(), true)
	if err != nil {
		return err
	}
	if !manager.Newer(release) {
		fmt.Fprintf(ctx.Stdout, "GOX %s: no newer stable release available.\n", manager.Current)
		return nil
	}
	fmt.Fprintf(ctx.Stdout, "Updating GOX from %s to %s...\n", manager.Current, release.Version)
	if err := manager.Install(context.Background(), release); err != nil {
		return err
	}
	fmt.Fprintln(ctx.Stdout, "GOX updated. Existing applications continue running their current version.")
	return nil
}
