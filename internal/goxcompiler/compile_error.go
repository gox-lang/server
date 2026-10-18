package gox

import (
	"path/filepath"
	"strings"

	"github.com/gox-lang/server/internal/goxerrors"
)

func newCompileErrorFromCore(core *goxerrors.GoxError) *goxerrors.GoxError {
	if core == nil {
		core = goxerrors.New(goxerrors.TypeCompiler, "compile", "", nil)
	}
	return core
}

func newCompilerError(kind string, message string) *goxerrors.GoxError {
	core := goxerrors.New(goxerrors.TypeCompiler, "compile", message, nil)
	core.Kind = kind
	return newCompileErrorFromCore(core)
}

func newCompilerErrorWithCause(kind string, message string, cause error) *goxerrors.GoxError {
	core := goxerrors.New(goxerrors.TypeCompiler, "compile", message, cause)
	core.Kind = kind
	return newCompileErrorFromCore(core)
}

func newCompilerErrorWithHint(kind string, message string, hint string) *goxerrors.GoxError {
	err := newCompilerError(kind, message)
	err.Hint = hint
	return err
}

func newTemplateCompileError(kind string, file string, line int, column int, message string) *goxerrors.GoxError {
	core := goxerrors.New(goxerrors.TypeTemplate, "compile", message, nil)
	core.Kind = kind
	core.File = file
	core.Line = line
	core.Column = column
	return newCompileErrorFromCore(core)
}

func firstCompileNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizeCompileErrorPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}

	slash := filepath.ToSlash(path)
	if strings.HasPrefix(slash, "./") {
		slash = strings.TrimPrefix(slash, "./")
	}

	if idx := strings.Index(slash, "/pages/"); idx >= 0 {
		return slash[idx+1:]
	}
	if strings.HasPrefix(slash, "pages/") {
		return slash
	}

	return slash
}
