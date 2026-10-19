package run

import (
	"context"
	"flag"
	"fmt"
	"github.com/gox-lang/server/internal/selfupdate"
	"io"
	"net"
	"strings"

	"github.com/gox-lang/server/internal/buildinfo"
	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/project"
	goxruntime "github.com/gox-lang/server/internal/runtime"
	"github.com/gox-lang/server/internal/serverruntime"
	"github.com/gox-lang/server/internal/toolchain"
)

type Command struct{}

func New() *Command {
	return &Command{}
}

func (c *Command) Name() string {
	return "run"
}

func (c *Command) Description() string {
	return "Run the GOX server"
}

func (c *Command) Help() string {
	return `gox run

Run the GOX server.

Usage:
  gox run [options]

Options:
  --dev                 Enable development mode
  --host <host>         Override the HTTP host
  --port <port>         Override the HTTP port (1-65535)
  -h, --help            Show help information`
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	logs.SetEnabled(true)

	options, err := parseOptions(ctx, args)
	if err != nil {
		return err
	}

	if options.dev {
		logs.Info("running Gox in development mode")
	} else {
		logs.Info("running Gox")
	}

	logs.Info("checking Gox project...")

	p, err := project.Detect(ctx.WorkDir)
	if err != nil {
		return err
	}

	logs.Info("project root: %s", p.RootDir)
	if err := p.UseAsWorkingDir(); err != nil {
		return err
	}

	tc := toolchain.New(buildinfo.GoVersion)

	paths, err := toolchain.Ensure(tc)
	if err != nil {
		return err
	}

	logs.Info("using Go toolchain: %s", paths.GoExec)

	loadedConfig, err := goxruntime.CompileConfigPlugin(p, paths, ctx.Stdout)
	if err != nil {
		return err
	}
	runtime, err := serverruntime.New(
		loadedConfig.Config,
		ctx.Stdout,
		p,
		paths,
		loadedConfig.CompileResult,
		options.runtimeOverrides(),
	)
	if err != nil {
		if serverruntime.IsErrorKind(err, "server-start-hook-failed") {
			return cli.SilentExit(1)
		}
		return err
	}
	if err := goxruntime.CleanupConfigBuildArtifacts(p.RootDir, loadedConfig); err != nil {
		return err
	}

	updateCtx, stopUpdates := context.WithCancel(context.Background())
	defer stopUpdates()
	if manager, err := selfupdate.New(buildinfo.Version()); err == nil {
		go manager.Watch(updateCtx, func(release selfupdate.Release) {
			logs.Info("GOX %s is available. Run gox update to install it.", release.Version)
		})
	}
	return runtime.ListenAndServe()
}

type runOptions struct {
	dev     bool
	host    string
	hostSet bool
	port    int
	portSet bool
}

func (options runOptions) runtimeOverrides() serverruntime.ConfigOverrides {
	return serverruntime.ConfigOverrides{
		DevEnabled:    options.dev,
		DevEnabledSet: options.dev,
		HTTPHost:      options.host,
		HTTPHostSet:   options.hostSet,
		HTTPPort:      options.port,
		HTTPPortSet:   options.portSet,
	}
}

func parseOptions(ctx cli.Context, args []string) (runOptions, error) {
	for _, argument := range args {
		if strings.HasPrefix(argument, "--dev=") {
			return runOptions{}, fmt.Errorf("option --dev does not accept a value")
		}
	}
	normalized, err := cli.NormalizeOptions(args, nil, map[string]struct{}{"dev": {}, "host": {}, "port": {}})
	if err != nil {
		return runOptions{}, err
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dev := fs.Bool("dev", false, "enable development mode")
	host := fs.String("host", "", "override HTTP host")
	port := fs.Int("port", 0, "override HTTP port")
	if err := rejectDuplicateOptions(args, "dev", "host", "port"); err != nil {
		return runOptions{}, err
	}

	if err := fs.Parse(normalized); err != nil {
		return runOptions{}, err
	}
	if fs.NArg() != 0 {
		return runOptions{}, fmt.Errorf("gox run does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	options := runOptions{dev: *dev}
	fs.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "host":
			options.hostSet = true
		case "port":
			options.portSet = true
		}
	})
	if options.hostSet {
		options.host = strings.TrimSpace(*host)
		if !validHost(options.host) {
			return runOptions{}, fmt.Errorf("invalid --host %q", *host)
		}
	}
	if options.portSet {
		options.port = *port
		if options.port < 1 || options.port > 65535 {
			return runOptions{}, fmt.Errorf("invalid --port %d: expected a value between 1 and 65535", options.port)
		}
	}
	return options, nil
}

func rejectDuplicateOptions(args []string, names ...string) error {
	seen := make(map[string]bool, len(names))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		for _, name := range names {
			plain := "--" + name
			if argument != plain && !strings.HasPrefix(argument, plain+"=") {
				continue
			}
			if seen[name] {
				return fmt.Errorf("option --%s cannot be specified more than once", name)
			}
			seen[name] = true
			break
		}
	}
	return nil
}

func validHost(host string) bool {
	if host == "" || strings.ContainsAny(host, " \t\r\n/\\") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}
