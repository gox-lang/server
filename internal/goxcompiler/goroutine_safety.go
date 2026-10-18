package gox

import (
	"go/ast"
	"go/token"

	"github.com/gox-lang/server/internal/goroutinesafety"
	"github.com/gox-lang/server/internal/goxmap"
)

const diagnosticUnsafeGoroutine = "unsafe-goroutine"

func validateGoroutineSafety(path, source, generatedSource string, fset *token.FileSet, sourceMap *goxmap.Map, file *ast.File) error {
	diagnostics := goroutinesafety.AnalyzeFiles(fset, []*ast.File{file})
	if len(diagnostics) == 0 {
		return nil
	}

	diagnostic := diagnostics[0]
	err := newCompilerErrorWithHint(
		diagnosticUnsafeGoroutine,
		"goroutine has no panic recovery and may terminate GOX Server",
		"Register a deferred recover barrier before any other statement in the goroutine entry function.",
	)
	err.File = normalizeCompileErrorPath(path)

	if fset == nil || sourceMap == nil || !diagnostic.Pos.IsValid() {
		return err
	}
	position := fset.PositionFor(diagnostic.Pos, false)
	if position.Offset < 0 {
		return err
	}
	original, ok := mappedSyntaxPosition(position.Offset, generatedSource, sourceMap)
	if !ok {
		return err
	}
	err.File = normalizeCompileErrorPath(original.URI)
	line, column := lineColAt(source, original.Offset)
	err.Line = line
	err.Column = column
	return err
}
