package lsp

import (
	"sort"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Only        []string     `json:"only,omitempty"`
}

type CodeAction struct {
	Title       string         `json:"title"`
	Kind        string         `json:"kind,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
	Edit        *WorkspaceEdit `json:"edit,omitempty"`
	IsPreferred bool           `json:"isPreferred,omitempty"`
}

type organizedImport struct {
	Alias string
	Path  string
	Keep  bool
}

func (s *Server) codeActions(document Document, params CodeActionParams) []CodeAction {
	var actions []CodeAction
	allowQuickFix := codeActionKindAllowed(params.Context.Only, "quickfix")
	allowOrganize := codeActionKindAllowed(params.Context.Only, "source.organizeImports")
	if allowQuickFix {
		var unusedDiagnostics []Diagnostic
		for _, diagnostic := range params.Context.Diagnostics {
			if diagnostic.Code == "unused-import" {
				unusedDiagnostics = append(unusedDiagnostics, diagnostic)
			}
			if diagnostic.Code == "undefined-identifier" {
				actions = append(actions, s.missingImportCodeActions(document, diagnostic)...)
				actions = append(actions, s.symbolQualificationCodeActions(document, diagnostic)...)
			}
			if diagnostic.Code == "undefined-selector" {
				actions = append(actions, s.selectorQualificationCodeActions(document, diagnostic)...)
			}
			if diagnostic.Code == "undefined-component" {
				actions = append(actions, s.componentQualificationCodeActions(document, diagnostic)...)
			}
		}
		if len(unusedDiagnostics) > 0 {
			if edit, ok := s.organizeImportsEdit(document, true); ok {
				actions = append(actions, CodeAction{
					Title: "Remove unused imports", Kind: "quickfix", Diagnostics: unusedDiagnostics,
					Edit: &WorkspaceEdit{Changes: map[string][]CompletionTextEdit{document.URI: {edit}}}, IsPreferred: true,
				})
			}
		}
	}
	if allowOrganize {
		if edit, ok := s.organizeImportsEdit(document, true); ok {
			actions = append(actions, CodeAction{
				Title: "Organize Imports", Kind: "source.organizeImports",
				Edit: &WorkspaceEdit{Changes: map[string][]CompletionTextEdit{document.URI: {edit}}},
			})
		}
	}
	return actions
}

func codeActionKindAllowed(only []string, kind string) bool {
	if len(only) == 0 {
		return true
	}
	for _, requested := range only {
		if requested == kind || strings.HasPrefix(kind, requested+".") || strings.HasPrefix(requested, kind+".") {
			return true
		}
	}
	return false
}

func (s *Server) missingImportCodeActions(document Document, diagnostic Diagnostic) []CodeAction {
	offset, err := document.Offset(diagnostic.Range.Start)
	if err != nil {
		return nil
	}
	alias, _, err := identifierAtPosition(document, diagnostic.Range.Start)
	if err != nil || alias == "" {
		return nil
	}
	var actions []CodeAction
	for _, module := range s.autoImportGoxModules(document, offset) {
		if module.Alias != alias {
			continue
		}
		actions = append(actions, importCodeAction(document, diagnostic, module.Alias, module.ImportPath, module.Edit))
	}
	for _, candidate := range s.autoImportGoPackages(document, offset, alias) {
		actions = append(actions, importCodeAction(document, diagnostic, candidate.Alias, candidate.ImportPath, candidate.Edit))
	}
	return actions
}

func importCodeAction(document Document, diagnostic Diagnostic, alias, path string, edit CompletionTextEdit) CodeAction {
	return CodeAction{
		Title: "Import " + alias + " from " + path, Kind: "quickfix", Diagnostics: []Diagnostic{diagnostic},
		Edit: &WorkspaceEdit{Changes: map[string][]CompletionTextEdit{document.URI: {edit}}}, IsPreferred: true,
	}
}

func (s *Server) organizeImportsEdit(document Document, removeUnused bool) (CompletionTextEdit, bool) {
	module, err := s.analysisModule(document)
	if err != nil {
		return CompletionTextEdit{}, false
	}
	ranges := goxImportSourceRanges(module.GoxImports, module.GoImports)
	if len(ranges) == 0 {
		return CompletionTextEdit{}, false
	}
	imports := make([]organizedImport, 0, len(ranges))
	for _, declaration := range module.GoxImports {
		alias := declaration.Name
		if alias == "" {
			alias, _, _ = s.inferredGoxImportAlias(document, declaration)
		}
		if alias == "" {
			alias = pathImportDiagnosticAlias(declaration.Path)
		}
		imports = append(imports, organizedImport{Alias: alias, Path: declaration.Path, Keep: importShouldRemain(document.Text, alias, removeUnused)})
	}
	root := s.projectRoot(document.Path)
	for _, declaration := range module.GoImports {
		alias := declaration.Alias
		if alias == "" {
			if packageInfo, loadErr := s.goPackages.Load(root, declaration.Path); loadErr == nil {
				alias = packageInfo.Name
			}
		}
		if alias == "" {
			alias = pathImportDiagnosticAlias(declaration.Path)
		}
		imports = append(imports, organizedImport{Alias: alias, Path: declaration.Path, Keep: importShouldRemain(document.Text, alias, removeUnused)})
	}
	seen := make(map[string]bool)
	kept := imports[:0]
	for _, declaration := range imports {
		if !declaration.Keep || seen[declaration.Path] {
			continue
		}
		seen[declaration.Path] = true
		kept = append(kept, declaration)
	}
	sort.SliceStable(kept, func(left, right int) bool {
		if kept[left].Path != kept[right].Path {
			return kept[left].Path < kept[right].Path
		}
		return kept[left].Alias < kept[right].Alias
	})
	startOffset, endOffset, ok := importDeclarationsSpan(document.Text, ranges)
	if !ok {
		return CompletionTextEdit{}, false
	}
	newText := organizedImportText(kept)
	if document.Text[startOffset:endOffset] == newText {
		return CompletionTextEdit{}, false
	}
	start, startErr := document.Position(startOffset)
	end, endErr := document.Position(endOffset)
	if startErr != nil || endErr != nil {
		return CompletionTextEdit{}, false
	}
	return CompletionTextEdit{Range: Range{Start: start, End: end}, NewText: newText}, true
}

func importShouldRemain(source, alias string, removeUnused bool) bool {
	return !removeUnused || alias == "_" || alias == "." || alias == "" || importAliasUsed(source, alias)
}

func organizedImportText(imports []organizedImport) string {
	if len(imports) == 0 {
		return ""
	}
	if len(imports) == 1 {
		return "import " + imports[0].Alias + " \"" + imports[0].Path + "\""
	}
	var builder strings.Builder
	builder.WriteString("import (\n")
	for _, declaration := range imports {
		builder.WriteString("\t")
		builder.WriteString(declaration.Alias)
		builder.WriteString(" \"")
		builder.WriteString(declaration.Path)
		builder.WriteString("\"\n")
	}
	builder.WriteString(")")
	return builder.String()
}

func importDeclarationsSpan(source string, ranges []goxmap.Range) (int, int, bool) {
	first := ranges[0]
	last := ranges[0]
	for _, declaration := range ranges[1:] {
		if declaration.Start < first.Start {
			first = declaration
		}
		if declaration.End > last.End {
			last = declaration
		}
	}
	firstKeyword := strings.LastIndex(source[:first.Start], "import")
	lastKeyword := strings.LastIndex(source[:last.Start], "import")
	if firstKeyword < 0 || lastKeyword < 0 {
		return 0, 0, false
	}
	start := sourceLineStart(source, firstKeyword)
	end := sourceLineEnd(source, last.End)
	if opening := strings.IndexByte(source[lastKeyword:last.Start], '('); opening >= 0 {
		if closing := strings.IndexByte(source[last.End:], ')'); closing >= 0 {
			end = sourceLineEnd(source, last.End+closing+1)
		}
	}
	return start, end, start <= end
}
