package client

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/clientbuilder"
	"github.com/gox-lang/server/internal/deps"
	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/projectdefaults"
	"github.com/gox-lang/server/internal/projecttoolchain"
	"github.com/gox-lang/server/internal/toolchain"
)

type Command struct{}

func New() *Command { return &Command{} }

func (c *Command) Name() string { return "client" }

func (c *Command) Description() string { return "Manage client framework dependencies" }

func (c *Command) Help() string {
	return `gox client

Manage client framework dependencies.

Usage:
  gox client <command>

Commands:
  add       Add one or more client framework dependencies
  remove    Remove one or more client framework dependencies
  list      List installed client framework dependencies

Options:
  -h, --help           Show help information`
}

func (c *Command) HelpFor(args []string) (string, error) {
	if len(args) == 0 {
		return c.Help(), nil
	}
	if len(args) != 1 {
		return "", fmt.Errorf("usage: gox help client [add|remove|list]")
	}
	switch args[0] {
	case "add":
		return addHelp(), nil
	case "remove":
		return removeHelp(), nil
	case "list":
		return listHelp(), nil
	default:
		return "", fmt.Errorf("unknown client command: %s", args[0])
	}
}

func (c *Command) Run(ctx cli.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gox client <add|remove|list>")
	}

	switch args[0] {
	case "add":
		return runAdd(ctx, args[1:])
	case "remove":
		return runRemove(ctx, args[1:])
	case "list":
		return runList(ctx, args[1:])
	default:
		return fmt.Errorf("unknown client command: %s", args[0])
	}
}

func addHelp() string {
	return `gox client add

Add one or more client framework dependencies.

Usage:
  gox client add <framework[@version]> [framework[@version]...]

Supported frameworks:
  react, preact, solid, vue, svelte

Options:
  -h, --help           Show help information`
}

func removeHelp() string {
	return `gox client remove

Remove one or more client framework dependencies.

Usage:
  gox client remove <framework> [framework...]

Options:
  -h, --help           Show help information`
}

func listHelp() string {
	return `gox client list

List installed client framework dependencies.

Usage:
  gox client list

Options:
  -h, --help           Show help information`
}

func runAdd(ctx cli.Context, args []string) error {
	specs, err := normalizeFrameworkSpecs(args, "gox client add")
	if err != nil {
		return err
	}
	previousLogsEnabled := logs.Enabled()
	logs.SetEnabled(true)
	defer logs.SetEnabled(previousLogsEnabled)

	if err := updateFrameworkVersions(ctx.WorkDir, specs); err != nil {
		return err
	}
	frameworks := make([]string, 0, len(specs))
	for _, spec := range specs {
		frameworks = append(frameworks, spec.Framework)
	}
	logs.Info("adding client frameworks: %s", strings.Join(frameworks, ", "))
	logs.Info("checking frontend toolchain from %s", deps.GoxJSONFile)

	nodePaths, pnpmPaths, err := ensureFrontendToolchain(ctx.WorkDir)
	if err != nil {
		return err
	}

	for _, framework := range frameworks {
		logs.Info("preparing client framework workspace: %s", framework)
		workspace, err := clientbuilder.EnsureFrameworkWorkspace(ctx.WorkDir, framework)
		if err != nil {
			return err
		}
		if strings.TrimSpace(workspace.RootDir) == "" {
			return fmt.Errorf("unsupported client framework %q", framework)
		}

		logs.Info("client framework workspace: %s", workspace.RootDir)
		if err := clientbuilder.InstallFrameworkWorkspace(nodePaths, pnpmPaths, workspace); err != nil {
			return err
		}
		logs.Info("client framework ready: %s", framework)
	}

	logs.Info("building production client framework vendors: %s", strings.Join(frameworks, ", "))
	if err := clientbuilder.BuildFrameworkVendors(nodePaths, pnpmPaths, ctx.WorkDir, frameworks); err != nil {
		return err
	}
	logs.Info("client frameworks ready: %s", strings.Join(frameworks, ", "))
	return nil
}

func runRemove(ctx cli.Context, args []string) error {
	frameworks, err := normalizeFrameworks(args, "gox client remove")
	if err != nil {
		return err
	}

	for _, framework := range frameworks {
		workspacePath := filepath.Join(ctx.WorkDir, "client", framework)
		if _, err := os.Stat(workspacePath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("client framework %q is not installed", framework)
			}
			return err
		}
		if err := os.RemoveAll(workspacePath); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(ctx.WorkDir, ".build", "client", framework)); err != nil {
			return err
		}
		logs.Info("client framework removed: %s", framework)
	}
	return nil
}

func runList(ctx cli.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: gox client list")
	}
	frameworks, err := installedFrameworks(ctx.WorkDir)
	if err != nil {
		return err
	}
	if len(frameworks) == 0 {
		fmt.Fprintln(ctx.Stdout, "No client frameworks installed.")
		return nil
	}
	for _, framework := range frameworks {
		fmt.Fprintln(ctx.Stdout, framework)
	}
	return nil
}

