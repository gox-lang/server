package goxsource

import (
	"fmt"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ModuleKind string

const (
	ModuleOther            ModuleKind = "other"
	ModulePublicPage       ModuleKind = "public-page"
	ModulePrivatePage      ModuleKind = "private-page"
	ModulePageAlias        ModuleKind = "page-alias"
	ModuleGlobalWebSocket  ModuleKind = "global-websocket"
	ModulePublicChannel    ModuleKind = "public-channel"
	ModulePrivateWebSocket ModuleKind = "private-websocket"
	ModuleWebSocketAlias   ModuleKind = "websocket-alias"
)

type ModuleContext struct {
	Kind            ModuleKind
	RelativePath    string
	ChannelName     string
	ChannelFunction string
}

// ClassifyModule describes the filesystem role of a .gox source file. It does
// not validate which GOX entrypoints the module declares; compiler validation
// applies those rules separately.
func ClassifyModule(rel string) ModuleContext {
	rel = structuralRelativePath(rel)
	ctx := ModuleContext{Kind: ModuleOther, RelativePath: rel}
	if path.Ext(rel) != ".gox" {
		return ctx
	}

	switch {
	case strings.HasPrefix(rel, "pages/"):
		parts := strings.Split(strings.TrimPrefix(rel, "pages/"), "/")
		if hasAliasSegment(parts) {
			ctx.Kind = ModulePageAlias
		} else if isPublicGOXPath(parts) {
			ctx.Kind = ModulePublicPage
		} else {
			ctx.Kind = ModulePrivatePage
		}
	case strings.HasPrefix(rel, "websocket/"):
		inside := strings.TrimPrefix(rel, "websocket/")
		if inside == "index.gox" {
			ctx.Kind = ModuleGlobalWebSocket
			return ctx
		}
		parts := strings.Split(inside, "/")
		if hasAliasSegment(parts) {
			ctx.Kind = ModuleWebSocketAlias
		} else if isPublicGOXPath(parts) {
			name, function, ok := PublicChannelIdentity(rel)
			if ok {
				ctx.Kind = ModulePublicChannel
				ctx.ChannelName = name
				ctx.ChannelFunction = function
			} else {
				ctx.Kind = ModulePrivateWebSocket
			}
		} else {
			ctx.Kind = ModulePrivateWebSocket
		}
	}
	return ctx
}

func structuralRelativePath(value string) string {
	value = filepath.ToSlash(value)
	if !path.IsAbs(value) && !(len(value) >= 2 && value[1] == ':') {
		return strings.TrimPrefix(value, "./")
	}
	for _, root := range []string{"/pages/", "/websocket/"} {
		if index := strings.LastIndex(value, root); index >= 0 {
			return strings.TrimPrefix(value[index+1:], "./")
		}
	}
	return strings.TrimPrefix(value, "./")
}

func hasAliasSegment(parts []string) bool {
	for _, part := range parts {
		if strings.HasPrefix(part, "@") {
			return true
		}
	}
	return false
}

func isPublicGOXPath(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if part == "" || strings.HasPrefix(part, "@") {
			return false
		}
	}
	base := strings.TrimSuffix(parts[len(parts)-1], ".gox")
	return base != "" && !strings.Contains(base, ".")
}

// PublicChannelIdentity returns the canonical static channel name and handler
// function represented by a public .gox file below websocket/. The root
// websocket/index.gox is reserved for the global WebSocket and is never a
// channel. Repeated terminal segments collapse (chat/chat.gox -> Chat), while
// nested names remain hierarchical (chat/index.gox -> Chat/Index).
func PublicChannelIdentity(rel string) (string, string, bool) {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if !strings.HasPrefix(rel, "websocket/") || path.Ext(rel) != ".gox" {
		return "", "", false
	}
	inside := strings.TrimPrefix(rel, "websocket/")
	if inside == "index.gox" {
		return "", "", false
	}
	parts := strings.Split(strings.TrimSuffix(inside, ".gox"), "/")
	if !isPublicGOXPath(strings.Split(inside, "/")) {
		return "", "", false
	}
	if len(parts) > 1 && parts[len(parts)-1] == parts[len(parts)-2] {
		parts = parts[:len(parts)-1]
	}
	for i := range parts {
		parts[i] = exportedSegment(parts[i])
		if !tokenIdentifier(parts[i]) {
			return "", "", false
		}
	}
	return strings.Join(parts, "/"), parts[len(parts)-1], true
}

