package gox

import "github.com/gox-lang/server/internal/goxmap"

type TemplateTokenKind string

const (
	TokenEOF TemplateTokenKind = "EOF"

	TokenText    TemplateTokenKind = "TEXT"
	TokenComment TemplateTokenKind = "COMMENT"

	TokenOpenTagStart      TemplateTokenKind = "OPEN_TAG_START"      // <
	TokenCloseTagStart     TemplateTokenKind = "CLOSE_TAG_START"     // </
	TokenDirectiveTagStart TemplateTokenKind = "DIRECTIVE_TAG_START" // <:
	TokenComponentTagStart TemplateTokenKind = "COMPONENT_TAG_START" // <&
	TokenClientTagStart    TemplateTokenKind = "CLIENT_TAG_START"    // <@

	TokenTagEnd       TemplateTokenKind = "TAG_END"        // >
	TokenSelfCloseEnd TemplateTokenKind = "SELF_CLOSE_END" // />

	TokenIdent TemplateTokenKind = "IDENT"
	TokenEqual TemplateTokenKind = "EQUAL"

	TokenString TemplateTokenKind = "STRING"
	TokenExpr   TemplateTokenKind = "EXPR" // { ... }
)

type TemplateExprMode string

const (
	ExprEscaped TemplateExprMode = "escaped"
	ExprRaw     TemplateExprMode = "raw"
)

type TemplateToken struct {
	Kind TemplateTokenKind
	Lit  string

	ExprMode TemplateExprMode
	Source   goxmap.SourceRange
	GoSource goxmap.SourceRange

	Line int
	Col  int
}
