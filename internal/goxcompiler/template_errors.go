package gox

import (
	"fmt"

	"github.com/gox-lang/server/internal/goxerrors"
)

func newTemplateError(kind string, pos TemplatePosition, message string) *goxerrors.GoxError {
	return newTemplateCompileError(kind, pos.File, pos.Line, pos.Column, message)
}

func newTemplateErrorf(kind string, pos TemplatePosition, format string, args ...any) *goxerrors.GoxError {
	return newTemplateError(kind, pos, fmt.Sprintf(format, args...))
}

func newTemplateErrorWithHint(kind string, pos TemplatePosition, message string, hint string) *goxerrors.GoxError {
	err := newTemplateError(kind, pos, message)
	if err == nil {
		return err
	}
	err.Hint = hint
	return err
}

func newTemplateErrorfWithHint(kind string, pos TemplatePosition, hint string, format string, args ...any) *goxerrors.GoxError {
	return newTemplateErrorWithHint(kind, pos, fmt.Sprintf(format, args...), hint)
}

func templateNodePosition(node TemplateNode) TemplatePosition {
	switch n := node.(type) {
	case *FragmentNode:
		return n.Pos
	case *ElementNode:
		return n.Pos
	case *TextNode:
		return n.Pos
	case *ExprNode:
		return n.Pos
	case *DirectiveNode:
		return n.Pos
	case *ComponentNode:
		return n.Pos
	case *ClientNode:
		return n.Pos
	case *CommentNode:
		return n.Pos
	default:
		return TemplatePosition{}
	}
}
