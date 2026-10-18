package createproject

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gox-lang/server/internal/logs"
	"github.com/gox-lang/server/internal/scaffold"
)

const demoArchiveURL = "https://codeload.github.com/gox-lang/demo/zip/refs/heads/main"
const maxDemoArchive = 32 << 20
const maxDemoExpanded = 128 << 20

// Prepare everything beside the destination, on the same filesystem. Only a
// successful setup may reserve the final directory; an existing path is never used.
func createDemo(opts scaffold.Options, prepare func(scaffold.Options) error, download func(string) error) error {
	destination := opts.ProjectPath()
	if err := scaffold.EnsureTargetDoesNotExist(destination); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(opts.TargetDir, ".gox-demo-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	staged := opts
	staged.TargetDir = staging
	if err := prepare(staged); err != nil {
		return err
	}
	if err := download(staged.ProjectPath()); err != nil {
		return fmt.Errorf("download demo: %w", err)
	}
	// Mkdir reserves the name without replacing even an empty directory or symlink
	// created by another process while dependencies were being installed.
	if err := os.Mkdir(destination, 0755); err != nil {
		return fmt.Errorf("create demo destination: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			os.RemoveAll(destination)
		}
	}()
	entries, err := os.ReadDir(staged.ProjectPath())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(staged.ProjectPath(), entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	completed = true
	return nil
}

func downloadDemo(root string) error {
	logs.Info("downloading GOX demo from gox-lang/demo...")
	client := &http.Client{Timeout: 2 * time.Minute}
	return fetchDemo(client, demoArchiveURL, root)
}

func fetchDemo(client *http.Client, url, root string) error {
	response, err := client.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("GOX demo repository or main branch is unavailable; gox-lang/demo must be publicly accessible (HTTP 404)")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("demo download returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDemoArchive+1))
	if err != nil {
		return err
	}
	if len(data) > maxDemoArchive {
		return fmt.Errorf("demo archive exceeds size limit")
	}
	return extractDemo(data, root)
}

// Copy only demo source and documentation. Project identity, dependency files,
// toolchain workspaces and repository metadata belong to the generated project.
func demoFileAllowed(name string) bool {
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == ".git" || part == "node_modules" || part == ".build" || part == ".env" || strings.HasPrefix(part, ".env.") {
			return false
		}
	}
	switch parts[0] {
	case "pages", "public", "websocket", "tests":
		return len(parts) > 1
	case "app":
		return name == "app/routes.gox" || name == "app/websocket.gox"
	case "client":
		if len(parts) < 4 || parts[2] != "playground" {
			return false
		}
		switch parts[1] {
		case "react", "preact", "solid", "vue", "svelte":
			return true
		}
	}
	switch name {
	case "README.md", "LICENSE", ".gitignore", ".gitattributes", "CONTRIBUTING.md", "SECURITY.md":
		return true
	}
	return false
}

func extractDemo(data []byte, root string) error {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("invalid demo archive: %w", err)
	}
	prefix := ""
	seen := map[string]bool{}
	var total uint64
	// Validate all entries before writing anything.
	for _, file := range archive.File {
		name := strings.TrimSuffix(file.Name, "/")
		if name == "" || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("invalid demo archive path: %q", file.Name)
		}
		parts := strings.SplitN(name, "/", 2)
		if prefix == "" {
			prefix = parts[0]
		}
		if parts[0] != prefix {
			return fmt.Errorf("demo archive has multiple roots")
		}
		if file.Mode()&os.ModeType != 0 && !file.FileInfo().IsDir() {
			return fmt.Errorf("unsupported demo archive entry: %s", name)
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if len(parts) != 2 {
			return fmt.Errorf("demo archive entry lacks repository root")
		}
		if seen[parts[1]] {
			return fmt.Errorf("duplicate demo archive path: %s", name)
		}
		seen[parts[1]] = true
		if file.UncompressedSize64 > maxDemoExpanded || total > maxDemoExpanded-file.UncompressedSize64 {
			return fmt.Errorf("expanded demo exceeds size limit")
		}
		total += file.UncompressedSize64
	}
	if !seen["pages/index.gox"] || !seen["LICENSE"] {
		return fmt.Errorf("demo archive is missing required files")
	}
	for _, file := range archive.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := strings.TrimPrefix(file.Name, prefix+"/")
		if !demoFileAllowed(name) {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		contents, readErr := io.ReadAll(io.LimitReader(reader, maxDemoExpanded+1))
		reader.Close()
		if readErr != nil {
			return readErr
		}
		if len(contents) > maxDemoExpanded {
			return fmt.Errorf("demo file exceeds size limit")
		}
		if err := os.WriteFile(target, contents, 0644); err != nil {
			return err
		}
	}
	return nil
}
