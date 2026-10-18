package gox

import (
	"fmt"
	"go/token"
	"strconv"
	"strings"

	"github.com/gox-lang/server/internal/clientframework"
	"github.com/gox-lang/server/internal/goxmap"

	"github.com/gox-lang/server/internal/goxerrors"
)

func extractClientDecls(path string, src string) ([]ClientDecl, string, error) {
	clients, transformed, _, err := extractClientDeclsMapped(path, src, nil)
	return clients, transformed, err
}

func extractClientDeclsMapped(path string, src string, upstream *goxmap.Map) ([]ClientDecl, string, *goxmap.Map, error) {
	var clients []ClientDecl
	sourceURI := path
	if upstream != nil {
		sourceURI = upstream.GeneratedURI()
	}
	generatedURI := path + "#clients"
	out, err := goxmap.NewWriter(generatedURI, sourceURI, src)
	if err != nil {
		return nil, "", nil, err
	}
	last := 0

	braceDepth := 0
	inString := byte(0)
	inRawString := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(src); {
		if !inStringState(inString, inRawString, inLineComment, inBlockComment) && looksLikeClientDeclAtLineStart(src, i) {
			if braceDepth != 0 {
				line, col := lineColAt(src, i)
				err := newCompilerErrorWithHint(
					"invalid-client-scope",
					"client declarations must be declared at module top-level",
					"Move this client declaration outside functions, methods, blocks and template bodies.",
				)
				err.File = normalizeCompileErrorPath(path)
				err.Line = line
				err.Column = col
				return nil, "", nil, err
			}
			if !looksLikeTopLevelClientDecl(src, i) {
				// A top-level client declaration must still be placed at the start of the logical line.
				// If the line contains other code before client, leave it for the Go parser.
			} else {
				start := i

				decl, end, err := parseClientDeclAt(path, src, i, token.Pos(start+1))
				if err != nil {
					return nil, "", nil, err
				}

				decl.Pos = token.Pos(start + 1)
				nameOffset := int(decl.NamePos) - 1
				frameworkOffset := nameOffset + len(decl.Name)
				for frameworkOffset < len(src) && isSpace(src[frameworkOffset]) {
					frameworkOffset++
				}
				decl.SourceOffset = originalClientOffset(upstream, start)
				decl.NameOffset = originalClientOffset(upstream, nameOffset)
				decl.FrameworkOffset = originalClientOffset(upstream, frameworkOffset)
				decl.EntryOffset = originalClientOffset(upstream, decl.EntryOffset)
				decl.SrcKeyOffset = originalClientOffset(upstream, decl.SrcKeyOffset)
				if decl.DevDeclared {
					decl.DevKeyOffset = originalClientOffset(upstream, decl.DevKeyOffset)
					decl.DevValueOffset = originalClientOffset(upstream, decl.DevValueOffset)
				}
				clients = append(clients, decl)

				if err := out.WriteSource(goxmap.Range{Start: last, End: start}); err != nil {
					return nil, "", nil, err
				}
				masked := maskClientDeclaration(src[start:end])
				if err := out.WriteGenerated(masked, goxmap.Range{Start: start, End: end}); err != nil {
					return nil, "", nil, err
				}

				i = end
				last = end
				continue
			}
		}

		ch := src[i]

		if inLineComment {
			if ch == '\n' || ch == '\r' {
				inLineComment = false
			}
			i++
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(src) && src[i+1] == '/' {
				i += 2
				inBlockComment = false
				continue
			}
			i++
			continue
		}
		if inRawString {
			if ch == '`' {
				inRawString = false
			}
			i++
			continue
		}
		if inString != 0 {
			if ch == '\\' && i+1 < len(src) {
				i += 2
				continue
			}
			if ch == inString {
				inString = 0
			}
			i++
			continue
		}

		if ch == '/' && i+1 < len(src) {
			next := src[i+1]
			if next == '/' {
				i += 2
				inLineComment = true
				continue
			}
			if next == '*' {
				i += 2
				inBlockComment = true
				continue
			}
		}
		if ch == '`' {
			inRawString = true
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			inString = ch
			i++
			continue
		}
		if ch == '{' {
			braceDepth++
		} else if ch == '}' && braceDepth > 0 {
			braceDepth--
		}
		i++
	}

	if err := out.WriteSource(goxmap.Range{Start: last, End: len(src)}); err != nil {
		return nil, "", nil, err
	}
	transformed, sourceMap, err := out.Build()
	if err != nil {
		return nil, "", nil, err
	}
	if upstream != nil {
		sourceMap, err = goxmap.Compose(generatedURI, sourceMap, upstream)
		if err != nil {
			return nil, "", nil, err
		}
	}
	return clients, transformed, sourceMap, nil
}

