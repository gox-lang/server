package toolchain

import (
	"os"
	"os/exec"
)

func Command(paths Paths, args ...string) *exec.Cmd {
	cmd := exec.Command(paths.GoExec, args...)
	cmd.Env = append(os.Environ(),
		"GOROOT="+paths.GoRoot,
		"GOTOOLCHAIN=local",
	)

	return cmd
}
