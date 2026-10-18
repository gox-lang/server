package lsp

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type importPathCompletionContext struct {
	prefix        string
	start         int
	end           int
	specStart     int
	specEnd       int
	explicitAlias bool
	unquoted      bool
}

type importPathCandidate struct {
	Path   string
	Alias  string
	Detail string
}

func (s *Server) importPathCompletions(document Document, offset int, cursor cursorContext) ([]CompletionItem, bool) {
	if cursor.Kind != cursorContextImportPath || !cursor.HasImport {
		return nil, false
	}
	context := cursor.ImportPath
	start, startErr := document.Position(context.start)
	end, endErr := document.Position(context.end)
	if startErr != nil || endErr != nil {
		return []CompletionItem{}, true
	}
	rangeValue := Range{Start: start, End: end}
	specStart, specStartErr := document.Position(context.specStart)
	specEnd, specEndErr := document.Position(context.specEnd)
	if specStartErr != nil || specEndErr != nil {
		return []CompletionItem{}, true
	}
	specificationRange := Range{Start: specStart, End: specEnd}
	items := s.goxImportPathCompletionItems(document, context, rangeValue, specificationRange)
	items = append(items, s.goImportPathCompletionItems(document, context, rangeValue, specificationRange)...)
	return items, true
}

func rawImportPathCompletionContextAt(source string, offset int) (importPathCompletionContext, bool) {
	if offset < 0 || offset > len(source) {
		return importPathCompletionContext{}, false
	}
	lineStart := sourceLineStart(source, offset)
	lineBefore := source[lineStart:offset]
	if strings.Count(lineBefore, `"`)%2 == 0 {
		return importPathCompletionContext{}, false
	}
	quote := strings.LastIndexByte(lineBefore, '"')
	if quote < 0 {
		return importPathCompletionContext{}, false
	}
	start := lineStart + quote + 1
	before := strings.TrimSpace(source[lineStart : start-1])
	if !importSpecificationPrefix(source, lineStart, before) {
		return importPathCompletionContext{}, false
	}
	end := offset
	closed := false
	if closing := strings.IndexByte(source[offset:], '"'); closing >= 0 {
		end = offset + closing
		closed = true
	}
	explicitAlias := importSpecificationHasExplicitAlias(before)
	specStart, specEnd := start, end
	if !explicitAlias {
		specStart = start - 1
		if closed {
			specEnd++
		}
	}
	return importPathCompletionContext{
		prefix: source[start:offset], start: start, end: end,
		specStart: specStart, specEnd: specEnd, explicitAlias: explicitAlias,
	}, true
}

func importSpecificationHasExplicitAlias(before string) bool {
	fields := strings.Fields(before)
	if len(fields) == 0 {
		return false
	}
	if fields[0] == "import" {
		return len(fields) == 2
	}
	return len(fields) == 1 && !strings.ContainsAny(fields[0], "(){}")
}

func rawBareImportPathCompletionContextAt(source string, offset int) (importPathCompletionContext, bool) {
	if offset < 0 || offset > len(source) {
		return importPathCompletionContext{}, false
	}
	lineStart := sourceLineStart(source, offset)
	start := offset
	for start > lineStart {
		value, size := utf8.DecodeLastRuneInString(source[lineStart:start])
		if !completionIdentifierRune(value) {
			break
		}
		start -= size
	}
	end := offset
	for end < len(source) {
		value, size := utf8.DecodeRuneInString(source[end:])
		if !completionIdentifierRune(value) {
			break
		}
		end += size
	}
	if start == end || strings.TrimSpace(source[lineStart:start]) != "" {
		return importPathCompletionContext{}, false
	}
	lineEnd := strings.IndexByte(source[offset:], '\n')
	if lineEnd >= 0 {
		lineEnd += offset
	} else {
		lineEnd = len(source)
	}
	if strings.TrimSpace(source[end:lineEnd]) != "" {
		return importPathCompletionContext{}, false
	}
	opening := strings.LastIndex(source[:lineStart], "import (")
	if opening < 0 || strings.LastIndexByte(source[opening:lineStart], ')') >= 0 {
		return importPathCompletionContext{}, false
	}
	return importPathCompletionContext{
		prefix: source[start:offset], start: start, end: end,
		specStart: start, specEnd: end, unquoted: true,
	}, true
}

func importSpecificationPrefix(source string, lineStart int, before string) bool {
	fields := strings.Fields(before)
	if len(fields) > 0 && fields[0] == "import" && len(fields) <= 2 {
		return true
	}
	if len(fields) > 1 || len(fields) == 1 && (fields[0] == "import" || strings.ContainsAny(fields[0], "(){}")) {
		return false
	}
	opening := strings.LastIndex(source[:lineStart], "import (")
	if opening < 0 {
		return false
	}
	return strings.LastIndexByte(source[opening:lineStart], ')') < 0
}

func (s *Server) goxImportPathCompletionItems(document Document, context importPathCompletionContext, replace Range, specification Range) []CompletionItem {
	paths := make(map[string]importPathCandidate)
	for _, candidate := range s.symbols.Documents() {
		if candidate.URI == document.URI || !strings.EqualFold(filepath.Ext(candidate.Path), ".gox") {
			continue
		}
		relative, err := filepath.Rel(filepath.Dir(document.Path), candidate.Path)
		if err != nil {
			continue
		}
		relative = filepath.ToSlash(relative)
		paths[relative] = importPathCandidate{Path: relative, Alias: s.goxModuleImportAlias(candidate.URI), Detail: "GOX module"}
	}
	for alias, target := range visibleGoxAliasImports(s.projectRoot(document.Path), document.Path) {
		paths[alias] = importPathCandidate{Path: alias, Alias: s.goxModuleImportAlias(FileURI(target)), Detail: "GOX ancestral alias → " + filepath.ToSlash(target)}
	}
	return importPathItems(paths, context, replace, specification, "1_", false)
}

