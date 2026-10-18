package gocmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gox-lang/server/internal/logs"
)

func ModTidy(goExec string, workDir string) error {
	logs.Info("running go mod tidy...")
	if err := runGoCommand(goExec, workDir, "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}
	logs.Info("go mod tidy complete")
	return nil
}

func Get(goExec string, workDir string, pkg string) error {
	logs.Info("running go get %s...", pkg)
	if err := runGoCommand(goExec, workDir, "get", pkg); err != nil {
		return fmt.Errorf("go get %s failed: %w", pkg, err)
	}
	logs.Info("go get %s complete", pkg)
	return nil
}

func runGoCommand(goExec string, workDir string, args ...string) error {
	cmd := exec.Command(goExec, args...)
	cmd.Dir = workDir
	cmd.Env = goCommandEnv(goExec)

	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Run(); err != nil {
		logCommandOutput(output.String(), true)
		return err
	}

	logCommandOutput(output.String(), false)
	return nil
}

func logCommandOutput(output string, asError bool) {
	output = strings.TrimSpace(output)
	if output == "" {
		return
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if asError {
			logs.Error("%s", line)
		} else {
			logs.Info("%s", line)
		}
	}
}

func goCommandEnv(goExec string) []string {
	goRoot := filepath.Dir(filepath.Dir(goExec))
	return append(os.Environ(),
		"GOROOT="+goRoot,
		"GOTOOLCHAIN=local",
	)
}
