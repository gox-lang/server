package gox

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/gox-lang/server/internal/goxmap"
)

type TemplateLexer struct {
	src        string
	file       string
	sourceBase int
	pos        int
	line       int
	col        int

	inTag bool
}

func NewTemplateLexer(src string) *TemplateLexer {
	return NewTemplateLexerAt(src, "", 1, 1)
}

func NewTemplateLexerAt(src string, file string, line int, col int) *TemplateLexer {
	return NewTemplateLexerAtOffset(src, file, line, col, 0)
}

func NewTemplateLexerAtOffset(src string, file string, line int, col int, sourceOffset int) *TemplateLexer {
	if line <= 0 {
		line = 1
	}
	if col <= 0 {
		col = 1
	}
	if sourceOffset < 0 {
		sourceOffset = 0
	}
	return &TemplateLexer{
		src:        src,
		file:       file,
		sourceBase: sourceOffset,
		line:       line,
		col:        col,
	}
}

func (l *TemplateLexer) LexAll() ([]TemplateToken, error) {
	var tokens []TemplateToken

	for {
		tok, err := l.NextToken()
		if err != nil {
			return nil, err
		}

		tokens = append(tokens, tok)

		if tok.Kind == TokenEOF {
			break
		}
	}

	return tokens, nil
}

func (l *TemplateLexer) NextToken() (TemplateToken, error) {
	if l.eof() {
		return l.token(TokenEOF, ""), nil
	}

	// line comment
	if l.peek() == '/' && l.peekN(1) == '/' {
		l.advance()
		l.advance()

		l.skipLineComment()

		return l.NextToken()
	}

	// block comment
	if l.peek() == '/' && l.peekN(1) == '*' {
		if err := l.skipBlockComment(); err != nil {
			return TemplateToken{}, err
		}

		return l.NextToken()
	}

	if l.inTag {
		l.skipSpaces()

		if l.eof() {
			return l.token(TokenEOF, ""), nil
		}

		if l.peek() == '/' && l.peekN(1) == '/' {
			l.advance()
			l.advance()
			l.skipLineComment()
			return l.NextToken()
		}

		if l.peek() == '/' && l.peekN(1) == '*' {
			if err := l.skipBlockComment(); err != nil {
				return TemplateToken{}, err
			}

			return l.NextToken()
		}

		switch {
		case l.match("/>"):
			tok := l.token(TokenSelfCloseEnd, "/>")
			l.advanceN(2)
			l.inTag = false
			return tok, nil

		case l.peek() == '>':
			tok := l.token(TokenTagEnd, ">")
			l.advance()
			l.inTag = false
			return tok, nil

		case l.peek() == '=':
			tok := l.token(TokenEqual, "=")
			l.advance()
			return tok, nil

		case l.peek() == '"' || l.peek() == '\'':
			return l.readString()

		case l.match("{{"):
			return l.readExpr()

		case isIdentStart(l.peek()) ||
			l.peek() == ':' ||
			l.peek() == '&' ||
			l.peek() == '-' ||
			l.peek() == '@':
			return l.readIdent()

		default:
			return TemplateToken{}, l.errorf("unexpected character inside tag: %q", l.peek())
		}
	}

	switch {
	case l.match("<!--"):
		return l.readComment()

	case l.looksLikeCloseTagStart():
		tok := l.token(TokenCloseTagStart, "</")
		l.advanceN(2)
		l.inTag = true
		return tok, nil

	case l.match("<:"):
		tok := l.token(TokenDirectiveTagStart, "<:")
		l.advanceN(2)
		l.inTag = true
		return tok, nil

	case l.match("<&"):
		tok := l.token(TokenComponentTagStart, "<&")
		l.advanceN(2)
		l.inTag = true
		return tok, nil

	case l.match("<@"):
		tok := l.token(TokenClientTagStart, "<@")
		l.advanceN(2)
		l.inTag = true
		return tok, nil

	case l.looksLikeOpenTagStart():
		tok := l.token(TokenOpenTagStart, "<")
		l.advance()
		l.inTag = true
		return tok, nil

	case l.match("{{"):
		return l.readExpr()

	default:
		return l.readText()
	}
}

// =========================
// READERS
// =========================

