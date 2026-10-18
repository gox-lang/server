package clientbuilder

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DetectFrameworkWorkspaces returns the managed client frameworks installed in
// the project's client directory.
func DetectFrameworkWorkspaces(projectRoot string) []string {
	root := filepath.Join(projectRoot, "client")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	frameworks := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		framework := strings.ToLower(strings.TrimSpace(entry.Name()))
		if framework == "" {
			continue
		}
		if _, _, ok := ManagedDependencies(framework); !ok {
			continue
		}

		frameworks = append(frameworks, framework)
	}

	sort.Strings(frameworks)
	return frameworks
}
