package serverruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cleanupGoxRouteBuildArtifacts removes stale generated Go and plugin files for
// one route after the replacement plugin has been loaded successfully.
func cleanupGoxRouteBuildArtifacts(projectRoot string, route *GoxRouteRuntime) error {
	return cleanupGoxRouteBuildArtifactsWithGrace(projectRoot, route, 0)
}

func cleanupGoxRouteBuildArtifactsWithGrace(projectRoot string, route *GoxRouteRuntime, grace time.Duration) error {
	if route == nil {
		return nil
	}

	buildDir := filepath.Clean(route.BuildDir)
	pagesRoot := filepath.Clean(filepath.Join(projectRoot, ".build", "pages"))
	webSocketRoot := filepath.Clean(filepath.Join(projectRoot, ".build", "websocket"))
	channelRoot := filepath.Clean(filepath.Join(webSocketRoot, "channels"))
	safeBuildDir := isPathInside(buildDir, pagesRoot) && buildDir != pagesRoot
	if route.RoutePath == webSocketModuleRoutePath {
		safeBuildDir = buildDir == webSocketRoot
	}
	if isChannelRoutePath(route.RoutePath) {
		safeBuildDir = isPathInside(buildDir, channelRoot) && buildDir != channelRoot
	}
	if projectRoot == "" || buildDir == "." || !safeBuildDir {
		return fmt.Errorf("refusing to clean unsafe gox build directory: %q", route.BuildDir)
	}

	keep := map[string]struct{}{}
	currentSourceMapPath := ""
	if route.CompileResult != nil {
		currentSourceMapPath = route.CompileResult.SourceMapPath
	}
	for _, path := range []string{route.GeneratedPath, route.PluginPath, currentSourceMapPath} {
		base := strings.TrimSpace(filepath.Base(path))
		if base != "" && base != "." {
			keep[base] = struct{}{}
		}
	}
	manifestBase := strings.TrimSpace(route.BuildVersion) + ".manifest.json"
	if strings.TrimSpace(route.BuildVersion) != "" {
		keep[manifestBase] = struct{}{}
	}
	if len(keep) < 2 || len(keep) > 4 {
		return fmt.Errorf("refusing to clean gox build directory without current artifacts: %q", buildDir)
	}

	entries, err := os.ReadDir(buildDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if _, ok := keep[entry.Name()]; ok {
			continue
		}
		if grace > 0 {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if time.Since(info.ModTime()) < grace {
				continue
			}
		}
		if err := os.RemoveAll(filepath.Join(buildDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
