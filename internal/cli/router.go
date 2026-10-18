package cli

import (
	"fmt"
	"sort"
	"strings"
)

type Router struct {
	commands map[string]Command
	order    []string
}

func NewRouter(commands ...Command) *Router {
	r := &Router{commands: make(map[string]Command, len(commands)), order: make([]string, 0, len(commands))}
	for _, cmd := range commands {
		name := cmd.Name()
		r.commands[name] = cmd
		r.order = append(r.order, name)
	}
	return r
}

func (r *Router) Run(ctx Context, args []string) error {
	if len(args) == 0 || (len(args) == 1 && isHelpOption(args[0])) {
		fmt.Fprintln(ctx.Stdout, r.rootHelp())
		return nil
	}
	if args[0] == "help" {
		if len(args) == 1 || (len(args) == 2 && isHelpOption(args[1])) {
			fmt.Fprintln(ctx.Stdout, r.rootHelp())
			return nil
		}
		cmd, ok := r.commands[args[1]]
		if !ok {
			return fmt.Errorf("unknown command: %s", args[1])
		}
		path := args[2:]
		for _, arg := range path {
			if strings.HasPrefix(arg, "-") {
				return r.printHelpForInvocation(ctx, cmd, path)
			}
		}
		return r.printCommandHelp(ctx, cmd, path)
	}
	cmd, ok := r.commands[args[0]]
	if !ok {
		return fmt.Errorf("unknown command: %s", args[0])
	}
	commandArgs := args[1:]
	if containsHelpOption(commandArgs) {
		return r.printHelpForInvocation(ctx, cmd, commandArgs)
	}
	return cmd.Run(ctx, commandArgs)
}

func isHelpOption(arg string) bool { return arg == "-h" || arg == "--help" }
func containsHelpOption(args []string) bool {
	for _, arg := range args {
		if isHelpOption(arg) {
			return true
		}
	}
	return false
}

func (r *Router) printHelpForInvocation(ctx Context, cmd Command, args []string) error {
	contextual, ok := cmd.(ContextualHelp)
	if !ok {
		fmt.Fprintln(ctx.Stdout, cmd.Help())
		return nil
	}
	path := make([]string, 0, len(args))
	for _, arg := range args {
		if isHelpOption(arg) || strings.HasPrefix(arg, "-") {
			break
		}
		path = append(path, arg)
	}
	for n := len(path); n >= 0; n-- {
		help, err := contextual.HelpFor(path[:n])
		if err == nil {
			fmt.Fprintln(ctx.Stdout, help)
			return nil
		}
	}
	return r.printCommandHelp(ctx, cmd, nil)
}

func (r *Router) printCommandHelp(ctx Context, cmd Command, path []string) error {
	if contextual, ok := cmd.(ContextualHelp); ok {
		help, err := contextual.HelpFor(path)
		if err != nil {
			return err
		}
		fmt.Fprintln(ctx.Stdout, help)
		return nil
	}
	if len(path) != 0 {
		return fmt.Errorf("command %s has no subcommand %s", cmd.Name(), strings.Join(path, " "))
	}
	fmt.Fprintln(ctx.Stdout, cmd.Help())
	return nil
}

func (r *Router) rootHelp() string {
	order := append([]string(nil), r.order...)
	if len(order) == 0 {
		for name := range r.commands {
			order = append(order, name)
		}
		sort.Strings(order)
	}
	var b strings.Builder
	b.WriteString("gox\n\nUsage:\n  gox <command> [options]\n\nCommands:\n")
	for _, name := range order {
		cmd := r.commands[name]
		fmt.Fprintf(&b, "  %-16s %s\n", name, cmd.Description())
	}
	b.WriteString("  help             Show help information\n\nOptions:\n  -h, --help        Show help information")
	return b.String()
}