func originalClientOffset(upstream *goxmap.Map, offset int) int {
	if upstream == nil {
		return offset
	}
	position, ok := upstream.OriginalPosition(offset)
	if !ok {
		return offset
	}
	return position.Offset
}

func maskClientDeclaration(src string) string {
	masked := []byte(src)
	for i := range masked {
		if masked[i] != '\n' {
			masked[i] = ' '
		}
	}
	return string(masked)
}

func inStringState(inString byte, inRawString, inLineComment, inBlockComment bool) bool {
	return inString != 0 || inRawString || inLineComment || inBlockComment
}

func looksLikeClientDeclAtLineStart(src string, i int) bool {
	if !strings.HasPrefix(src[i:], "client") {
		return false
	}
	if i > 0 {
		prev := src[i-1]
		if isIdentPart(prev) {
			return false
		}
	}
	end := i + len("client")
	if end >= len(src) || !isSpace(src[end]) {
		return false
	}
	for j := i - 1; j >= 0 && src[j] != '\n' && src[j] != '\r'; j-- {
		if src[j] != ' ' && src[j] != '\t' {
			return false
		}
	}
	return true
}

func looksLikeTopLevelClientDecl(src string, i int) bool {
	if !strings.HasPrefix(src[i:], "client") {
		return false
	}
	if i > 0 {
		prev := src[i-1]
		if isIdentPart(prev) {
			return false
		}
	}
	end := i + len("client")
	if end >= len(src) || !isSpace(src[end]) {
		return false
	}

	// Only accept declarations at the start of a logical line after whitespace.
	for j := i - 1; j >= 0 && src[j] != '\n' && src[j] != '\r'; j-- {
		if src[j] != ' ' && src[j] != '\t' {
			return false
		}
	}
	return true
}

func parseClientDeclAt(path string, src string, i int, pos token.Pos) (ClientDecl, int, error) {
	p := clientDeclParser{src: src, pos: i}
	declStart := i
	p.consumeWord("client")
	p.skipSpaces()

	nameStart := p.pos
	name := p.readIdent()
	if name == "" || !isValidIdent(name) || token.Lookup(name).IsKeyword() {
		return ClientDecl{}, i, compilerErrorAt(path, src, nameStart, diagnosticInvalidClientName, "invalid client component name", "Use: client <componentName> <frameworkName> { src: \"...\" }.")
	}

	p.skipSpaces()
	frameworkStart := p.pos
	frameworkName := p.readIdent()
	if frameworkName != strings.ToLower(frameworkName) {
		return ClientDecl{}, i, compilerErrorAt(path, src, frameworkStart, diagnosticInvalidClientFrameworkCase, fmt.Sprintf("client framework %q must be lowercase", frameworkName), "Use: "+strings.ToLower(frameworkName)+".")
	}
	framework, status, ok := parseClientFramework(frameworkName)
	if !ok {
		return ClientDecl{}, i, compilerErrorAt(path, src, frameworkStart, diagnosticInvalidClientFramework, fmt.Sprintf("unsupported client framework %q", frameworkName), "Supported frameworks: "+strings.Join(clientframework.SupportedNames(), ", ")+".")
	}
	if status == clientframework.NotSupportedYet {
		return ClientDecl{}, i, compilerErrorAt(path, src, frameworkStart, diagnosticClientFrameworkNotSupportedYet, fmt.Sprintf("client framework %q is not supported yet", strings.ToLower(frameworkName)), "Use one of: "+strings.Join(clientframework.SupportedNames(), ", ")+".")
	}

	p.skipSpaces()
	bracePos := p.pos
	if !p.consumeByte('{') {
		return ClientDecl{}, i, compilerErrorAt(path, src, bracePos, diagnosticInvalidClientDeclaration, "expected { after client declaration", "Use: client menu react { src: \"client/menu.jsx\" }.")
	}

	bodyStart := p.pos
	depth := 1
	inString := byte(0)
	for p.pos < len(src) {
		ch := src[p.pos]
		if inString != 0 {
			if ch == '\\' {
				p.pos += 2
				continue
			}
			if ch == inString {
				inString = 0
			}
			p.pos++
			continue
		}
		if ch == '\'' || ch == '"' {
			inString = ch
			p.pos++
			continue
		}
		if ch == '{' {
			depth++
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				body := src[bodyStart:p.pos]
				p.pos++
				decl, err := parseClientDeclBody(path, src, bodyStart, name, token.Pos(nameStart+1), framework, body, pos)
				return decl, p.pos, err
			}
		}
		p.pos++
	}

	return ClientDecl{}, i, compilerErrorAt(path, src, declStart, diagnosticUnclosedClientDeclaration, "unclosed client declaration", "Add } after the client declaration body.")
}

