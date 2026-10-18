package gox

import (
	"fmt"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

type TemplateReturn struct {
	ID         string
	ModuleName string
	FuncName   string
	Start      int
	End        int
	Source     goxmap.SourceRange
	Content    goxmap.SourceRange

	Raw      string
	Template *Template
}

func (t TemplateReturn) Occurrence() TemplateOccurrence {
	return TemplateOccurrence{
		Kind:       TemplateOccurrenceReturn,
		ModuleName: t.ModuleName,
		FuncName:   t.FuncName,
		Source:     t.Source,
		Content:    t.Content,
		Raw:        t.Raw,
		Template:   t.Template,
	}
}

type TemplateReturnExtraction struct {
	Source    string
	SourceMap *goxmap.Map
	Templates []TemplateReturn
}

func ExtractTemplateReturns(moduleName string, path string, src string) (*TemplateReturnExtraction, error) {
	return ExtractTemplateReturnsMapped(moduleName, path, src, nil)
}

func ExtractTemplateReturnsMapped(moduleName string, path string, src string, upstream *goxmap.Map) (*TemplateReturnExtraction, error) {
	generatedURI := path + "#template-returns"
	sourceURI := path
	if upstream != nil {
		sourceURI = upstream.GeneratedURI()
	}
	out, err := goxmap.NewWriter(generatedURI, sourceURI, src)
	if err != nil {
		return nil, err
	}
	var templates []TemplateReturn

	i := 0
	id := 0

	for i < len(src) {
		idx := nextTemplateReturnKeyword(src, i)
		if idx == -1 {
			if err := out.WriteSource(goxmap.Range{Start: i, End: len(src)}); err != nil {
				return nil, err
			}
			break
		}

		if err := out.WriteSource(goxmap.Range{Start: i, End: idx}); err != nil {
			return nil, err
		}

		if !isReturnKeyword(src, idx) {
			if err := out.WriteSource(goxmap.Range{Start: idx, End: idx + len("return")}); err != nil {
				return nil, err
			}
			i = idx + len("return")
			continue
		}

		j := skipInlineSpaces(src, idx+len("return"))
		if j >= len(src) {
			if err := out.WriteSource(goxmap.Range{Start: idx, End: idx + len("return")}); err != nil {
				return nil, err
			}
			i = idx + len("return")
			continue
		}

		if src[j] == '\n' || src[j] == '\r' {
			next := skipSpaces(src, j)
			if next < len(src) && (looksLikeTemplateFragmentStart(src, next) || looksLikeTemplateExpressionStart(src, next)) {
				line, col := sourceLineColumn(src, next)
				err := newTemplateCompileError(
					"template.return.multiline_requires_fragment",
					path,
					line,
					col,
					"multiline template return requires a fragment",
				)
				err.Hint = "Use `return <> ... </>` for multiline template content, or keep single-tag HTML on the same line as `return`."
				return nil, err
			}

			if err := out.WriteSource(goxmap.Range{Start: idx, End: idx + len("return")}); err != nil {
				return nil, err
			}
			i = idx + len("return")
			continue
		}

		var (
			contentStart int
			raw          string
			end          int
			err          error
		)

		switch {
		case src[j] == '(':
			contentStart = skipSpaces(src, j+1)
			if contentStart < len(src) && (looksLikeTemplateFragmentStart(src, contentStart) || looksLikeTemplateExpressionStart(src, contentStart)) {
				line, col := sourceLineColumn(src, j)
				err := newTemplateCompileError(
					"template.return.parentheses_removed",
					path,
					line,
					col,
					"template returns no longer use parentheses",
				)
				err.Hint = "Use `return <tag>...</tag>` for a single HTML tag or `return <> ... </>` for multiline template content."
				return nil, err
			}

			if err := out.WriteSource(goxmap.Range{Start: idx, End: idx + len("return")}); err != nil {
				return nil, err
			}
			i = idx + len("return")
			continue

		case looksLikeTemplateFragmentStart(src, j):
			raw, end, contentStart, err = readTemplateFragmentByDelim(path, src, j)
			if err != nil {
				return nil, err
			}

		case looksLikeTemplateExpressionStart(src, j):
			contentStart = j
			raw, end, err = readTemplateExpressionByHTML(path, src, j)
			if err != nil {
				return nil, err
			}

		default:
			if err := out.WriteSource(goxmap.Range{Start: idx, End: idx + len("return")}); err != nil {
				return nil, err
			}
			i = idx + len("return")
			continue
		}

		baseLine, baseCol := sourceLineColumn(src, contentStart)
		fullSource, err := exactTemplateSourceRange(path, upstream, j, end)
		if err != nil {
			return nil, err
		}
		contentSource, err := exactTemplateSourceRange(path, upstream, contentStart, contentStart+len(raw))
		if err != nil {
			return nil, err
		}

		tpl, err := ParseTemplateAtOffset(raw, contentSource.URI, baseLine, baseCol, contentSource.Range.Start)
		if err != nil {
			return nil, err
		}

		id++
		funcName := findCurrentFunctionName(src, idx)

		placeholder := fmt.Sprintf(
			"__gox_%s_%s_Template_%d",
			moduleName,
			funcName,
			id,
		)

		originalStart := mappedTemplateOffset(upstream, contentStart, false)
		originalEnd := mappedTemplateOffset(upstream, end, true)
		templates = append(templates, TemplateReturn{
			ID:         placeholder,
			ModuleName: moduleName,
			FuncName:   funcName,
			Start:      originalStart,
			End:        originalEnd,
			Source:     fullSource,
			Content:    contentSource,
			Raw:        raw,
			Template:   tpl,
		})

		returnEnd := idx + len("return")
		if err := out.WriteSource(goxmap.Range{Start: idx, End: returnEnd}); err != nil {
			return nil, err
		}
		if err := out.WriteGenerated(" ", goxmap.Range{Start: returnEnd, End: contentStart}); err != nil {
			return nil, err
		}
		if err := out.WriteGenerated(placeholder, goxmap.Range{Start: j, End: end}); err != nil {
			return nil, err
		}

		i = end
	}
	generatedSource, sourceMap, err := out.Build()
	if err != nil {
		return nil, err
	}
	if upstream != nil {
		sourceMap, err = goxmap.Compose(generatedURI, sourceMap, upstream)
		if err != nil {
			return nil, err
		}
	}

	return &TemplateReturnExtraction{
		Source:    generatedSource,
		SourceMap: sourceMap,
		Templates: templates,
	}, nil
}

// Search only Go code. Comments and literals may contain complete templates,
// but must remain untouched by the template extraction pass.
func nextTemplateReturnKeyword(src string, start int) int {
	for i := start; i < len(src); {
		if shouldSkipGoStringOrComment(src, i) {
			next, err := skipGoStringOrComment(src, i)
			if err != nil {
				// Leave malformed literals/comments to the Go parser.
				return -1
			}
			i = next
			continue
		}
		if strings.HasPrefix(src[i:], "return") && isReturnKeyword(src, i) {
			return i
		}
		i++
	}
	return -1
}

func mappedTemplateOffset(upstream *goxmap.Map, offset int, end bool) int {
	if upstream == nil {
		return offset
	}
	lookup := offset
	if end && lookup > 0 {
		lookup--
	}
	position, ok := upstream.OriginalPosition(lookup)
	if !ok {
		return offset
	}
	if end {
		return position.Offset + 1
	}
	return position.Offset
}

func isReturnKeyword(src string, idx int) bool {
	beforeOK := idx == 0 || !isIdentByte(src[idx-1])

	end := idx + len("return")
	afterOK := end >= len(src) || !isIdentByte(src[end])

	return beforeOK && afterOK
}

func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

func readTemplateReturnByHTML(path string, src string, openParen int) (string, int, error) {
	start := skipSpaces(src, openParen+1)

	baseLine, baseCol := sourceLineColumn(src, start)

	tokens, rawEnd, err := readTemplateRootTokens(path, src[start:], baseLine, baseCol)
	if err != nil {
		return "", 0, err
	}

	p := &TemplateParser{tokens: tokens, file: path}

	_, err = p.Parse()
	if err != nil {
		return "", 0, err
	}

	endAbs := start + rawEnd

	j := skipSpaces(src, endAbs)
	if j >= len(src) || src[j] != ')' {
		return "", 0, newTemplateErrorWithHint("expected-closing-template-return", TemplatePosition{File: path, Line: baseLine, Column: baseCol}, "expected closing ')' after template", "Close the template return block with a matching ')'.")
	}

	raw := strings.TrimSpace(src[start:endAbs])

	return raw, j, nil
}

func findTemplateRawEnd(src string, lastTok TemplateToken) int {
	line := 1
	col := 1

	for i := 0; i < len(src); i++ {
		if line == lastTok.Line && col == lastTok.Col {
			return i + len(lastTok.Lit)
		}

		if src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}

	return -1
}

func skipInlineSpaces(src string, i int) int {
	for i < len(src) {
		switch src[i] {
		case ' ', '\t':
			i++
		default:
			return i
		}
	}

	return i
}

func skipSpaces(src string, i int) int {
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}

	return i
}

