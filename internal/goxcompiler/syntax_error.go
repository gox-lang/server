package gox

import (
	"errors"
	"go/scanner"
	"go/token"

	"github.com/gox-lang/server/internal/goxmap"
)

func mapGoSyntaxError(err error, path, originalSource, generatedSource string, sourceMap *goxmap.Map) error {
	position, message, ok := firstGoSyntaxError(err)
	if !ok {
		return err
	}

	mappedPath := path
	line, column := 0, 0
	if original, found := mappedSyntaxPosition(position.Offset, generatedSource, sourceMap); found {
		mappedPath = original.URI
		if sourcePosition, positionErr := goxmap.NewLineIndex(originalSource).BytePosition(original.Offset); positionErr == nil {
			line = sourcePosition.Line + 1
			column = sourcePosition.Column + 1
		}
	}

	compileErr := newCompilerErrorWithHint("go-syntax-error", message, "Correct the GOX syntax at the reported location.")
	compileErr.File = normalizeCompileErrorPath(mappedPath)
	compileErr.Line = line
	compileErr.Column = column
	return compileErr
}

func firstGoSyntaxError(err error) (token.Position, string, bool) {
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		return list[0].Pos, list[0].Msg, true
	}
	var item *scanner.Error
	if errors.As(err, &item) && item != nil {
		return item.Pos, item.Msg, true
	}
	return token.Position{}, "", false
}

func mappedSyntaxPosition(offset int, generatedSource string, sourceMap *goxmap.Map) (goxmap.Position, bool) {
	if sourceMap == nil {
		return goxmap.Position{}, false
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(generatedSource) {
		offset = len(generatedSource)
	}
	if original, ok := sourceMap.OriginalPosition(offset); ok {
		return original, true
	}
	if offset > 0 {
		return sourceMap.OriginalPosition(offset - 1)
	}
	return goxmap.Position{}, false
}
