package lsp

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gox-lang/server/internal/clientframework"
	"github.com/gox-lang/server/internal/clienttypes"
	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type InitializeParams struct {
	RootPath         string            `json:"rootPath,omitempty"`
	RootURI          string            `json:"rootUri,omitempty"`
	WorkspaceFolders []WorkspaceFolder `json:"workspaceFolders,omitempty"`
}

type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type DidChangeWatchedFilesParams struct {
	Changes []FileEvent `json:"changes"`
}

type FileEvent struct {
	URI  string `json:"uri"`
	Type int    `json:"type"`
}

type CreateFilesParams struct {
	Files []FileCreate `json:"files"`
}

type FileCreate struct {
	URI string `json:"uri"`
}

type DeleteFilesParams struct {
	Files []FileDelete `json:"files"`
}

type FileDelete struct {
	URI string `json:"uri"`
}

type RenameFilesParams struct {
	Files []FileRename `json:"files"`
}

type FileRename struct {
	OldURI string `json:"oldUri"`
	NewURI string `json:"newUri"`
}

const (
	fileCreated = 1
	fileChanged = 2
	fileDeleted = 3
)

func workspaceRoots(params InitializeParams) []string {
	var roots []string
	seen := make(map[string]struct{})
	appendRoot := func(path string) {
		if path == "" {
			return
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return
		}
		absolute = filepath.Clean(absolute)
		if _, exists := seen[absolute]; exists {
			return
		}
		seen[absolute] = struct{}{}
		roots = append(roots, absolute)
	}
	for _, folder := range params.WorkspaceFolders {
		if path, err := FilePath(folder.URI); err == nil {
			appendRoot(path)
		}
	}
	if len(roots) == 0 && params.RootURI != "" {
		if path, err := FilePath(params.RootURI); err == nil {
			appendRoot(path)
		}
	}
	if len(roots) == 0 {
		appendRoot(params.RootPath)
	}
	return roots
}

func (s *Server) indexWorkspaceRoots(roots []string) {
	for _, root := range roots {
		s.rememberWorkspaceRoot(root)
		projectSources := map[string][]string{}
		projectRoots := map[string]struct{}{}
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				if path != root && ignoredWorkspaceDirectory(entry.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Name() == "go.mod" {
				projectRoots[filepath.Dir(path)] = struct{}{}
			}
			if strings.EqualFold(filepath.Ext(entry.Name()), ".gox") {
				projectRoot := s.projectRoot(path)
				projectRoots[projectRoot] = struct{}{}
				projectSources[projectRoot] = append(projectSources[projectRoot], path)
				s.refreshDiskPath(path)
			}
			return nil
		})
		for projectRoot := range projectRoots {
			s.bootstrapGeneratedTypes(projectRoot, projectSources[projectRoot])
		}
	}
}

func (s *Server) bootstrapGeneratedTypes(projectRoot string, sourcePaths []string) {
	_ = clienttypes.EnsureStatic(projectRoot)
	_ = goxcompiler.PruneWebSocketEventTypesSources(projectRoot, sourcePaths)
	_ = goxcompiler.PruneActionTypesSources(projectRoot, sourcePaths)
}

func (s *Server) rememberWorkspaceRoot(root string) {
	root, err := filepath.Abs(root)
	if err != nil {
		return
	}
	root = filepath.Clean(root)
	for _, known := range s.workspaceRoots {
		if samePath(known, root) {
			return
		}
	}
	s.workspaceRoots = append(s.workspaceRoots, root)
}

func ignoredWorkspaceDirectory(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".toolchain":
		return true
	default:
		return false
	}
}

func (s *Server) refreshDiskURI(uri string) {
	path, err := FilePath(uri)
	if err != nil || !strings.EqualFold(filepath.Ext(path), ".gox") {
		return
	}
	s.refreshDiskPath(path)
}

func (s *Server) refreshDiskPath(path string) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return
	}
	content, err := os.ReadFile(absolute)
	if err != nil {
		return
	}
	uri := FileURI(absolute)
	document := Document{URI: uri, Path: absolute, LanguageID: "gox", Text: string(content)}
	if err := s.symbols.UpdateDisk(document); err == nil {
		s.syncGeneratedTypes(document)
	}
}

func (s *Server) removeDiskURI(uri string) {
	if path, err := FilePath(uri); err == nil && strings.EqualFold(filepath.Ext(path), ".gox") {
		s.symbols.RemoveDisk(FileURI(path))
		s.removeGeneratedTypes(path)
	}
}

func (s *Server) syncGeneratedTypes(document Document) {
	if !strings.EqualFold(filepath.Ext(document.Path), ".gox") {
		return
	}
	root := s.projectRoot(document.Path)
	module, err := goxcompiler.ParseProjectModule(root, document.Path, document.Text)
	if err != nil {
		s.removeGeneratedTypes(document.Path)
		return
	}
	s.syncGeneratedTypesModule(document, module)
}

func (s *Server) syncGeneratedTypesModule(document Document, module *goxcompiler.Module) {
	if !strings.EqualFold(filepath.Ext(document.Path), ".gox") || module == nil {
		return
	}
	root := s.projectRoot(document.Path)
	_ = goxcompiler.UpdateWebSocketEventTypesForModule(root, module)
	_ = goxcompiler.UpdateActionTypesForModule(root, module)
}

func (s *Server) removeGeneratedTypes(path string) {
	if !strings.EqualFold(filepath.Ext(path), ".gox") {
		return
	}
	root := s.projectRoot(path)
	_ = goxcompiler.RemoveWebSocketEventTypesForSource(root, path)
	_ = goxcompiler.RemoveActionTypesForSource(root, path)
}

func goDependencyFileURI(uri string) bool {
	path, err := FilePath(uri)
	if err != nil {
		return false
	}
	name := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(name, "_test.go") {
		return false
	}
	return name == "go.mod" || name == "go.sum" || name == "go.work" || strings.EqualFold(filepath.Ext(path), ".go")
}

func (s *Server) clientWorkspaceRootForURI(uri string) (string, bool) {
	path, err := FilePath(uri)
	if err != nil {
		return "", false
	}
	root := s.projectRoot(path)
	if root == "" {
		return "", false
	}
	clientRoot := filepath.Join(root, "client")
	relative, err := filepath.Rel(clientRoot, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return "", false
	}
	definition, ok := clientframework.Lookup(strings.ToLower(strings.TrimSpace(parts[0])))
	if !ok || definition.Status != clientframework.Supported {
		return "", false
	}
	return root, true
}

func FileURI(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	slashPath := filepath.ToSlash(absolute)
	if runtime.GOOS == "windows" && len(slashPath) >= 2 && slashPath[1] == ':' {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}
