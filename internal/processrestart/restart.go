package processrestart

import (
	"os"
	"syscall"
)

type executableResolver func() (string, error)
type processExec func(string, []string, []string) error

// Current replaces the running GOX process with a fresh instance of the same
// executable while preserving its arguments, environment and open stdio file
// descriptors.
func Current() error {
	return replace(os.Executable, syscall.Exec, os.Args, os.Environ())
}

func replace(resolve executableResolver, exec processExec, args, environment []string) error {
	executable, err := resolve()
	if err != nil {
		return err
	}
	return exec(executable, args, environment)
}
