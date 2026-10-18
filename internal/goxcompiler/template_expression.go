package gox

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"regexp"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

var templateVarComponentDeclRE = regexp.MustCompile(`var[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]+gox\.Component[ \t]*=[ \t]*$`)
var templateVarInferredDeclRE = regexp.MustCompile(`var[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]*=[ \t]*$`)

func TransformTemplateExpressions(path string, src string) (string, error) {
	out, _, err := TransformTemplateExpressionsWithStatics("", path, src)
	return out, err
}

func TransformTemplateExpressionsWithStatics(moduleName string, path string, src string) (string, []TemplateStaticDecl, error) {
	transformed, err := TransformTemplateExpressionsMapped(moduleName, path, src, nil)
	if err != nil {
		return "", nil, err
	}
	return transformed.Source, transformed.Statics, nil
}

// TemplateExpressionTransformation contains one transformed source document
// and the map from that document back to its original GOX input.
type TemplateExpressionTransformation struct {
	Source      string
	SourceMap   *goxmap.Map
	Statics     []TemplateStaticDecl
	Occurrences []TemplateOccurrence
}

// TransformTemplateExpressionsMapped rewrites template expressions and emits
// their source map. When upstream is provided, the stage map is composed with
// it so the result resolves directly to the original GOX source.
func TransformTemplateExpressionsMapped(moduleName string, path string, src string, upstream *goxmap.Map) (*TemplateExpressionTransformation, error) {
	sourceURI := path
	if upstream != nil {
		sourceURI = upstream.GeneratedURI()
	}
	generatedURI := path + "#template-expressions"
	out, err := goxmap.NewWriter(generatedURI, sourceURI, src)
	if err != nil {
		return nil, err
	}
	var statics []TemplateStaticDecl
	var occurrences []TemplateOccurrence

	last := 0
	exprID := 0
	for i := 0; i < len(src); {
		if shouldSkipGoStringOrComment(src, i) {
			next, err := skipGoStringOrComment(src, i)
			if err != nil {
				// Deja que go/parser reporte strings/comentarios inválidos con su error normal.
				i++
				continue
			}
			i = next
			continue
		}

		if src[i] != '<' || !isTemplateValuePositionStart(src, i) {
			i++
			continue
		}

		var (
			raw      string
			end      int
			rawStart int
			err      error
		)

		switch {
		case looksLikeTemplateFragmentStart(src, i):
			raw, end, rawStart, err = readTemplateFragmentByDelim(path, src, i)
		case looksLikeTemplateExpressionStart(src, i):
			raw, end, err = readTemplateExpressionByHTML(path, src, i)
			rawStart = i
		default:
			i++
			continue
		}
		if err != nil {
			return nil, err
		}

		exprID++
		prefix := out.String() + src[last:i]
		varName := templateExpressionVarName(prefix)
		funcName := templateExpressionFuncNameAt(src, i)
		staticName := templateExpressionStaticName(moduleName, funcName, varName, exprID)
		var literals []string
		fullSource, err := exactTemplateSourceRange(path, upstream, i, end)
		if err != nil {
			return nil, err
		}
		contentSource, err := exactTemplateSourceRange(path, upstream, rawStart, rawStart+len(raw))
		if err != nil {
			return nil, err
		}
		funcSrc, tpl, err := buildTemplateComponentFuncLiteralSource(path, src, rawStart, raw, contentSource.Range.Start, staticName, &literals)
		if err != nil {
			return nil, err
		}
		occurrences = append(occurrences, TemplateOccurrence{
			Kind:       TemplateOccurrenceValue,
			ModuleName: moduleName,
			FuncName:   funcName,
			Source:     fullSource,
			Content:    contentSource,
			Raw:        raw,
			Template:   tpl,
			StaticName: staticName,
		})
		if len(literals) > 0 {
			statics = append(statics, TemplateStaticDecl{Name: staticName, Literals: literals})
		}

		if err := rejectTrailingInferredVarTemplateDecl(path, src, i, prefix); err != nil {
			return nil, err
		}
		componentDecl := templateVarComponentDeclRE.FindStringSubmatchIndex(prefix)
		if componentDecl != nil && componentDecl[1] == len(prefix) && componentDecl[0] >= out.Len() {
			declStart := last + componentDecl[0] - out.Len()
			nameStart := last + componentDecl[2] - out.Len()
			nameEnd := last + componentDecl[3] - out.Len()
			if err := out.WriteSource(goxmap.Range{Start: last, End: declStart}); err != nil {
				return nil, err
			}
			if err := out.WriteGenerated("var ", goxmap.Range{Start: declStart, End: nameStart}); err != nil {
				return nil, err
			}
			if err := out.WriteSource(goxmap.Range{Start: nameStart, End: nameEnd}); err != nil {
				return nil, err
			}
			if err := out.WriteGenerated(" = ", goxmap.Range{Start: nameEnd, End: i}); err != nil {
				return nil, err
			}
		} else if err := out.WriteSource(goxmap.Range{Start: last, End: i}); err != nil {
			return nil, err
		}
		if err := out.WriteGenerated(funcSrc, goxmap.Range{Start: i, End: end}); err != nil {
			return nil, err
		}

		i = end
		last = end
	}

	if err := out.WriteSource(goxmap.Range{Start: last, End: len(src)}); err != nil {
		return nil, err
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
	return &TemplateExpressionTransformation{Source: generatedSource, SourceMap: sourceMap, Statics: statics, Occurrences: occurrences}, nil
}

// Template values are currently legal after an assignment or as a call
// argument. Template returns are extracted earlier by ExtractTemplateReturns.
// Restricting this to syntactic value boundaries avoids treating ordinary Go
// comparisons as template expressions.
func isTemplateValuePositionStart(src string, i int) bool {
	if isAssignmentRHSStart(src, i) {
		return true
	}
	j := i - 1
	for j >= 0 {
		switch src[j] {
		case ' ', '\t', '\n', '\r':
			j--
			continue
		}
		break
	}
	return j >= 0 && (src[j] == ',' || src[j] == '(')
}

func templateExpressionStaticName(moduleName string, funcName string, varName string, id int) string {
	if varName == "" {
		varName = "Expr"
	}

	varPart := varName
	if varName == "Expr" {
		varPart = sanitizeGoIdentPart(varName)
	}

	if funcName == "" {
		return "goxStatic" + varPart
	}

	if moduleName == "" {
		return "goxStatic" + sanitizeGoIdentPart(funcName) + varPart
	}

	return "goxStatic" + sanitizeGoIdentPart(moduleName) + sanitizeGoIdentPart(funcName) + varPart
}

func templateExpressionVarName(prefix string) string {
	if match := templateVarComponentDeclRE.FindStringSubmatch(prefix); match != nil {
		return match[1]
	}

	shortDeclRE := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)[ \t]*:=[ \t]*$`)
	if match := shortDeclRE.FindStringSubmatch(prefix); match != nil {
		return match[1]
	}

	return ""
}

func templateExpressionFuncNameAt(src string, pos int) string {
	depth := 0
	currentFunc := ""
	currentFuncDepth := -1
	pendingFunc := ""

	for i := 0; i < pos; {
		if shouldSkipGoStringOrComment(src, i) {
			next, err := skipGoStringOrComment(src, i)
			if err == nil && next > i {
				i = next
				continue
			}
		}

		if isKeywordAt(src, i, "func") {
			name, next := readFuncDeclName(src, i+len("func"))
			if name != "" {
				pendingFunc = name
				i = next
				continue
			}
		}

		switch src[i] {
		case '{':
			depth++
			if pendingFunc != "" {
				currentFunc = pendingFunc
				currentFuncDepth = depth
				pendingFunc = ""
			}
		case '}':
			if currentFuncDepth == depth {
				currentFunc = ""
				currentFuncDepth = -1
			}
			if depth > 0 {
				depth--
			}
		}

		i++
	}

	return currentFunc
}

func readFuncDeclName(src string, i int) (string, int) {
	i = skipSpaces(src, i)
	if i < len(src) && src[i] == '(' {
		depth := 1
		i++
		for i < len(src) && depth > 0 {
			switch src[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
			i++
		}
		i = skipSpaces(src, i)
	}

	if i >= len(src) || !isIdentStart(src[i]) {
		return "", i
	}

	start := i
	i++
	for i < len(src) && isIdentPart(src[i]) {
		i++
	}

	return src[start:i], i
}

func isKeywordAt(src string, i int, keyword string) bool {
	if i < 0 || i+len(keyword) > len(src) || src[i:i+len(keyword)] != keyword {
		return false
	}
	if i > 0 && isIdentPart(src[i-1]) {
		return false
	}
	end := i + len(keyword)
	return end >= len(src) || !isIdentPart(src[end])
}

func looksLikeTemplateFragmentStart(src string, i int) bool {
	return i >= 0 && i+1 < len(src) && src[i] == '<' && src[i+1] == '>'
}

func looksLikeTemplateExpressionStart(src string, i int) bool {
	if i < 0 || i >= len(src) || src[i] != '<' {
		return false
	}

	if i+1 >= len(src) {
		return false
	}

	next := src[i+1]
	return isIdentStart(next) || next == ':' || next == '&' || next == '@'
}

func isAssignmentRHSStart(src string, i int) bool {
	j := i - 1
	for j >= 0 {
		switch src[j] {
		case ' ', '\t', '\n', '\r':
			j--
			continue
		}
		break
	}

	if j < 0 || src[j] != '=' {
		return false
	}

	if j > 0 {
		switch src[j-1] {
		case '=', '!', '<', '>':
			return false
		}
	}

	return true
}

func rejectTrailingInferredVarTemplateDecl(path string, src string, start int, generatedPrefix string) error {
	loc := templateVarInferredDeclRE.FindStringSubmatchIndex(generatedPrefix)
	if loc == nil || loc[1] != len(generatedPrefix) {
		return nil
	}

	line, col := sourceLineColumn(src, start)
	err := newTemplateCompileError(
		"template.expression.invalid-var-declaration",
		path,
		line,
		col,
		"template component expressions cannot be assigned with inferred var declarations",
	)
	err.Hint = "Use `Name := <tag>...</tag>` or `var Name gox.Component = <tag>...</tag>`."
	return err
}

func readTemplateExpressionByHTML(path string, src string, start int) (string, int, error) {
	baseLine, baseCol := sourceLineColumn(src, start)

	_, rawEnd, err := readTemplateExpressionRootTokens(path, src[start:], baseLine, baseCol)
	if err != nil {
		return "", 0, err
	}

	return src[start : start+rawEnd], start + rawEnd, nil
}

// readTemplateExpressionRootTokens finds the first complete template value in
// src. The lexer already exposes enough structure to find the root boundary in
// one pass, so valid templates must not repeatedly parse every growing token
// prefix just to discover where the root closes.
//
// The final candidate is still validated by TemplateParser before it is
// accepted. If the lightweight boundary tracking disagrees with the parser, we
// fall back to the historical parser-driven scan so malformed-input diagnostics
// keep their existing behavior without putting that quadratic work on the
// successful compile path.
func readTemplateExpressionRootTokens(path string, src string, baseLine int, baseCol int) ([]TemplateToken, int, error) {
	lexer := NewTemplateLexerAt(src, path, baseLine, baseCol)

	tokens := make([]TemplateToken, 0, 32)
	depth := 0
	rootStarted := false
	pendingKind := TokenEOF
	pendingName := ""
	wantName := false

	for {
		tok, err := lexer.NextToken()
		if err != nil {
			return nil, 0, err
		}
		tokens = append(tokens, tok)

		switch tok.Kind {
		case TokenOpenTagStart, TokenDirectiveTagStart, TokenComponentTagStart, TokenClientTagStart:
			rootStarted = true
			pendingKind = tok.Kind
			pendingName = ""
			wantName = true

		case TokenCloseTagStart:
			pendingKind = tok.Kind
			pendingName = ""
			wantName = true

		case TokenIdent:
			if wantName {
				pendingName = tok.Lit
				wantName = false
			}

		case TokenSelfCloseEnd:
			pendingKind = TokenEOF
			pendingName = ""
			wantName = false
			if rootStarted && depth == 0 {
				if templateExpressionTokensParse(tokens, path) {
					return tokens, lexer.pos, nil
				}
				return readTemplateExpressionRootTokensParserDriven(path, src, baseLine, baseCol)
			}

		case TokenTagEnd:
			switch pendingKind {
			case TokenCloseTagStart:
				if depth > 0 {
					depth--
				}
			case TokenOpenTagStart:
				if !isHTMLVoidTag(pendingName) {
					depth++
				}
			case TokenDirectiveTagStart, TokenComponentTagStart, TokenClientTagStart:
				depth++
			}
			pendingKind = TokenEOF
			pendingName = ""
			wantName = false

			if rootStarted && depth == 0 {
				if templateExpressionTokensParse(tokens, path) {
					return tokens, lexer.pos, nil
				}
				return readTemplateExpressionRootTokensParserDriven(path, src, baseLine, baseCol)
			}

		case TokenEOF:
			return readTemplateExpressionRootTokensParserDriven(path, src, baseLine, baseCol)
		}
	}
}

func templateExpressionTokensParse(tokens []TemplateToken, path string) bool {
	p := &TemplateParser{tokens: tokens, file: path}
	_, err := p.Parse()
	return err == nil
}

// readTemplateExpressionRootTokensParserDriven is the previous boundary
// algorithm. It is intentionally retained only as an error-path fallback so
// malformed templates preserve their established diagnostics.
func readTemplateExpressionRootTokensParserDriven(path string, src string, baseLine int, baseCol int) ([]TemplateToken, int, error) {
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
			return nil, 0, newTemplateCompileError("template.unexpected_eof", path, tok.Line, tok.Col, "unexpected EOF while reading template expression")
		}

		if tok.Kind != TokenTagEnd && tok.Kind != TokenSelfCloseEnd {
			continue
		}

		p := &TemplateParser{tokens: tokens, file: path}
		if _, err := p.Parse(); err != nil {
			lastParseErr = err
			continue
		}

		return tokens, lexer.pos, nil
	}
}

func buildTemplateComponentFuncLiteralSource(path string, fullSrc string, start int, raw string, sourceOffset int, staticName string, staticLiterals *[]string) (string, *Template, error) {
	baseLine, baseCol := sourceLineColumn(fullSrc, start)

	tpl, err := ParseTemplateAtOffset(raw, path, baseLine, baseCol, sourceOffset)
	if err != nil {
		return "", nil, err
	}

	stmts, err := GenerateTemplateStmts(tpl, &TemplateGenContext{
		WriterName:     "goxWriter",
		StaticVarName:  staticName,
		StaticLiterals: staticLiterals,
	})
	if err != nil {
		return "", nil, err
	}

	fn := &ast.FuncLit{
		Type: &ast.FuncType{
			Params: &ast.FieldList{
				List: []*ast.Field{
					{
						Names: []*ast.Ident{ast.NewIdent("goxWriter")},
						Type: &ast.SelectorExpr{
							X:   ast.NewIdent("gox"),
							Sel: ast.NewIdent("Writer"),
						},
					},
				},
			},
		},
		Body: &ast.BlockStmt{List: stmts},
	}

	var buf bytes.Buffer
	fset := token.NewFileSet()
	if err := format.Node(&buf, fset, fn); err != nil {
		return "", nil, err
	}

	return buf.String(), tpl, nil
}

func shouldSkipGoStringOrComment(src string, i int) bool {
	if i >= len(src) {
		return false
	}

	if src[i] == '"' || src[i] == '`' || src[i] == '\'' {
		return true
	}

	return src[i] == '/' && i+1 < len(src) && (src[i+1] == '/' || src[i+1] == '*')
}

