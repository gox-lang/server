package serverruntime

import (
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

func applyGoxSourceMapDiagnostic(err *GoxError, route *GoxRouteRuntime) {
	if err == nil || route == nil || route.CompileResult == nil {
		return
	}
	sourceMapPath := strings.TrimSpace(route.CompileResult.SourceMapPath)
	if sourceMapPath == "" {
		return
	}
	var generatedDiagnostic *goxDiagnostic
	cause := ""
	if err.Cause != nil {
		cause = err.Cause.Error()
	}
	for _, diagnostic := range extractGoxDiagnostics(cause) {
		if diagnostic.Generated && diagnostic.Line > 0 && diagnostic.Column > 0 {
			copy := diagnostic
			generatedDiagnostic = &copy
			break
		}
	}
	if generatedDiagnostic == nil && strings.EqualFold(filepath.Ext(err.File), ".go") && err.Line > 0 {
		generatedDiagnostic = &goxDiagnostic{File: err.File, Line: err.Line, Column: err.Column, Generated: true}
	}
	if generatedDiagnostic == nil {
		return
	}
	generated, readErr := os.ReadFile(route.GeneratedPath)
	if readErr != nil {
		return
	}
	mapData, readErr := os.ReadFile(sourceMapPath)
	if readErr != nil {
		return
	}
	sourceMap, mapErr := goxmap.UnmarshalJSONDocument(mapData)
	if mapErr != nil {
		return
	}
	generatedOffset, found := generatedDiagnosticOffset(route.GeneratedPath, generated, *generatedDiagnostic)
	if !found {
		return
	}
	original, ok := sourceMap.OriginalPosition(generatedOffset)
	if !ok {
		return
	}
	originalSource, readErr := os.ReadFile(resolveGoxMappedSourcePath(route, original.URI))
	if readErr != nil {
		return
	}
	originalPosition, positionErr := goxmap.NewLineIndex(string(originalSource)).BytePosition(original.Offset)
	if positionErr != nil {
		return
	}
	err.File = normalizeGoxDiagnosticFile(err, original.URI)
	err.Line = originalPosition.Line + 1
	err.Column = originalPosition.Column + 1
	err.AddDetail("sourceMap.exact", strconv.FormatBool(original.Exact))
}

// Go reports positions after applying //line directives. Those directives may
// still name the temporary artifact after it has been moved to its build hash.
// Resolve the reported position through the actual file before using byte maps.
func generatedDiagnosticOffset(path string, source []byte, diagnostic goxDiagnostic) (int, bool) {
	file := token.NewFileSet().AddFile(path, -1, len(source))
	var scan scanner.Scanner
	scan.Init(file, source, nil, scanner.ScanComments)
	for {
		_, tok, _ := scan.Scan()
		if tok == token.EOF {
			break
		}
	}
	for line := 1; line <= file.LineCount(); line++ {
		start := file.LineStart(line)
		position := file.PositionFor(start, true)
		diagnosticPath := diagnostic.File
		if !filepath.IsAbs(diagnosticPath) {
			diagnosticPath = filepath.Join(filepath.Dir(path), diagnosticPath)
		}
		projectPath := diagnostic.File
		if !filepath.IsAbs(projectPath) {
			projectPath = filepath.Join(projectRootFromGeneratedPath(path), projectPath)
		}
		if position.Line != diagnostic.Line || (!sameGoxDiagnosticPath(position.Filename, diagnostic.File) && !sameGoxDiagnosticPath(position.Filename, diagnosticPath) && !sameGoxDiagnosticPath(position.Filename, projectPath)) {
			continue
		}
		offset := file.Offset(start) + diagnostic.Column - position.Column
		end := len(source)
		if line < file.LineCount() {
			end = file.Offset(file.LineStart(line + 1))
		}
		// runtime.Caller/FlowControl provide a line but no column. Resolve to
		// the first token of that physical line, not its indentation.
		if diagnostic.Column <= 0 {
			offset = file.Offset(start)
			for offset < end && (source[offset] == ' ' || source[offset] == '\t') {
				offset++
			}
		}
		if position.Column > 0 && offset >= file.Offset(start) && offset < end {
			return offset, true
		}
	}
	return 0, false
}

func sameGoxDiagnosticPath(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	leftClean := filepath.Clean(left)
	rightClean := filepath.Clean(right)
	if leftClean == rightClean {
		return true
	}
	leftAbs, leftErr := filepath.Abs(leftClean)
	rightAbs, rightErr := filepath.Abs(rightClean)
	return leftErr == nil && rightErr == nil && leftAbs == rightAbs
}

func resolveGoxMappedSourcePath(route *GoxRouteRuntime, uri string) string {
	if filepath.IsAbs(uri) {
		return filepath.Clean(uri)
	}
	root := projectRootFromGeneratedPath(route.GeneratedPath)
	if root == "" {
		return filepath.Clean(uri)
	}
	return filepath.Clean(filepath.Join(root, filepath.FromSlash(uri)))
}
