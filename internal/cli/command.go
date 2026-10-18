package cli

type Command interface {
	Name() string
	Description() string
	Help() string
	Run(ctx Context, args []string) error
}

// ContextualHelp is implemented by commands with subcommands. The arguments
// identify the requested subcommand path, for example ["add"] for
// "gox help client add".
type ContextualHelp interface {
	HelpFor(args []string) (string, error)
}
