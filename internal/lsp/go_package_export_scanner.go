package lsp

import (
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// goPackageExportNamesFromScanner builds the production shallow global export
// index. Exhaustive equivalence coverage compares it with an AST oracle across
// the active standard library, while the scanner avoids constructing full ASTs
// for declarations whose detailed metadata may never be requested.
//
// The scanner records only top-level exported declaration names and their
// coarse kinds. Function bodies and type/value expressions are tokenized but
// never materialized as AST nodes. Detailed metadata remains lazy and continues
// to use goPackageExportsFromSource only for packages that match the prefix.
func goPackageExportNamesFromScanner(loaded *packages.Package, importPath string, preserveInternalMarkers bool) (GoPackage, error) {
	if loaded == nil {
		return GoPackage{}, fmt.Errorf("Go package %q could not be loaded", importPath)
	}
	if len(loaded.GoFiles) == 0 {
		return GoPackage{}, fmt.Errorf("Go package %q has no source files", importPath)
	}

	exports := make(map[string]GoPackageExport)
	var firstScanErr error
	for _, filename := range loaded.GoFiles {
		source, err := os.ReadFile(filename)
		if err != nil {
			if firstScanErr == nil {
				firstScanErr = err
			}
			continue
		}
		if err := goScannerSourceExportNames(filename, source, preserveInternalMarkers, exports); err != nil {
			if firstScanErr == nil {
				firstScanErr = err
			}
		}
	}
	if len(exports) == 0 && firstScanErr != nil {
		return GoPackage{}, firstScanErr
	}

	result := make([]GoPackageExport, 0, len(exports))
	for _, exported := range exports {
		result = append(result, exported)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return GoPackage{Path: importPath, Name: loaded.Name, Exports: result}, nil
}

type goExportScanToken struct {
	tok  token.Token
	lit  string
	line int
}

type goExportTokenStream struct {
	scanner scanner.Scanner
	file    *token.File
	peeked  *goExportScanToken
	err     error
}

func newGoExportTokenStream(filename string, source []byte) *goExportTokenStream {
	files := token.NewFileSet()
	file := files.AddFile(filename, -1, len(source))
	stream := &goExportTokenStream{file: file}
	stream.scanner.Init(file, source, func(position token.Position, message string) {
		if stream.err == nil {
			stream.err = fmt.Errorf("%s: %s", position, message)
		}
	}, 0)
	return stream
}

func (s *goExportTokenStream) next() goExportScanToken {
	if s.peeked != nil {
		value := *s.peeked
		s.peeked = nil
		return value
	}
	position, tok, literal := s.scanner.Scan()
	return goExportScanToken{tok: tok, lit: literal, line: s.file.Position(position).Line}
}

func (s *goExportTokenStream) unread(value goExportScanToken) {
	if s.peeked != nil {
		panic("go export scanner supports a single token of lookahead")
	}
	s.peeked = &value
}

type goExportMarkerLines struct {
	lines []string
}

func newGoExportMarkerLines(source []byte) goExportMarkerLines {
	return goExportMarkerLines{lines: strings.Split(string(source), "\n")}
}

func (m goExportMarkerLines) docHasInternalMarker(line int) bool {
	index := line - 2
	for index >= 0 {
		text := strings.TrimSpace(m.lines[index])
		if text == "" {
			return false
		}
		if strings.HasPrefix(text, "//") {
			if strings.Contains(text, goxInternalAPIMarker) {
				return true
			}
			index--
			continue
		}
		if strings.HasSuffix(text, "*/") {
			for index >= 0 {
				blockLine := m.lines[index]
				if strings.Contains(blockLine, goxInternalAPIMarker) {
					return true
				}
				if strings.Contains(blockLine, "/*") {
					index--
					break
				}
				index--
			}
			continue
		}
		return false
	}
	return false
}

func (m goExportMarkerLines) lineHasInternalMarker(line int) bool {
	if line <= 0 || line > len(m.lines) {
		return false
	}
	text := m.lines[line-1]
	comment := strings.Index(text, "//")
	if comment >= 0 && strings.Contains(text[comment:], goxInternalAPIMarker) {
		return true
	}
	block := strings.Index(text, "/*")
	return block >= 0 && strings.Contains(text[block:], goxInternalAPIMarker)
}

func goScannerSourceExportNames(filename string, source []byte, preserveInternalMarkers bool, exports map[string]GoPackageExport) error {
	stream := newGoExportTokenStream(filename, source)
	var markers goExportMarkerLines
	if preserveInternalMarkers {
		markers = newGoExportMarkerLines(source)
	}

	for {
		value := stream.next()
		switch value.tok {
		case token.EOF:
			return stream.err
		case token.FUNC:
			goScanTopLevelFunction(stream, markers, preserveInternalMarkers, value.line, exports)
		case token.TYPE:
			goScanTopLevelTypeDecl(stream, markers, preserveInternalMarkers, value.line, exports)
		case token.CONST, token.VAR:
			goScanTopLevelValueDecl(stream, markers, preserveInternalMarkers, value.tok, value.line, exports)
		}
	}
}

func goScanTopLevelFunction(stream *goExportTokenStream, markers goExportMarkerLines, preserveInternal bool, declarationLine int, exports map[string]GoPackageExport) {
	first := stream.next()
	if first.tok == token.IDENT {
		internal := preserveInternal && markers.docHasInternalMarker(declarationLine)
		if token.IsExported(first.lit) && !internal {
			exports[first.lit] = GoPackageExport{Name: first.lit, Kind: "func"}
		}
	}
	goScanUntilDeclarationEnd(stream, false)
}

func goScanTopLevelTypeDecl(stream *goExportTokenStream, markers goExportMarkerLines, preserveInternal bool, declarationLine int, exports map[string]GoPackageExport) {
	declarationInternal := preserveInternal && markers.docHasInternalMarker(declarationLine)
	first := stream.next()
	if first.tok == token.LPAREN {
		for {
			value := stream.next()
			switch value.tok {
			case token.EOF, token.RPAREN:
				return
			case token.SEMICOLON:
				continue
			case token.IDENT:
				if goScanTypeSpec(stream, markers, preserveInternal, declarationInternal, value, exports, true) {
					return
				}
			default:
				goScanUntilSpecEnd(stream, true)
			}
		}
	}
	if first.tok == token.IDENT {
		goScanTypeSpec(stream, markers, preserveInternal, declarationInternal, first, exports, false)
		return
	}
	goScanUntilDeclarationEnd(stream, false)
}

func goScanTypeSpec(stream *goExportTokenStream, markers goExportMarkerLines, preserveInternal, declarationInternal bool, name goExportScanToken, exports map[string]GoPackageExport, grouped bool) bool {
	internal := declarationInternal
	if preserveInternal && !internal {
		internal = markers.docHasInternalMarker(name.line) || markers.lineHasInternalMarker(name.line)
	}
	kind := "type"
	alias := false

	value := stream.next()
	if value.tok == token.LBRACK {
		goScanBalanced(stream, token.LBRACK, token.RBRACK)
		value = stream.next()
	}
	if value.tok == token.ASSIGN {
		alias = true
		value = stream.next()
	}
	if value.tok == token.STRUCT {
		kind = "struct"
	} else if value.tok == token.INTERFACE {
		kind = "interface"
	}
	if token.IsExported(name.lit) && !internal {
		exports[name.lit] = GoPackageExport{Name: name.lit, Kind: kind, Alias: alias}
	}
	stream.unread(value)
	return goScanUntilSpecEnd(stream, grouped)
}

func goScanTopLevelValueDecl(stream *goExportTokenStream, markers goExportMarkerLines, preserveInternal bool, declaration token.Token, declarationLine int, exports map[string]GoPackageExport) {
	declarationInternal := preserveInternal && markers.docHasInternalMarker(declarationLine)
	kind := "var"
	if declaration == token.CONST {
		kind = "const"
	}
	first := stream.next()
	if first.tok == token.LPAREN {
		for {
			value := stream.next()
			switch value.tok {
			case token.EOF, token.RPAREN:
				return
			case token.SEMICOLON:
				continue
			case token.IDENT:
				if goScanValueSpec(stream, markers, preserveInternal, declarationInternal, kind, value, exports, true) {
					return
				}
			default:
				goScanUntilSpecEnd(stream, true)
			}
		}
	}
	if first.tok == token.IDENT {
		goScanValueSpec(stream, markers, preserveInternal, declarationInternal, kind, first, exports, false)
		return
	}
	goScanUntilDeclarationEnd(stream, false)
}

func goScanValueSpec(stream *goExportTokenStream, markers goExportMarkerLines, preserveInternal, declarationInternal bool, kind string, first goExportScanToken, exports map[string]GoPackageExport, grouped bool) bool {
	names := []goExportScanToken{first}
	for {
		value := stream.next()
		if value.tok != token.COMMA {
			stream.unread(value)
			break
		}
		name := stream.next()
		if name.tok != token.IDENT {
			stream.unread(name)
			break
		}
		names = append(names, name)
	}

	internal := declarationInternal
	if preserveInternal && !internal {
		internal = markers.docHasInternalMarker(first.line) || markers.lineHasInternalMarker(first.line)
	}
	if !internal {
		for _, name := range names {
			if token.IsExported(name.lit) {
				exports[name.lit] = GoPackageExport{Name: name.lit, Kind: kind}
			}
		}
	}
	return goScanUntilSpecEnd(stream, grouped)
}

func goScanBalanced(stream *goExportTokenStream, open, close token.Token) {
	depth := 1
	for depth > 0 {
		value := stream.next()
		switch value.tok {
		case token.EOF:
			return
		case open:
			depth++
		case close:
			depth--
		}
	}
}

// goScanUntilSpecEnd consumes a single type/value spec. For grouped
// declarations, an unmatched right parenthesis belongs to the enclosing group
// and is reported to the caller as the group's end.
func goScanUntilSpecEnd(stream *goExportTokenStream, grouped bool) bool {
	parenDepth, bracketDepth, braceDepth := 0, 0, 0
	for {
		value := stream.next()
		switch value.tok {
		case token.EOF:
			return grouped
		case token.LPAREN:
			parenDepth++
		case token.RPAREN:
			if parenDepth > 0 {
				parenDepth--
			} else if grouped && bracketDepth == 0 && braceDepth == 0 {
				return true
			}
		case token.LBRACK:
			bracketDepth++
		case token.RBRACK:
			if bracketDepth > 0 {
				bracketDepth--
			}
		case token.LBRACE:
			braceDepth++
		case token.RBRACE:
			if braceDepth > 0 {
				braceDepth--
			}
		case token.SEMICOLON:
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				return false
			}
		}
	}
}

func goScanUntilDeclarationEnd(stream *goExportTokenStream, grouped bool) {
	_ = goScanUntilSpecEnd(stream, grouped)
}
