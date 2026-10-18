package gocmd

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Module describes a module in the effective Go module graph.
type Module struct {
	Path    string
	Version string
}

// ListModules returns every non-main module in the effective Go module graph.
func ListModules(goExec string, workDir string) ([]Module, error) {
	cmd := exec.Command(goExec, "list", "-m", "-f", "{{if not .Main}}{{.Path}}\t{{.Version}}{{end}}", "all")
	cmd.Dir = workDir
	cmd.Env = goCommandEnv(goExec)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		logCommandOutput(stderr.String(), true)
		return nil, fmt.Errorf("go list -m all failed: %w", err)
	}

	modules, err := parseModuleList(stdout.String())
	if err != nil {
		return nil, err
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })
	return modules, nil
}

func parseModuleList(output string) ([]Module, error) {
	var modules []Module
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 || strings.TrimSpace(fields[0]) == "" {
			return nil, fmt.Errorf("invalid go module list entry: %q", line)
		}
		modules = append(modules, Module{
			Path:    strings.TrimSpace(fields[0]),
			Version: strings.TrimSpace(fields[1]),
		})
	}
	return modules, nil
}