func exportedSegment(segment string) string {
	first, size := utf8.DecodeRuneInString(segment)
	if first == utf8.RuneError && size == 0 {
		return segment
	}
	return string(unicode.ToUpper(first)) + segment[size:]
}

func tokenIdentifier(value string) bool {
	return token.IsIdentifier(value)
}

// IsPublicPage reports whether rel is a routable .gox file below pages/.
// A segment beginning with @ and a filename with an additional dot are private.
func IsPublicPage(rel string) bool {
	return ClassifyModule(rel).Kind == ModulePublicPage
}

// PublicPageRoute returns the canonical HTTP route represented by a public
// page path relative to the project root.
func PublicPageRoute(rel string) (string, bool) {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if !IsPublicPage(rel) {
		return "", false
	}

	value := strings.TrimSuffix(strings.TrimPrefix(rel, "pages/"), ".gox")
	value = strings.ToLower(strings.Trim(value, "/"))
	if value == "index" {
		return "/", true
	}

	parts := strings.Split(value, "/")
	if len(parts) >= 2 && parts[len(parts)-1] == parts[len(parts)-2] {
		parts = parts[:len(parts)-1]
	}
	return "/" + strings.Join(parts, "/"), true
}

type Resolution struct {
	ImportPath   string
	ResolvedPath string
	Candidates   []string
}

// ResolveAlias resolves an @ import by searching only the importer's directory
// and its ancestors up to sourceRoot. The first existing regular file wins.
func ResolveAlias(sourceRoot, importerPath, importPath string) (Resolution, error) {
	result := Resolution{ImportPath: importPath}
	if importPath == "" || !strings.HasPrefix(importPath, "@") {
		return result, fmt.Errorf("alias import must begin with @")
	}
	if strings.Contains(importPath, "\\") || path.IsAbs(importPath) {
		return result, fmt.Errorf("alias import %q must use a slash-separated relative path", importPath)
	}
	if path.Ext(importPath) != ".gox" {
		return result, fmt.Errorf("alias import %q must include the .gox extension", importPath)
	}
	clean := path.Clean(importPath)
	if clean != importPath || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return result, fmt.Errorf("alias import %q cannot contain traversal or empty segments", importPath)
	}

	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return result, err
	}
	dir, err := filepath.Abs(filepath.Dir(importerPath))
	if err != nil {
		return result, err
	}
	inside, err := filepath.Rel(root, dir)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return result, fmt.Errorf("alias importer %q is outside source root", importerPath)
	}

	for {
		candidate := filepath.Join(dir, filepath.FromSlash(clean))
		result.Candidates = append(result.Candidates, candidate)
		info, statErr := os.Stat(candidate)
		if statErr == nil {
			if info.Mode().IsRegular() {
				real, evalErr := filepath.EvalSymlinks(candidate)
				if evalErr != nil {
					return result, evalErr
				}
				rel, relErr := filepath.Rel(root, real)
				if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return result, fmt.Errorf("alias import %q resolves outside source root", importPath)
				}
				result.ResolvedPath = candidate
				return result, nil
			}
			return result, fmt.Errorf("alias import %q resolves to a directory", importPath)
		}
		if !os.IsNotExist(statErr) {
			return result, statErr
		}
		if dir == root {
			break
		}
		dir = filepath.Dir(dir)
	}
	return result, fmt.Errorf("alias module %q was not found in the importer scope or its ancestors", importPath)
}

// AliasRoot returns the source-tree boundary used by @ imports. Pages and
// WebSocket/Channel modules use identical ancestral lookup rules but never
// cross from one tree into the other.
func AliasRoot(projectRoot, importerPath string) (string, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	importer, err := filepath.Abs(importerPath)
	if err != nil {
		return "", err
	}
	for _, tree := range []string{"pages", "websocket"} {
		treeRoot := filepath.Join(root, tree)
		rel, relErr := filepath.Rel(treeRoot, importer)
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return treeRoot, nil
		}
	}
	return "", fmt.Errorf("alias importer %q is outside pages and websocket", importerPath)
}