func parseClientDeclBody(path string, fullSrc string, bodyStart int, name string, namePos token.Pos, framework ClientFramework, body string, pos token.Pos) (ClientDecl, error) {
	decl := ClientDecl{Name: name, Framework: framework, Pos: pos, NamePos: namePos}
	seenProperties := make(map[string]struct{}, 2)
	offset := 0
	for offset <= len(body) {
		lineEnd := strings.IndexByte(body[offset:], '\n')
		lineLen := 0
		if lineEnd < 0 {
			lineLen = len(body) - offset
		} else {
			lineLen = lineEnd
		}

		rawLine := body[offset : offset+lineLen]
		lineStartAbs := bodyStart + offset
		trimLeft := strings.TrimLeft(rawLine, " \t\r")
		contentPos := lineStartAbs + len(rawLine) - len(trimLeft)
		line := strings.TrimSpace(rawLine)

		if line != "" && !strings.HasPrefix(line, "//") {
			colon := strings.Index(line, ":")
			if colon < 0 {
				return ClientDecl{}, compilerErrorAt(path, fullSrc, contentPos, diagnosticInvalidClientProperty, "invalid client declaration property", "Use key: value inside client declarations.")
			}

			key := strings.TrimSpace(line[:colon])
			keyPos := contentPos
			if idx := strings.Index(rawLine, key); idx >= 0 {
				keyPos = lineStartAbs + idx
			}
			if _, exists := seenProperties[key]; exists {
				return ClientDecl{}, compilerErrorAt(path, fullSrc, keyPos, diagnosticDuplicateClientProperty, fmt.Sprintf("duplicate client property %q", key), "Declare each client property only once.")
			}
			seenProperties[key] = struct{}{}
			valueStartInLine := strings.Index(rawLine, ":") + 1
			for valueStartInLine < len(rawLine) && (rawLine[valueStartInLine] == ' ' || rawLine[valueStartInLine] == '\t') {
				valueStartInLine++
			}
			valuePos := lineStartAbs + valueStartInLine
			valueText := rawLine[valueStartInLine:]

			switch key {
			case "src":
				decl.SrcKeyOffset = keyPos
				value, errPos, err := readClientStringValue(valueText, valuePos)
				if err != nil {
					if unexpected, ok := err.(*clientUnexpectedTokenError); ok {
						return ClientDecl{}, compilerErrorAt(path, fullSrc, errPos, "unexpected-token", fmt.Sprintf("unexpected token %q", unexpected.token), clientUnexpectedTokenHint(unexpected.token))
					}
					return ClientDecl{}, compilerErrorAt(path, fullSrc, errPos, diagnosticInvalidClientSrc, "client src must be a non-empty string", "Use src: \"client/menu.jsx\".")
				}
				if value == "" {
					return ClientDecl{}, compilerErrorAt(path, fullSrc, valuePos, diagnosticInvalidClientSrc, "client src must be a non-empty string", "Use src: \"client/menu.jsx\".")
				}
				decl.Entry = value
				decl.EntryPos = token.Pos(valuePos + 1)
				decl.EntryOffset = valuePos
			case "dev":
				decl.DevKeyOffset = keyPos
				decl.DevValueOffset = valuePos
				decl.DevDeclared = true
				value, errPos, err := readClientBoolValue(valueText, valuePos)
				if err != nil {
					if unexpected, ok := err.(*clientUnexpectedTokenError); ok {
						return ClientDecl{}, compilerErrorAt(path, fullSrc, errPos, "unexpected-token", fmt.Sprintf("unexpected token %q", unexpected.token), clientUnexpectedTokenHint(unexpected.token))
					}
					return ClientDecl{}, compilerErrorAt(path, fullSrc, errPos, diagnosticInvalidClientDev, "client dev must be true or false", "Use dev: true or dev: false.")
				}
				decl.Dev = value
			default:
				return ClientDecl{}, compilerErrorAt(path, fullSrc, keyPos, diagnosticUnknownClientProperty, fmt.Sprintf("unknown client property %q", key), "Supported properties: src, dev.")
			}
		}

		if lineEnd < 0 {
			break
		}
		offset += lineLen + 1
	}
	if decl.Entry == "" {
		return ClientDecl{}, compilerErrorAt(path, fullSrc, int(pos)-1, diagnosticMissingClientSrc, "client declaration requires src", "Add src: \"client/entry.jsx\".")
	}
	return decl, nil
}

