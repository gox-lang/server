package toolchain

import (
	"os"
	"os/exec"
	"runtime/debug"
)

func Command(paths Paths, args ...string) *exec.Cmd {
	cmd := exec.Command(paths.GoExec, PluginBuildArgs(args)...)
	cmd.Env = append(os.Environ(),
		"GOROOT="+paths.GoRoot,
		"GOTOOLCHAIN=local",
	)

	return cmd
}

// PluginBuildArgs matches the host's path normalization so plugins built on
// another machine retain compatible package fingerprints.
func PluginBuildArgs(args []string) []string {
	info, _ := debug.ReadBuildInfo()
	return pluginBuildArgs(args, info)
}

func pluginBuildArgs(args []string, info *debug.BuildInfo) []string {
	if len(args) == 0 || args[0] != "build" || info == nil {
		return args
	}
	plugin := false
	for _, arg := range args {
		if arg == "-buildmode=plugin" {
			plugin = true
		}
		if arg == "-trimpath" {
			return args
		}
	}
	if !plugin {
		return args
	}
	for _, setting := range info.Settings {
		if setting.Key == "-trimpath" && setting.Value == "true" {
			result := append([]string{"build", "-trimpath"}, args[1:]...)
			return result
		}
	}
	return args
}