func (l *TemplateLexer) readText() (TemplateToken, error) {
	startPos := l.pos
	startLine := l.line
	startCol := l.col
	var escaped strings.Builder

	for !l.eof() {
		if l.peek() == '\\' {
			if escaped.Len() == 0 {
				escaped.WriteString(l.src[startPos:l.pos])
			}

			l.advance() // \

			if l.eof() {
				escaped.WriteByte('\\')
				break
			}

			escaped.WriteByte(l.peek())
			l.advance()
			continue
		}

		if l.match("{{") || l.looksLikeHTMLStart() {
			break
		}

		if l.peek() == '/' && (l.peekN(1) == '/' || l.peekN(1) == '*') {
			break
		}

		if escaped.Len() > 0 {
			escaped.WriteByte(l.peek())
		}
		l.advance()
	}

	lit := l.src[startPos:l.pos]
	if escaped.Len() > 0 {
		lit = escaped.String()
	}

	return TemplateToken{
		Kind:   TokenText,
		Lit:    lit,
		Source: l.sourceRange(startPos, l.pos),
		Line:   startLine,
		Col:    startCol,
	}, nil
}

func (l *TemplateLexer) readComment() (TemplateToken, error) {
	startPos := l.pos
	startLine := l.line
	startCol := l.col

	l.advanceN(len("<!--"))
	start := l.pos

	for !l.eof() && !l.match("-->") {
		l.advance()
	}

	if l.eof() {
		return TemplateToken{}, l.errorAt(startLine, startCol, "unclosed HTML comment")
	}

	lit := l.src[start:l.pos]
	l.advanceN(len("-->"))

	return TemplateToken{
		Kind:   TokenComment,
		Lit:    lit,
		Source: l.sourceRange(startPos, l.pos),
		Line:   startLine,
		Col:    startCol,
	}, nil
}

func (l *TemplateLexer) readIdent() (TemplateToken, error) {
	startPos := l.pos
	startLine := l.line
	startCol := l.col

	for !l.eof() {
		ch := l.peek()

		if isIdentPart(ch) || ch == ':' || ch == '&' || ch == '-' || ch == '@' {
			l.advance()
			continue
		}

		break
	}

	return TemplateToken{
		Kind:   TokenIdent,
		Lit:    l.src[startPos:l.pos],
		Source: l.sourceRange(startPos, l.pos),
		Line:   startLine,
		Col:    startCol,
	}, nil
}

func (l *TemplateLexer) readString() (TemplateToken, error) {
	startPos := l.pos
	quote := l.peek()
	startLine := l.line
	startCol := l.col

	l.advance()
	start := l.pos

	for !l.eof() {
		ch := l.peek()

		if ch == quote {
			lit := l.src[start:l.pos]
			l.advance()

			return TemplateToken{
				Kind:   TokenString,
				Lit:    lit,
				Source: l.sourceRange(startPos, l.pos),
				Line:   startLine,
				Col:    startCol,
			}, nil
		}

		if ch == '<' || ch == '>' {
			return TemplateToken{}, l.errorf("invalid character %q inside quoted attribute", ch)
		}

		l.advance()
	}

	return TemplateToken{}, l.errorAt(startLine, startCol, "unclosed quoted attribute")
}

func (l *TemplateLexer) readExpr() (TemplateToken, error) {
	tokenStart := l.pos
	startLine := l.line
	startCol := l.col

	if !l.match("{{") {
		return TemplateToken{}, l.errorAt(startLine, startCol, "template expression must start with {{")
	}

	l.advanceN(2) // {{

	mode := ExprEscaped

	if !l.eof() && l.peek() == '<' {
		mode = ExprRaw
		l.advance() // <
	}

	start := l.pos

	for !l.eof() {
		ch := l.peek()

		if ch == '\n' || ch == '\r' {
			return TemplateToken{}, l.errorAt(startLine, startCol, "template expression must be inline")
		}

		if mode == ExprRaw {
			if ch == '>' && l.peekN(1) == '}' && l.peekN(2) == '}' {
				raw := l.src[start:l.pos]
				lit, exprStart, exprEnd := trimmedTemplateExpr(raw, start)
				l.advanceN(3) // >}}

				if lit == "" {
					return TemplateToken{}, l.errorAt(startLine, startCol, "empty raw template expression")
				}

				if strings.Contains(lit, "<") || strings.Contains(lit, ">") {
					return TemplateToken{}, l.errorAt(startLine, startCol, "raw template expression must be a Go expression, not HTML")
				}

				return TemplateToken{
					Kind:     TokenExpr,
					Lit:      lit,
					ExprMode: ExprRaw,
					Source:   l.sourceRange(tokenStart, l.pos),
					GoSource: l.sourceRange(exprStart, exprEnd),
					Line:     startLine,
					Col:      startCol,
				}, nil
			}

			l.advance()
			continue
		}

		if ch == '}' && l.peekN(1) == '}' {
			raw := l.src[start:l.pos]
			lit, exprStart, exprEnd := trimmedTemplateExpr(raw, start)
			l.advanceN(2) // }}

			if lit == "" {
				return TemplateToken{}, l.errorAt(startLine, startCol, "empty template expression")
			}

			return TemplateToken{
				Kind:     TokenExpr,
				Lit:      lit,
				ExprMode: ExprEscaped,
				Source:   l.sourceRange(tokenStart, l.pos),
				GoSource: l.sourceRange(exprStart, exprEnd),
				Line:     startLine,
				Col:      startCol,
			}, nil
		}

		l.advance()
	}

	if mode == ExprRaw {
		return TemplateToken{}, l.errorAt(startLine, startCol, "unclosed raw template expression, expected >}}")
	}

	return TemplateToken{}, l.errorAt(startLine, startCol, "unclosed template expression, expected }}")
}