func readClientStringValue(text string, absStart int) (string, int, error) {
	idx := 0
	for idx < len(text) && (text[idx] == ' ' || text[idx] == '\t' || text[idx] == '\r') {
		idx++
	}
	if idx >= len(text) || text[idx] != '"' {
		return "", absStart + idx, fmt.Errorf("expected double-quoted string literal")
	}
	quote := text[idx]
	end := idx + 1
	for end < len(text) {
		ch := text[end]
		if ch == '\\' {
			end += 2
			continue
		}
		if ch == quote {
			end++
			break
		}
		end++
	}
	if end > len(text) || text[end-1] != quote {
		return "", absStart + idx, fmt.Errorf("unterminated string literal")
	}
	literal := text[idx:end]
	value, err := strconv.Unquote(literal)
	if err != nil {
		return "", absStart + idx, err
	}
	if errPos, ok := clientUnexpectedTrailingToken(text[end:], absStart+end); ok {
		return "", errPos, &clientUnexpectedTokenError{token: stringAt(text, errPos-absStart)}
	}
	return value, absStart + idx, nil
}

func readClientBoolValue(text string, absStart int) (bool, int, error) {
	idx := 0
	for idx < len(text) && (text[idx] == ' ' || text[idx] == '\t' || text[idx] == '\r') {
		idx++
	}
	var value bool
	var end int
	if strings.HasPrefix(text[idx:], "true") && !hasClientIdentContinuation(text, idx+4) {
		value = true
		end = idx + 4
	} else if strings.HasPrefix(text[idx:], "false") && !hasClientIdentContinuation(text, idx+5) {
		value = false
		end = idx + 5
	} else {
		return false, absStart + idx, fmt.Errorf("expected bool literal")
	}
	if errPos, ok := clientUnexpectedTrailingToken(text[end:], absStart+end); ok {
		return false, errPos, &clientUnexpectedTokenError{token: stringAt(text, errPos-absStart)}
	}
	return value, absStart + idx, nil
}

func clientUnexpectedTrailingToken(rest string, absStart int) (int, bool) {
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case ' ', '\t', '\r':
			continue
		case ',':
			// A trailing comma is accepted for multi-line declaration ergonomics.
			for j := i + 1; j < len(rest); j++ {
				if rest[j] != ' ' && rest[j] != '\t' && rest[j] != '\r' {
					return absStart + j, true
				}
			}
			return 0, false
		default:
			return absStart + i, true
		}
	}
	return 0, false
}

func hasClientIdentContinuation(text string, idx int) bool {
	return idx < len(text) && isIdentPart(text[idx])
}

type clientUnexpectedTokenError struct{ token string }

func (e *clientUnexpectedTokenError) Error() string { return "unexpected token " + e.token }

func stringAt(text string, idx int) string {
	if idx < 0 || idx >= len(text) {
		return ""
	}
	return string(text[idx])
}

func clientUnexpectedTokenHint(token string) string {
	if token == ";" {
		return "Remove ';'. Client declarations do not use semicolons."
	}
	return "Remove the unexpected token or use key: value inside the client declaration."
}

func compilerErrorAt(path string, src string, pos int, kind string, message string, hint string) *goxerrors.GoxError {
	if pos < 0 {
		pos = 0
	}
	if pos > len(src) {
		pos = len(src)
	}
	line, col := lineColAt(src, pos)
	err := newCompilerErrorWithHint(kind, message, hint)
	err.File = normalizeCompileErrorPath(path)
	err.Line = line
	err.Column = col
	return err
}

func parseClientFramework(s string) (ClientFramework, clientframework.Status, bool) {
	def, ok := clientframework.Lookup(s)
	if !ok {
		return "", clientframework.Supported, false
	}
	switch def.Name {
	case "gox":
		return ClientGox, def.Status, true
	case "react":
		return ClientReact, def.Status, true
	case "preact":
		return ClientPreact, def.Status, true
	case "solid":
		return ClientSolid, def.Status, true
	case "vue":
		return ClientVue, def.Status, true
	case "svelte":
		return ClientSvelte, def.Status, true
	default:
		return "", def.Status, false
	}
}

type clientDeclParser struct {
	src string
	pos int
}

func (p *clientDeclParser) skipSpaces() {
	for p.pos < len(p.src) && isSpace(p.src[p.pos]) {
		p.pos++
	}
}

func (p *clientDeclParser) consumeWord(w string) bool {
	if strings.HasPrefix(p.src[p.pos:], w) {
		p.pos += len(w)
		return true
	}
	return false
}

func (p *clientDeclParser) consumeByte(ch byte) bool {
	if p.pos < len(p.src) && p.src[p.pos] == ch {
		p.pos++
		return true
	}
	return false
}

func (p *clientDeclParser) readIdent() string {
	start := p.pos
	if p.pos >= len(p.src) || !isIdentStart(p.src[p.pos]) {
		return ""
	}
	p.pos++
	for p.pos < len(p.src) && isIdentPart(p.src[p.pos]) {
		p.pos++
	}
	return p.src[start:p.pos]
}

func isSpace(ch byte) bool { return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' }

func lineColAt(src string, pos int) (int, int) {
	line, col := 1, 1
	for i := 0; i < pos && i < len(src); i++ {
		if src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}
