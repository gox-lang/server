package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gox-lang/server/internal/goxsource"
)

type Project struct {
	RootDir string
}

func Detect(cwd string) (*Project, error) {
	root, err := findGoModRoot(cwd)
	if err != nil {
		return nil, err
	}

	p := &Project{RootDir: root}
	if err := p.Validate(); err != nil {
		return nil, err
	}

	return p, nil
}

// UseAsWorkingDir makes the project root the process working directory.
// User code compiled by Gox runs in-process, so relative paths used with
// standard Go APIs such as os.Open resolve from the project root.
func (p *Project) UseAsWorkingDir() error {
	if p == nil || strings.TrimSpace(p.RootDir) == "" {
		return fmt.Errorf("project root is empty")
	}
	if err := os.Chdir(p.RootDir); err != nil {
		return fmt.Errorf("change working directory to project root %s: %w", p.RootDir, err)
	}
	return nil
}

func findGoModRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}

	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod from %s", start)
		}

		dir = parent
	}
}

func (p *Project) Validate() error {
	requiredDirs := []string{
		".build",
		"app",
		"pages",
		"tests",
		"public",
	}

	for _, dir := range requiredDirs {
		if err := expectDir(filepath.Join(p.RootDir, dir)); err != nil {
			return err
		}
	}

	requiredFiles := []string{
		"go.mod",
		"app/config.gox",
	}

	for _, file := range requiredFiles {
		if err := expectFile(filepath.Join(p.RootDir, file)); err != nil {
			return err
		}
	}

	return nil
}

func (p *Project) Rel(path string) string {
	rel, err := filepath.Rel(p.RootDir, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func expectDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("expected directory %s: %w", path, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("expected directory %s, found file", path)
	}

	return nil
}

func expectFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("expected file %s: %w", path, err)
	}

	if info.IsDir() {
		return fmt.Errorf("expected file %s, found directory", path)
	}

	return nil
}

func CleanRoute(path string) string {
	route, _ := goxsource.PublicPageRoute(path)
	return route
}