func skipGoStringOrComment(src string, i int) (int, error) {
	switch src[i] {
	case '"', '\'':
		quote := src[i]
		i++
		for i < len(src) {
			if src[i] == '\\' {
				i += 2
				continue
			}
			if src[i] == quote {
				return i + 1, nil
			}
			i++
		}
		return i, nil

	case '`':
		i++
		for i < len(src) {
			if src[i] == '`' {
				return i + 1, nil
			}
			i++
		}
		return i, nil

	case '/':
		if i+1 >= len(src) {
			return i + 1, nil
		}
		if src[i+1] == '/' {
			i += 2
			for i < len(src) && src[i] != '\n' {
				i++
			}
			return i, nil
		}
		if src[i+1] == '*' {
			i += 2
			for i+1 < len(src) {
				if src[i] == '*' && src[i+1] == '/' {
					return i + 2, nil
				}
				i++
			}
			return i, nil
		}
	}

	return i + 1, nil
}

func findTemplateFragmentClose(src string, start int) int {
	for i := start; i < len(src); i++ {
		if src[i] == '\\' {
			i++
			continue
		}

		if strings.HasPrefix(src[i:], "</>") {
			return i
		}
	}

	return -1
}

func readTemplateFragmentByDelim(path string, src string, start int) (string, int, int, error) {
	if !looksLikeTemplateFragmentStart(src, start) {
		line, col := sourceLineColumn(src, start)
		return "", 0, 0, newTemplateCompileError("template.fragment.expected-open", path, line, col, "expected template fragment opening '<>'")
	}

	contentStart := start + len("<>")
	contentEnd := findTemplateFragmentClose(src, contentStart)
	if contentEnd == -1 {
		line, col := sourceLineColumn(src, start)
		err := newTemplateCompileError("template.fragment.unclosed", path, line, col, "unclosed template fragment")
		err.Hint = "Close the fragment with `</>`."
		return "", 0, 0, err
	}

	raw := src[contentStart:contentEnd]

	line, col := sourceLineColumn(src, contentStart)
	if _, err := ParseTemplateAt(raw, path, line, col); err != nil {
		return "", 0, 0, err
	}

	return raw, contentEnd + len("</>"), contentStart, nil
}