func findCurrentFunctionName(src string, idx int) string {
	before := src[:idx]

	lines := strings.Split(before, "\n")

	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])

		if !strings.HasPrefix(line, "func ") {
			continue
		}

		line = strings.TrimPrefix(line, "func ")

		if strings.HasPrefix(line, "(") {
			parts := strings.Split(line, ")")
			if len(parts) < 2 {
				return "unknown"
			}

			line = strings.TrimSpace(parts[1])
		}

		nameEnd := strings.Index(line, "(")
		if nameEnd == -1 {
			return "unknown"
		}

		return strings.TrimSpace(line[:nameEnd])
	}

	return "unknown"
}

func sourceLineColumn(src string, pos int) (int, int) {
	line := 1
	col := 1
	if pos < 0 {
		pos = 0
	}
	if pos > len(src) {
		pos = len(src)
	}
	for i := 0; i < pos; i++ {
		if src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func readTemplateRootTokens(path string, src string, baseLine int, baseCol int) ([]TemplateToken, int, error) {
	lexer := NewTemplateLexerAt(src, path, baseLine, baseCol)

	var tokens []TemplateToken
	var lastParseErr error

	for {
		tok, err := lexer.NextToken()
		if err != nil {
			return nil, 0, err
		}

		tokens = append(tokens, tok)

		if tok.Kind == TokenEOF {
			if lastParseErr != nil {
				return nil, 0, lastParseErr
			}
			return nil, 0, newTemplateCompileError("template.unexpected_eof", path, tok.Line, tok.Col, "unexpected EOF while reading template")
		}

		// Solo intentamos cerrar el return cuando el template ya puede
		// estar estructuralmente completo. La decisión final no depende
		// de tener un solo root: depende de que lo próximo real sea ')'.
		if tok.Kind != TokenTagEnd && tok.Kind != TokenSelfCloseEnd {
			continue
		}

		j := skipSpaces(src, lexer.pos)
		if j >= len(src) || src[j] != ')' {
			continue
		}

		p := &TemplateParser{
			tokens: tokens,
			file:   path,
		}

		if _, err := p.Parse(); err != nil {
			lastParseErr = err
			continue
		}

		return tokens, lexer.pos, nil
	}
}
