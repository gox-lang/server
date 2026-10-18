package main

import (
	"fmt"
	"io"
	"os"

	"github.com/gox-lang/server/internal/cli"
	clientcommand "github.com/gox-lang/server/internal/commands/client"
	createproject "github.com/gox-lang/server/internal/commands/createproject"
	getcommand "github.com/gox-lang/server/internal/commands/get"
	graphcommand "github.com/gox-lang/server/internal/commands/graph"
	installcommand "github.com/gox-lang/server/internal/commands/install"
	listcommand "github.com/gox-lang/server/internal/commands/list"
	lscommand "github.com/gox-lang/server/internal/commands/ls"
	removecommand "github.com/gox-lang/server/internal/commands/remove"
	runcommand "github.com/gox-lang/server/internal/commands/run"
	testcommand "github.com/gox-lang/server/internal/commands/test"
	tidycommand "github.com/gox-lang/server/internal/commands/tidy"
	versioncommand "github.com/gox-lang/server/internal/commands/version"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/processrestart"
	"github.com/gox-lang/server/internal/serverruntime"
)

func main() {
	logs.Init(4096)
	configureCLILogging()

	wd := os.Getenv("GOX_WORKDIR")
	if wd == "" {
		var err error
		wd, err = os.Getwd()
		if err != nil {
			writeCLIError(os.Stderr, err)
			logs.Flush()
			os.Exit(1)
		}
	}

	router := cli.NewRouter(
		clientcommand.New(),
		createproject.New(),
		getcommand.New(),
		graphcommand.New(),
		installcommand.New(),
		listcommand.New(),
		lscommand.New(),
		tidycommand.New(),
		removecommand.New(),
		runcommand.New(),
		testcommand.New(),
		versioncommand.New(),
	)

	ctx := cli.Context{
		WorkDir: wd,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}

	if err := router.Run(ctx, os.Args[1:]); err != nil {
		if _, restart := serverruntime.RestartRequestFromError(err); restart {
			if restartErr := processrestart.Current(); restartErr != nil {
				writeCLIError(ctx.Stderr, fmt.Errorf("restart Gox process: %w", restartErr))
				logs.Flush()
				os.Exit(1)
			}
			return
		}
		if silent, ok := err.(cli.SilentExitError); ok {
			logs.Flush()
			os.Exit(silent.Code)
		}
		writeCLIError(ctx.Stderr, err)
		logs.Flush()
		os.Exit(1)
	}

	logs.Flush()
}

func configureCLILogging() {
	logs.SetEnabled(false)
}

func writeCLIError(w io.Writer, err error) {
	fmt.Fprintln(w, err)
}
