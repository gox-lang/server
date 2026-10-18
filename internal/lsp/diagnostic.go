package lsp

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"unicode/utf8"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxerrors"
	"github.com/gox-lang/server/internal/goxmap"
)

const diagnosticSeverityError = 1

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Code     string `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func Diagnostics(document Document) []Diagnostic {
	_, err := goxcompiler.ParseModule(document.Path, document.Text)
	if err == nil {
		return []Diagnostic{}
	}
	return []Diagnostic{diagnosticFromError(document, err)}
}

func (s *Server) diagnostics(document Document) []Diagnostic {
	snapshot := s.analysisSnapshot(document)
	if snapshot == nil {
		return []Diagnostic{}
	}
	// Diagnostics are immutable analysis products for one canonical URI/version.
	// Cache them on that snapshot and return a defensive copy to callers.
	snapshot.diagnosticsOnce.Do(func() {
		snapshot.diagnostics = s.computeDiagnostics(document, snapshot)
		if snapshot.diagnostics == nil {
			snapshot.diagnostics = []Diagnostic{}
		}
	})
	diagnostics := cloneDiagnostics(snapshot.diagnostics)
	// Client files can change without a new GOX document version. Keep these
	// filesystem-dependent diagnostics outside the immutable semantic cache.
	if snapshot.Err == nil {
		if clientDiagnostics := s.clientSourceDiagnostics(document, snapshot.Module); len(clientDiagnostics) > 0 {
			return normalizeDiagnostics(append(diagnostics, clientDiagnostics...))
		}
	}
	return diagnostics
}

func (s *Server) computeDiagnostics(document Document, snapshot *AnalysisSnapshot) []Diagnostic {
	if snapshot.Err != nil {
		return []Diagnostic{diagnosticFromError(document, snapshot.Err)}
	}
	if snapshot.Module != nil {
		if err := goxcompiler.ValidateLocalComponentSemantics(snapshot.Module, snapshot.Module.Templates); err != nil {
			return []Diagnostic{diagnosticFromError(document, err)}
		}
	}
	diagnostics := make([]Diagnostic, 0)
	if snapshot.semanticReady() {
		diagnostics = append(diagnostics, s.semanticNameDiagnostics(document, snapshot.Module)...)
	} else {
		unresolved := s.symbols.UnresolvedIdentifiers(document.URI)
		for _, identifier := range unresolved {
			start, startErr := document.Position(identifier.Source.Range.Start)
			end, endErr := document.Position(identifier.Source.Range.End)
			if startErr != nil || endErr != nil {
				continue
			}
			diagnostics = append(diagnostics, Diagnostic{
				Range:    Range{Start: start, End: end},
				Severity: diagnosticSeverityError,
				Code:     "undefined-identifier",
				Source:   "gox",
				Message:  "undefined: " + identifier.Name,
			})
		}
		diagnostics = append(diagnostics, s.templateUndefinedIdentifierDiagnostics(document, diagnostics)...)
	}
	diagnostics = append(diagnostics, s.blankIdentifierDiagnostics(document)...)
	diagnostics = append(diagnostics, s.importResolutionDiagnostics(document)...)
	diagnostics = append(diagnostics, s.unusedImportDiagnostics(document)...)
	diagnostics = append(diagnostics, s.assignmentStructureDiagnostics(document)...)
	diagnostics = append(diagnostics, s.unusedLocalDiagnostics(document)...)
	diagnostics = append(diagnostics, s.assignmentTypeDiagnostics(document)...)
	diagnostics = append(diagnostics, s.returnDiagnostics(document)...)
	diagnostics = append(diagnostics, s.conditionDiagnostics(document)...)
	diagnostics = append(diagnostics, s.callArgumentDiagnostics(document)...)
	diagnostics = append(diagnostics, s.templateReferenceDiagnostics(document)...)
	return normalizeDiagnostics(filterPassiveEmbeddedDiagnostics(document, snapshot.Module, diagnostics))
}

func cloneDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	result := make([]Diagnostic, len(diagnostics))
	copy(result, diagnostics)
	return result
}

func filterPassiveEmbeddedDiagnostics(document Document, module *goxcompiler.Module, diagnostics []Diagnostic) []Diagnostic {
	if len(diagnostics) == 0 {
		return diagnostics
	}
	filtered := make([]Diagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		offset, err := document.Offset(diagnostic.Range.Start)
		if err == nil && classifyCursorContext(document.Text, offset, module).passiveEmbedded() {
			continue
		}
		filtered = append(filtered, diagnostic)
	}
	return filtered
}

type diagnosticIdentity struct {
	startLine      int
	startCharacter int
	endLine        int
	endCharacter   int
	severity       int
	code           string
	source         string
	message        string
}

func normalizeDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	seen := make(map[diagnosticIdentity]bool, len(diagnostics))
	result := make([]Diagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		identity := diagnosticIdentity{
			startLine: diagnostic.Range.Start.Line, startCharacter: diagnostic.Range.Start.Character,
			endLine: diagnostic.Range.End.Line, endCharacter: diagnostic.Range.End.Character,
			severity: diagnostic.Severity, code: diagnostic.Code, source: diagnostic.Source, message: diagnostic.Message,
		}
		if seen[identity] {
			continue
		}
		seen[identity] = true
		result = append(result, diagnostic)
	}
	sort.SliceStable(result, func(left, right int) bool {
		leftRange, rightRange := result[left].Range, result[right].Range
		if leftRange.Start.Line != rightRange.Start.Line {
			return leftRange.Start.Line < rightRange.Start.Line
		}
		if leftRange.Start.Character != rightRange.Start.Character {
			return leftRange.Start.Character < rightRange.Start.Character
		}
		if leftRange.End.Line != rightRange.End.Line {
			return leftRange.End.Line < rightRange.End.Line
		}
		if leftRange.End.Character != rightRange.End.Character {
			return leftRange.End.Character < rightRange.End.Character
		}
		if result[left].Severity != result[right].Severity {
			return result[left].Severity < result[right].Severity
		}
		if result[left].Code != result[right].Code {
			return result[left].Code < result[right].Code
		}
		return result[left].Message < result[right].Message
	})
	return result
}

func (s *Server) templateUndefinedIdentifierDiagnostics(document Document, existing []Diagnostic) []Diagnostic {
	seen := make(map[int]bool)
	for _, diagnostic := range existing {
		if offset, err := document.Offset(diagnostic.Range.Start); err == nil {
			seen[offset] = true
		}
	}
	var diagnostics []Diagnostic
	for _, templateRange := range s.symbols.TemplateRanges(document.URI) {
		start := templateRange.Start
		end := templateRange.End
		if start < 0 {
			start = 0
		}
		if end > len(document.Text) {
			end = len(document.Text)
		}
		for cursor := start; cursor < end; {
			relative := strings.Index(document.Text[cursor:end], "{{")
			if relative < 0 {
				break
			}
			expressionStart := cursor + relative + 2
			closeRelative := strings.Index(document.Text[expressionStart:end], "}}")
			if closeRelative < 0 {
				break
			}
			expressionEnd := expressionStart + closeRelative
			diagnostics = append(diagnostics, s.undefinedExpressionDiagnostics(document, expressionStart, expressionEnd, seen)...)
			cursor = expressionEnd + 2
		}
	}
	return diagnostics
}

func (s *Server) undefinedExpressionDiagnostics(document Document, start, end int, seen map[int]bool) []Diagnostic {
	if start < 0 || end < start || end > len(document.Text) {
		return nil
	}
	fset := token.NewFileSet()
	expression, err := parser.ParseExprFrom(fset, document.Path+"#template", document.Text[start:end], 0)
	if err != nil {
		return nil
	}
	excluded := make(map[*ast.Ident]bool)
	ast.Inspect(expression, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SelectorExpr:
			excluded[value.Sel] = true
		case *ast.KeyValueExpr:
			if key, ok := value.Key.(*ast.Ident); ok {
				excluded[key] = true
			}
		}
		return true
	})
	file := fset.File(expression.Pos())
	if file == nil {
		return nil
	}
	var diagnostics []Diagnostic
	ast.Inspect(expression, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || excluded[ident] || ident.Name == "_" || typesUniverseIdentifier(ident.Name) {
			return true
		}
		offset := start + file.Offset(ident.Pos())
		if seen[offset] {
			return true
		}
		position, positionErr := document.Position(offset)
		if positionErr != nil || len(s.referenceTargetsAt(document, position)) > 0 {
			return true
		}
		endPosition, endErr := document.Position(offset + len(ident.Name))
		if endErr != nil {
			return true
		}
		seen[offset] = true
		diagnostics = append(diagnostics, Diagnostic{
			Range:    Range{Start: position, End: endPosition},
			Severity: diagnosticSeverityError,
			Code:     "undefined-identifier",
			Source:   "gox",
			Message:  "undefined: " + ident.Name,
		})
		return true
	})
	return diagnostics
}

func typesUniverseIdentifier(name string) bool {
	return token.Lookup(name).IsKeyword() || types.Universe.Lookup(name) != nil
}

func diagnosticFromError(document Document, err error) Diagnostic {
	diagnostic := Diagnostic{
		Range:    pointRange(Position{}),
		Severity: diagnosticSeverityError,
		Source:   "gox",
		Message:  err.Error(),
	}
	var goxErr *goxerrors.GoxError
	if !errors.As(err, &goxErr) {
		return diagnostic
	}
	if goxErr.Kind != "" {
		diagnostic.Code = goxErr.Kind
	}
	if goxErr.Message != "" {
		diagnostic.Message = goxErr.Message
	}
	if goxErr.Hint != "" {
		diagnostic.Message += " " + goxErr.Hint
	}
	if goxErr.Line <= 0 || goxErr.Column <= 0 {
		return diagnostic
	}
	index := goxmap.NewLineIndex(document.Text)
	offset, positionErr := index.ByteOffset(goxmap.BytePosition{Line: goxErr.Line - 1, Column: goxErr.Column - 1})
	if positionErr != nil {
		return diagnostic
	}
	start, positionErr := document.Position(offset)
	if positionErr != nil {
		return diagnostic
	}
	endOffset := offset
	if goxErr.Kind == "unsafe-goroutine" && strings.HasPrefix(document.Text[offset:], "go") {
		endOffset += len("go")
	} else if offset < len(document.Text) && document.Text[offset] != '\n' && document.Text[offset] != '\r' {
		_, size := utf8.DecodeRuneInString(document.Text[offset:])
		endOffset += size
	}
	end, positionErr := document.Position(endOffset)
	if positionErr != nil {
		end = start
	}
	diagnostic.Range = Range{Start: start, End: end}
	return diagnostic
}

func pointRange(position Position) Range {
	return Range{Start: position, End: position}
}
