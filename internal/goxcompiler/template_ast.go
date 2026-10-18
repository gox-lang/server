package gox

import (
	"go/ast"

	"github.com/gox-lang/server/internal/goxmap"
)

type Template struct {
	Root   TemplateNode
	Source goxmap.SourceRange
}

// TemplatePosition points back to the user-facing .gox source.
type TemplatePosition struct {
	File   string
	Line   int
	Column int
}

func (p TemplatePosition) IsValid() bool {
	return p.File != "" && p.Line > 0 && p.Column > 0
}

type TemplateNode interface {
	templateNode()
}

// =========================
// NODES
// =========================

type FragmentNode struct {
	Pos      TemplatePosition
	Children []TemplateNode
}

func (*FragmentNode) templateNode() {}

type ElementNode struct {
	Pos        TemplatePosition
	Tag        string
	Attrs      []TemplateAttr
	Children   []TemplateNode
	SelfClosed bool
}

func (*ElementNode) templateNode() {}

type TextNode struct {
	Pos   TemplatePosition
	Value string
}

func (*TextNode) templateNode() {}

type ExprNode struct {
	Pos        TemplatePosition
	Source     goxmap.SourceRange
	ExprSource goxmap.SourceRange
	Expr       ast.Expr
	Raw        string
	Mode       TemplateExprMode
}

func (*ExprNode) templateNode() {}

type DirectiveNode struct {
	Pos        TemplatePosition
	Name       string
	Attrs      []TemplateAttr
	Children   []TemplateNode
	SelfClosed bool
}

func (*DirectiveNode) templateNode() {}

type ComponentNode struct {
	Pos        TemplatePosition
	Name       string
	Attrs      []TemplateAttr
	Children   []TemplateNode
	SelfClosed bool
}

func (*ComponentNode) templateNode() {}

type ClientNode struct {
	Pos        TemplatePosition
	Name       string
	Attrs      []TemplateAttr
	Children   []TemplateNode
	SelfClosed bool
}

func (*ClientNode) templateNode() {}

type CommentNode struct {
	Pos   TemplatePosition
	Value string
}

func (*CommentNode) templateNode() {}

// =========================
// ATTRIBUTES
// =========================

type TemplateAttrKind string

const (
	AttrStatic  TemplateAttrKind = "static"
	AttrExpr    TemplateAttrKind = "expr"
	AttrBoolean TemplateAttrKind = "boolean"
	AttrSpecial TemplateAttrKind = "special"
)

type TemplateAttr struct {
	Pos        TemplatePosition
	Source     goxmap.SourceRange
	NameSource goxmap.SourceRange
	Name       string
	Kind       TemplateAttrKind

	Value       string
	ValueSource goxmap.SourceRange

	Expr       ast.Expr
	ExprSource goxmap.SourceRange
	Raw        string
	Mode       TemplateExprMode
}