func installedFrameworks(projectRoot string) ([]string, error) {
	clientRoot := filepath.Join(projectRoot, "client")
	entries, err := os.ReadDir(clientRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	frameworks := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(entry.Name()))
		if _, _, ok := clientbuilder.ManagedDependencies(name); !ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(clientRoot, entry.Name(), "package.json")); err == nil {
			frameworks = append(frameworks, name)
		}
	}
	sort.Strings(frameworks)
	return frameworks, nil
}

type frameworkSpec struct {
	Framework string
	Version   string
}

func normalizeFrameworkSpecs(args []string, command string) ([]frameworkSpec, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("usage: %s <framework[@version]> [framework[@version]...]", command)
	}

	seen := make(map[string]frameworkSpec, len(args))
	out := make([]frameworkSpec, 0, len(args))
	for _, arg := range args {
		raw := strings.TrimSpace(arg)
		if raw == "" {
			return nil, fmt.Errorf("missing framework name")
		}

		name, version, hasVersion := strings.Cut(raw, "@")
		name = strings.ToLower(strings.TrimSpace(name))
		version = strings.TrimSpace(version)
		if name == "" {
			return nil, fmt.Errorf("missing framework name")
		}
		if hasVersion && version == "" {
			return nil, fmt.Errorf("missing version for client framework %q", name)
		}
		if strings.Contains(version, "@") || strings.ContainsAny(version, " \t\r\n") {
			return nil, fmt.Errorf("invalid version %q for client framework %q", version, name)
		}
		if _, _, ok := clientbuilder.ManagedDependencies(name); !ok {
			return nil, fmt.Errorf("unsupported client framework %q", name)
		}

		spec := frameworkSpec{Framework: name, Version: version}
		if previous, exists := seen[name]; exists {
			if previous.Version != spec.Version {
				return nil, fmt.Errorf("conflicting versions for client framework %q", name)
			}
			continue
		}
		seen[name] = spec
		out = append(out, spec)
	}
	return out, nil
}

func updateFrameworkVersions(projectRoot string, specs []frameworkSpec) error {
	manifest, err := deps.ReadGoxJSON(projectRoot)
	if err != nil {
		return err
	}
	changed := false
	if manifest.Frameworks == nil {
		manifest.Frameworks = deps.Frameworks{}
	}
	for _, spec := range specs {
		if _, ok := manifest.Frameworks[spec.Framework]; !ok {
			manifest.Frameworks[spec.Framework] = projectdefaults.Manifest().Frameworks[spec.Framework]
			changed = true
		}
		if spec.Version == "" {
			continue
		}
		packages, ok := manifest.Frameworks[spec.Framework]
		if !ok {
			return fmt.Errorf("%s frameworks.%s is required", deps.GoxJSONFile, spec.Framework)
		}

		var managed []string
		switch spec.Framework {
		case "react":
			managed = []string{"react", "react-dom"}
		case "preact":
			managed = []string{"preact"}
		case "solid":
			managed = []string{"solid-js"}
		case "vue":
			managed = []string{"vue"}
		case "svelte":
			managed = []string{"svelte"}
		default:
			return fmt.Errorf("unsupported client framework %q", spec.Framework)
		}

		for _, name := range managed {
			if _, exists := packages[name]; !exists {
				return fmt.Errorf("%s frameworks.%s[%q] is required", deps.GoxJSONFile, spec.Framework, name)
			}
			packages[name] = spec.Version
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return deps.WriteGoxJSON(projectRoot, manifest)
}

func normalizeFrameworks(args []string, command string) ([]string, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("usage: %s <framework> [framework...]", command)
	}

	seen := make(map[string]bool, len(args))
	frameworks := make([]string, 0, len(args))
	for _, arg := range args {
		framework := strings.ToLower(strings.TrimSpace(arg))
		if framework == "" {
			return nil, fmt.Errorf("missing framework name")
		}
		if _, _, ok := clientbuilder.ManagedDependencies(framework); !ok {
			return nil, fmt.Errorf("unsupported client framework %q", framework)
		}
		if seen[framework] {
			continue
		}
		seen[framework] = true
		frameworks = append(frameworks, framework)
	}
	return frameworks, nil
}

func ensureFrontendToolchain(projectRoot string) (toolchain.NodePaths, toolchain.PNPMPaths, error) {
	nodePaths, pnpmPaths, err := projecttoolchain.Ensure(projectRoot)
	if err != nil {
		return toolchain.NodePaths{}, toolchain.PNPMPaths{}, err
	}
	logs.Info("using Node toolchain: %s", nodePaths.NodeExec)
	logs.Info("using PNPM toolchain: %s", pnpmPaths.PNPMExec)
	return nodePaths, pnpmPaths, nil
}
