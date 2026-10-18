package lsp

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) clientSourceDefinitionLinks(document Document, position Position) []LocationLink {
	offset, err := document.Offset(position)
	if err != nil {
		return nil
	}
	module, err := s.analysisModule(document)
	if err != nil {
		return nil
	}
	for _, client := range module.Clients {
		start, end, ok := clientEntryValueRange(document.Text, client.EntryOffset)
		if !ok || offset < start || offset > end {
			continue
		}
		target, ok := s.resolveClientSourcePath(document, client)
		if !ok {
			return nil
		}
		originStart, startErr := document.Position(start)
		originEnd, endErr := document.Position(end)
		if startErr != nil || endErr != nil {
			return nil
		}
		zero := Range{Start: Position{}, End: Position{}}
		return []LocationLink{{
			OriginSelectionRange: Range{Start: originStart, End: originEnd},
			TargetURI:            FileURI(target), TargetRange: zero, TargetSelectionRange: zero,
		}}
	}
	return nil
}

func clientEntryValueRange(source string, offset int) (int, int, bool) {
	if offset < 0 || offset >= len(source) || source[offset] != '"' {
		return 0, 0, false
	}
	for end := offset + 1; end < len(source); end++ {
		if source[end] == '\\' {
			end++
			continue
		}
		if source[end] == '"' {
			return offset + 1, end, true
		}
	}
	return 0, 0, false
}

func (s *Server) resolveClientSourcePath(document Document, client goxcompiler.ClientDecl) (string, bool) {
	framework := strings.ToLower(strings.TrimSpace(string(client.Framework)))
	entry := strings.TrimSpace(client.Entry)
	if framework == "" || entry == "" || strings.Contains(entry, "\\") || filepath.IsAbs(entry) || strings.HasPrefix(entry, "/") {
		return "", false
	}
	clean := path.Clean(entry)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", false
	}
	clean = strings.TrimPrefix(clean, "client/")
	clean = strings.TrimPrefix(clean, framework+"/")
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	frameworkRoot := filepath.Join(s.projectRoot(document.Path), "client", framework)
	target := filepath.Join(frameworkRoot, filepath.FromSlash(clean))
	relative, err := filepath.Rel(frameworkRoot, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		return "", false
	}
	resolvedRoot, rootErr := filepath.EvalSymlinks(frameworkRoot)
	resolvedTarget, targetErr := filepath.EvalSymlinks(target)
	if rootErr == nil && targetErr == nil {
		resolvedRelative, relErr := filepath.Rel(resolvedRoot, resolvedTarget)
		if relErr != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
			return "", false
		}
	}
	return target, true
}
