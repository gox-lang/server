package gox

import (
	"fmt"
	"go/ast"
	"go/parser"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

type TemplateParser struct {
	tokens []TemplateToken
	pos    int
	file   string
	source goxmap.SourceRange
}

func ParseTemplate(src string) (*Template, error) {
	return ParseTemplateAt(src, "", 1, 1)
}

func ParseTemplateAt(src string, file string, line int, column int) (*Template, error) {
	return ParseTemplateAtOffset(src, file, line, column, 0)
}

func ParseTemplateAtOffset(src string, file string, line int, column int, sourceOffset int) (*Template, error) {
	lexer := NewTemplateLexerAtOffset(src, file, line, column, sourceOffset)

	tokens, err := lexer.LexAll()
	if err != nil {
		return nil, err
	}

	p := &TemplateParser{
		tokens: tokens,
		file:   file,
		source: goxmap.SourceRange{URI: file, Range: goxmap.Range{Start: sourceOffset, End: sourceOffset + len(src)}},
	}

	return p.Parse()
}

func (p *TemplateParser) Parse() (*Template, error) {
	var children []TemplateNode

	for {
		p.skipTextWhitespace()

		if p.peek().Kind == TokenEOF {
			break
		}

		node, err := p.parseNode()
		if err != nil {
			return nil, err
		}

		children = append(children, node)
	}

	switch len(children) {
	case 0:
		return nil, p.errorf("empty template")
	case 1:
		return &Template{Root: children[0], Source: p.source}, nil
	default:
		return &Template{Root: &FragmentNode{Children: children}, Source: p.source}, nil
	}
}

func (p *TemplateParser) ParseRootOnly() (TemplateNode, error) {
	p.skipTextWhitespace()

	if p.peek().Kind == TokenEOF {
		return nil, p.errorf("empty template")
	}

	root, err := p.parseNode()
	if err != nil {
		return nil, err
	}

	return root, nil
}

// =========================
// NODE PARSER
// =========================

func (p *TemplateParser) parseNode() (TemplateNode, error) {
	switch p.peek().Kind {
	case TokenOpenTagStart:
		return p.parseElement()

	case TokenDirectiveTagStart:
		return p.parseDirective()

	case TokenComponentTagStart:
		return p.parseComponent()

	case TokenClientTagStart:
		return p.parseClient()

	case TokenText:
		tok := p.next()
		return &TextNode{Pos: p.position(tok), Value: tok.Lit}, nil

	case TokenExpr:
		tok := p.next()

		expr, err := parseTemplateGoExpr(tok.Lit)
		if err != nil {
			return nil, p.errorAt(tok, "invalid Go expression %q: %v", tok.Lit, err)
		}

		return &ExprNode{
			Pos:        p.position(tok),
			Source:     tok.Source,
			ExprSource: tok.GoSource,
			Expr:       expr,
			Raw:        tok.Lit,
			Mode:       tok.ExprMode,
		}, nil

	case TokenComment:
		tok := p.next()
		return &CommentNode{Pos: p.position(tok), Value: tok.Lit}, nil
	}

	return nil, p.errorf("unexpected token %s", p.peek().Kind)
}

// =========================
// ELEMENT
// =========================

func (p *TemplateParser) parseElement() (*ElementNode, error) {
	p.expect(TokenOpenTagStart)

	nameTok, err := p.expect(TokenIdent)
	if err != nil {
		return nil, err
	}

	node := &ElementNode{
		Pos: p.position(nameTok),
		Tag: nameTok.Lit,
	}

	attrs, selfClosed, err := p.parseAttrsAndTagEnd()
	if err != nil {
		return nil, err
	}

	if err := validateAdaptAttrs(attrs, false); err != nil {
		return nil, err
	}
	node.Attrs = attrs
	node.SelfClosed = selfClosed || isHTMLVoidTag(nameTok.Lit)

	if node.SelfClosed {
		return node, nil
	}

	children, err := p.parseChildrenUntilClose(nameTok.Lit)
	if err != nil {
		return nil, err
	}

	node.Children = children

	return node, nil
}

// =========================
// DIRECTIVE
// =========================

func (p *TemplateParser) parseDirective() (*DirectiveNode, error) {
	p.expect(TokenDirectiveTagStart)

	nameTok, err := p.expect(TokenIdent)
	if err != nil {
		return nil, err
	}

	node := &DirectiveNode{
		Pos:  p.position(nameTok),
		Name: nameTok.Lit,
	}

	attrs, selfClosed, err := p.parseAttrsAndTagEnd()
	if err != nil {
		return nil, err
	}

	if err := validateAdaptAttrs(attrs, false); err != nil {
		return nil, err
	}
	node.Attrs = attrs
	node.SelfClosed = selfClosed

	if selfClosed {
		return node, nil
	}

	children, err := p.parseChildrenUntilDirectiveClose(nameTok.Lit)
	if err != nil {
		return nil, err
	}

	node.Children = children

	return node, nil
}

// =========================
// COMPONENT
// =========================

func (p *TemplateParser) parseComponent() (*ComponentNode, error) {
	p.expect(TokenComponentTagStart)

	nameTok, err := p.expect(TokenIdent)
	if err != nil {
		return nil, err
	}

	node := &ComponentNode{
		Pos:  p.position(nameTok),
		Name: nameTok.Lit,
	}

	attrs, selfClosed, err := p.parseAttrsAndTagEnd()
	if err != nil {
		return nil, err
	}

	if err := validateAdaptAttrs(attrs, false); err != nil {
		return nil, err
	}
	node.Attrs = attrs
	node.SelfClosed = selfClosed

	if selfClosed {
		return node, nil
	}

	children, err := p.parseChildrenUntilComponentClose(nameTok.Lit)
	if err != nil {
		return nil, err
	}

	node.Children = children

	return node, nil
}

// =========================
// CLIENT COMPONENT
// =========================

func (p *TemplateParser) parseClient() (*ClientNode, error) {
	p.expect(TokenClientTagStart)

	nameTok, err := p.expect(TokenIdent)
	if err != nil {
		return nil, err
	}

	node := &ClientNode{
		Pos:  p.position(nameTok),
		Name: nameTok.Lit,
	}

	attrs, selfClosed, err := p.parseAttrsAndTagEnd()
	if err != nil {
		return nil, err
	}

	if err := validateAdaptAttrs(attrs, true); err != nil {
		return nil, err
	}
	node.Attrs = attrs
	node.SelfClosed = selfClosed

	if selfClosed {
		return node, nil
	}

	children, err := p.parseChildrenUntilClientClose(nameTok.Lit)
	if err != nil {
		return nil, err
	}

	node.Children = children

	return node, nil
}

// =========================
// CHILDREN
// =========================

func (p *TemplateParser) parseChildrenUntilClose(tag string) ([]TemplateNode, error) {
	var children []TemplateNode

	for {
		if p.peek().Kind == TokenEOF {
			return nil, p.unclosedTagError(tag)
		}

		if p.peek().Kind == TokenCloseTagStart {
			p.next()

			nameTok, err := p.expect(TokenIdent)
			if err != nil {
				return nil, err
			}

			if nameTok.Lit != tag {
				return nil, p.mismatchedClosingTagError(nameTok, tag, nameTok.Lit)
			}

			if _, err := p.expect(TokenTagEnd); err != nil {
				return nil, err
			}

			return children, nil
		}

		child, err := p.parseNode()
		if err != nil {
			return nil, err
		}

		children = append(children, child)
	}
}

func (p *TemplateParser) parseChildrenUntilDirectiveClose(name string) ([]TemplateNode, error) {
	var children []TemplateNode

	for {
		if p.peek().Kind == TokenEOF {
			return nil, p.unclosedDirectiveError(name)
		}

		if p.peek().Kind == TokenCloseTagStart {
			p.next()

			nameTok, err := p.expect(TokenIdent)
			if err != nil {
				return nil, err
			}

			if nameTok.Lit != ":"+name {
				return nil, p.mismatchedClosingDirectiveError(nameTok, name, nameTok.Lit)
			}

			if _, err := p.expect(TokenTagEnd); err != nil {
				return nil, err
			}

			return children, nil
		}

		child, err := p.parseNode()
		if err != nil {
			return nil, err
		}

		children = append(children, child)
	}
}

func (p *TemplateParser) parseChildrenUntilComponentClose(name string) ([]TemplateNode, error) {
	var children []TemplateNode

	for {
		if p.peek().Kind == TokenEOF {
			return nil, p.unclosedComponentError(name)
		}

		if p.peek().Kind == TokenCloseTagStart {
			p.next()

			nameTok, err := p.expect(TokenIdent)
			if err != nil {
				return nil, err
			}

			if nameTok.Lit != "&"+name {
				return nil, p.mismatchedClosingComponentError(nameTok, name, nameTok.Lit)
			}

			if _, err := p.expect(TokenTagEnd); err != nil {
				return nil, err
			}

			return children, nil
		}

		child, err := p.parseNode()
		if err != nil {
			return nil, err
		}

		children = append(children, child)
	}
}

func (p *TemplateParser) parseChildrenUntilClientClose(name string) ([]TemplateNode, error) {
	var children []TemplateNode

	for {
		if p.peek().Kind == TokenEOF {
			return nil, p.unclosedClientError(name)
		}

		if p.peek().Kind == TokenCloseTagStart {
			p.next()

			nameTok, err := p.expect(TokenIdent)
			if err != nil {
				return nil, err
			}

			if nameTok.Lit != "@"+name {
				return nil, p.mismatchedClosingClientError(nameTok, name, nameTok.Lit)
			}

			if _, err := p.expect(TokenTagEnd); err != nil {
				return nil, err
			}

			return children, nil
		}

		child, err := p.parseNode()
		if err != nil {
			return nil, err
		}

		children = append(children, child)
	}
}

// =========================
// ATTRIBUTES
// =========================

func (p *TemplateParser) parseAttrsAndTagEnd() ([]TemplateAttr, bool, error) {
	var attrs []TemplateAttr

	for {
		switch p.peek().Kind {
		case TokenTagEnd:
			p.next()
			return attrs, false, nil

		case TokenSelfCloseEnd:
			p.next()
			return attrs, true, nil

		case TokenIdent:
			attr, err := p.parseAttr()
			if err != nil {
				return nil, false, err
			}

			attrs = append(attrs, attr)

		default:
			return nil, false, p.errorf("expected attribute or tag end, got %s", p.peek().Kind)
		}
	}
}

func (p *TemplateParser) parseAttr() (TemplateAttr, error) {
	nameTok := p.next()

	attr := TemplateAttr{
		Pos:        p.position(nameTok),
		Source:     nameTok.Source,
		NameSource: nameTok.Source,
		Name:       nameTok.Lit,
		Kind:       AttrBoolean,
	}

	if isSpecialAttr(nameTok.Lit) {
		attr.Kind = AttrSpecial
	}

	if p.peek().Kind != TokenEqual {
		return attr, nil
	}

	p.next()

	switch p.peek().Kind {
	case TokenString:
		valTok := p.next()

		if attr.Kind != AttrSpecial {
			attr.Kind = AttrStatic
		}

		attr.Value = valTok.Lit
		attr.ValueSource = valTok.Source
		attr.Source.Range.End = valTok.Source.Range.End
		return attr, nil

	case TokenExpr:
		exprTok := p.next()

		expr, err := parseTemplateGoExpr(exprTok.Lit)
		if err != nil {
			// <:case is={1, 2, 3}> permite lista de expresiones.
			// Se valida luego en template_generator_switch.go.
			if nameTok.Lit != "is" {
				return TemplateAttr{}, p.errorAt(exprTok, "invalid Go expression %q: %v", exprTok.Lit, err)
			}
		}

		if attr.Kind != AttrSpecial {
			attr.Kind = AttrExpr
		}

		attr.Raw = exprTok.Lit
		attr.Expr = expr
		attr.ValueSource = exprTok.Source
		attr.ExprSource = exprTok.GoSource
		attr.Source.Range.End = exprTok.Source.Range.End
		attr.Mode = exprTok.ExprMode

		return attr, nil
	}

	return TemplateAttr{}, p.errorf("expected string or expression value for attribute %s", nameTok.Lit)
}

func isSpecialAttr(name string) bool {
	return len(name) > 0 && name[0] == ':'
}

// =========================
// GO EXPR
// =========================

func parseTemplateGoExpr(src string) (ast.Expr, error) {
	return parser.ParseExpr(src)
}

// alias pequeño para evitar repetir imports en explicación;
// si prefieres, cambia astExpr por ast.Expr e importa go/ast.
type astExpr = interface{}

// =========================
// PARSER HELPERS
// =========================

func (p *TemplateParser) position(tok TemplateToken) TemplatePosition {
	return TemplatePosition{
		File:   p.file,
		Line:   tok.Line,
		Column: tok.Col,
	}
}

func (p *TemplateParser) peek() TemplateToken {
	if p.pos >= len(p.tokens) {
		return TemplateToken{Kind: TokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *TemplateParser) next() TemplateToken {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *TemplateParser) expect(kind TemplateTokenKind) (TemplateToken, error) {
	tok := p.peek()

	if tok.Kind != kind {
		return TemplateToken{}, p.errorAt(
			tok,
			"expected %s, got %s",
			kind,
			tok.Kind,
		)
	}

	p.pos++
	return tok, nil
}

func (p *TemplateParser) skipTextWhitespace() {
	for p.peek().Kind == TokenText {
		if strings.TrimSpace(p.peek().Lit) != "" {
			return
		}
		p.next()
	}
}

func (p *TemplateParser) errorf(format string, args ...any) error {
	tok := p.peek()
	return p.errorAt(tok, format, args...)
}

func (p *TemplateParser) errorAt(tok TemplateToken, format string, args ...any) error {
	return newTemplateCompileError("template.parser", p.file, tok.Line, tok.Col, fmt.Sprintf(format, args...))
}

func (p *TemplateParser) mismatchedClosingTagError(tok TemplateToken, expected string, found string) error {
	err := newTemplateCompileError(
		"template.mismatched_closing_tag",
		p.file,
		tok.Line,
		tok.Col,
		fmt.Sprintf("expected closing tag </%s>, got </%s>", expected, found),
	)
	err.Expected = "</" + expected + ">"
	err.Found = "</" + found + ">"
	err.Hint = fmt.Sprintf("close <%s> before closing <%s>", expected, found)
	return err
}

func (p *TemplateParser) mismatchedClosingDirectiveError(tok TemplateToken, expected string, found string) error {
	err := newTemplateCompileError(
		"template.mismatched_closing_directive",
		p.file,
		tok.Line,
		tok.Col,
		fmt.Sprintf("expected closing directive </:%s>, got </%s>", expected, found),
	)
	err.Expected = "</:" + expected + ">"
	err.Found = "</" + found + ">"
	err.Hint = "close the current directive before closing another block"
	return err
}

func (p *TemplateParser) mismatchedClosingClientError(tok TemplateToken, expected string, found string) error {
	err := newTemplateCompileError(
		"template.mismatched_closing_client_component",
		p.file,
		tok.Line,
		tok.Col,
		fmt.Sprintf("expected closing client component </@%s>, got </%s>", expected, found),
	)
	err.Expected = "</@" + expected + ">"
	err.Found = "</" + found + ">"
	err.Hint = "close the current client component before closing another block"
	return err
}

func (p *TemplateParser) mismatchedClosingComponentError(tok TemplateToken, expected string, found string) error {
	err := newTemplateCompileError(
		"template.mismatched_closing_component",
		p.file,
		tok.Line,
		tok.Col,
		fmt.Sprintf("expected closing component </&%s>, got </%s>", expected, found),
	)
	err.Expected = "</&" + expected + ">"
	err.Found = "</" + found + ">"
	err.Hint = "close the current component before closing another block"
	return err
}

func (p *TemplateParser) unclosedTagError(tag string) error {
	tok := p.peek()
	err := newTemplateCompileError(
		"template.unclosed_tag",
		p.file,
		tok.Line,
		tok.Col,
		fmt.Sprintf("unclosed tag <%s>", tag),
	)
	err.Expected = "</" + tag + ">"
	err.Hint = fmt.Sprintf("add </%s> before the template ends", tag)
	return err
}

func (p *TemplateParser) unclosedDirectiveError(name string) error {
	tok := p.peek()
	err := newTemplateCompileError(
		"template.unclosed_directive",
		p.file,
		tok.Line,
		tok.Col,
		fmt.Sprintf("unclosed directive <:%s>", name),
	)
	err.Expected = "</:" + name + ">"
	err.Hint = fmt.Sprintf("add </:%s> before the template ends", name)
	return err
}

func (p *TemplateParser) unclosedClientError(name string) error {
	tok := p.peek()
	err := newTemplateCompileError(
		"template.unclosed_client_component",
		p.file,
		tok.Line,
		tok.Col,
		fmt.Sprintf("unclosed client component <@%s>", name),
	)
	err.Expected = "</@" + name + ">"
	err.Hint = fmt.Sprintf("add </@%s> before the template ends", name)
	return err
}

func (p *TemplateParser) unclosedComponentError(name string) error {
	tok := p.peek()
	err := newTemplateCompileError(
		"template.unclosed_component",
		p.file,
		tok.Line,
		tok.Col,
		fmt.Sprintf("unclosed component <&%s>", name),
	)
	err.Expected = "</&" + name + ">"
	err.Hint = fmt.Sprintf("add </&%s> before the template ends", name)
	return err
}