// =========================
// LOW LEVEL
// =========================

func (l *TemplateLexer) looksLikeHTMLStart() bool {
	return l.match("<!--") ||
		l.looksLikeCloseTagStart() ||
		l.match("<:") ||
		l.match("<&") ||
		l.match("<@") ||
		l.looksLikeOpenTagStart()
}

func (l *TemplateLexer) looksLikeOpenTagStart() bool {
	return l.peek() == '<' && isIdentStart(l.peekN(1))
}

func (l *TemplateLexer) looksLikeCloseTagStart() bool {
	if !l.match("</") {
		return false
	}

	next := l.peekN(2)
	return isIdentStart(next) || next == ':' || next == '&' || next == '@'
}

func (l *TemplateLexer) eof() bool {
	return l.pos >= len(l.src)
}

func (l *TemplateLexer) peek() byte {
	if l.eof() {
		return 0
	}
	return l.src[l.pos]
}

func (l *TemplateLexer) match(s string) bool {
	return strings.HasPrefix(l.src[l.pos:], s)
}

func (l *TemplateLexer) advance() {
	if l.eof() {
		return
	}

	if l.src[l.pos] == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}

	l.pos++
}

func (l *TemplateLexer) advanceN(n int) {
	for i := 0; i < n; i++ {
		l.advance()
	}
}

func (l *TemplateLexer) skipSpaces() {
	for !l.eof() {
		switch l.peek() {
		case ' ', '\t', '\n', '\r':
			l.advance()
		default:
			return
		}
	}
}

func (l *TemplateLexer) skipLineComment() {
	for !l.eof() {
		if l.peek() == '\n' {
			return
		}

		l.advance()
	}
}

func (l *TemplateLexer) skipBlockComment() error {
	l.advance() // /
	l.advance() // *

	for !l.eof() {
		if l.peek() == '*' && l.peekN(1) == '/' {
			l.advance()
			l.advance()
			return nil
		}

		l.advance()
	}

	return l.errorf("unclosed block comment")
}

func (l *TemplateLexer) peekN(n int) byte {
	pos := l.pos + n

	if pos >= len(l.src) {
		return 0
	}

	return l.src[pos]
}

func (l *TemplateLexer) token(kind TemplateTokenKind, lit string) TemplateToken {
	end := l.pos + len(lit)
	if end > len(l.src) {
		end = len(l.src)
	}
	return TemplateToken{
		Kind:   kind,
		Lit:    lit,
		Source: l.sourceRange(l.pos, end),
		Line:   l.line,
		Col:    l.col,
	}
}

func (l *TemplateLexer) sourceRange(start, end int) goxmap.SourceRange {
	return goxmap.SourceRange{
		URI: l.file,
		Range: goxmap.Range{
			Start: l.sourceBase + start,
			End:   l.sourceBase + end,
		},
	}
}

func trimmedTemplateExpr(raw string, absoluteStart int) (string, int, int) {
	leftTrimmed := strings.TrimLeftFunc(raw, unicode.IsSpace)
	left := len(raw) - len(leftTrimmed)
	trimmed := strings.TrimRightFunc(leftTrimmed, unicode.IsSpace)
	return trimmed, absoluteStart + left, absoluteStart + left + len(trimmed)
}

func (l *TemplateLexer) errorf(format string, args ...any) error {
	return l.errorAt(l.line, l.col, fmt.Sprintf(format, args...))
}

func (l *TemplateLexer) errorAt(line int, col int, msg string) error {
	return newTemplateCompileError("template.lexer", l.file, line, col, msg)
}

func isIdentStart(ch byte) bool {
	return ch == '_' || unicode.IsLetter(rune(ch))
}

func isIdentPart(ch byte) bool {
	return ch == '_' ||
		ch == '.' ||
		unicode.IsLetter(rune(ch)) ||
		unicode.IsDigit(rune(ch))
}