func (s *Server) goxModuleImportAlias(uri string) string {
	for _, candidate := range s.symbols.DocumentSymbols(uri) {
		if candidate.Symbol.Kind == goxcompiler.SymbolModule && candidate.Symbol.Name != "" {
			return implicitGoxModuleAlias(candidate.Symbol.Name)
		}
	}
	return ""
}

func visibleGoxAliasImports(root, importer string) map[string]string {
	result := make(map[string]string)
	pages := filepath.Join(root, "pages")
	dir, err := filepath.Abs(filepath.Dir(importer))
	if err != nil || !pathInside(pages, dir) {
		return result
	}
	for {
		_ = filepath.WalkDir(dir, func(candidate string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || candidate == dir {
				return nil
			}
			relative, relErr := filepath.Rel(dir, candidate)
			if relErr != nil {
				return nil
			}
			parts := strings.Split(filepath.ToSlash(relative), "/")
			if !strings.HasPrefix(parts[0], "@") {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".gox") {
				return nil
			}
			alias := filepath.ToSlash(relative)
			if _, shadowed := result[alias]; !shadowed {
				result[alias] = candidate
			}
			return nil
		})
		if samePath(dir, pages) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir || !pathInside(pages, parent) {
			break
		}
		dir = parent
	}
	return result
}

func pathInside(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func (s *Server) goImportPathCompletionItems(document Document, context importPathCompletionContext, replace Range, specification Range) []CompletionItem {
	candidates, err := s.goPackages.Discover(s.goImportRoot(document.Path))
	if err != nil {
		return nil
	}
	paths := make(map[string]importPathCandidate, len(candidates))
	for _, candidate := range candidates {
		if candidate.Path != "" {
			paths[candidate.Path] = importPathCandidate{Path: candidate.Path, Alias: candidate.Name, Detail: "Go package " + candidate.Name}
		}
	}
	return importPathItems(paths, context, replace, specification, "2_", true)
}

// goImportRoot prefers the initialized workspace folder when it is a Go
// module. This keeps imports requested through the LSP protocol rooted at the
// same project VS Code opened, including while the active GOX document lives
// in a nested pages directory.
func (s *Server) goImportRoot(documentPath string) string {
	for index := len(s.workspaceRoots) - 1; index >= 0; index-- {
		root := s.workspaceRoots[index]
		if !pathInside(root, documentPath) && !samePath(root, filepath.Dir(documentPath)) {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, "go.mod")); err == nil && !info.IsDir() {
			return root
		}
	}
	return s.projectRoot(documentPath)
}

func importPathItems(paths map[string]importPathCandidate, context importPathCompletionContext, replace Range, specification Range, sortPrefix string, presentByPrefix bool) []CompletionItem {
	items := make([]CompletionItem, 0, len(paths))
	for _, candidate := range paths {
		if !importPathCandidateMatches(candidate, context.prefix, context.unquoted) {
			continue
		}
		edit := CompletionTextEdit{Range: replace, NewText: candidate.Path}
		var additionalEdits []CompletionTextEdit
		if candidate.Alias != "" && !context.explicitAlias {
			if context.unquoted {
				edit = CompletionTextEdit{Range: specification, NewText: candidate.Alias + ` "` + candidate.Path + `"`}
			} else {
				additionalEdits = []CompletionTextEdit{{
					Range:   Range{Start: specification.Start, End: specification.Start},
					NewText: candidate.Alias + " ",
				}}
			}
		}
		label, filterText := candidate.Path, path.Base(candidate.Path)
		if presentByPrefix {
			label, filterText = importPathCompletionPresentation(candidate, context)
		}
		items = append(items, CompletionItem{
			Label: label, Kind: 17, Detail: candidate.Detail, FilterText: filterText,
			SortText: sortPrefix + candidate.Path, TextEdit: &edit, AdditionalTextEdits: additionalEdits,
		})
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].Label < items[right].Label })
	return items
}

func importPathCompletionPresentation(candidate importPathCandidate, context importPathCompletionContext) (string, string) {
	prefix := strings.ToLower(context.prefix)
	packagePath := strings.ToLower(candidate.Path)
	if context.unquoted && candidate.Alias != "" && importPathCandidateAliasMatches(candidate, context.prefix) &&
		!strings.HasPrefix(packagePath, prefix) {
		return candidate.Alias, candidate.Alias
	}
	if strings.HasPrefix(packagePath, prefix) {
		return candidate.Path, candidate.Path
	}
	base := path.Base(candidate.Path)
	return base, base
}

func importPathCandidateMatches(candidate importPathCandidate, prefix string, includeAlias bool) bool {
	if prefix == "" {
		return true
	}
	prefix = strings.ToLower(prefix)
	if strings.HasPrefix(strings.ToLower(candidate.Path), prefix) ||
		strings.HasPrefix(strings.ToLower(path.Base(candidate.Path)), prefix) {
		return true
	}
	return includeAlias && importPathCandidateAliasMatches(candidate, prefix)
}

func importPathCandidateAliasMatches(candidate importPathCandidate, prefix string) bool {
	return prefix != "" && strings.HasPrefix(strings.ToLower(candidate.Alias), strings.ToLower(prefix))
}
