package lsp

import (
	"go/ast"
	"go/scanner"
	"go/token"
	"go/types"
	"strings"
)

// Repair only the element under the cursor. The semantic type of the literal
// owns its fields, including nested literals whose type is omitted in source.
func (s *Server) structLiteralCompletions(document Document, offset int, prefix string) ([]CompletionItem, bool) {
	start := offset - len(prefix)
	if start < 0 {
		return nil, false
	}
	var scan scanner.Scanner
	scan.Init(token.NewFileSet().AddFile("", -1, len(document.Text[:start])), []byte(document.Text[:start]), nil, 0)
	previous := token.ILLEGAL
	for {
		_, tok, _ := scan.Scan()
		if tok == token.EOF {
			break
		}
		previous = tok
	}
	if previous != token.LBRACE && previous != token.COMMA {
		return nil, false
	}
	end := offset
	if _, identifierEnd, ok := identifierOffsetsAt(document.Text, offset); ok && identifierEnd > end {
		end = identifierEnd
	}
	const marker = "__gox_struct_field_completion"
	suffix := document.Text[end:]
	var tail scanner.Scanner
	tail.Init(token.NewFileSet().AddFile("", -1, len(suffix)), []byte(suffix), nil, 0)
	_, next, _ := tail.Scan()
	hasColon := next == token.COLON
	replacement := marker
	if !hasColon && next != token.COMMA {
		replacement += ","
	}
	repaired := document.Text[:start] + replacement + document.Text[end:]
	module, err := s.parseTransientModule(document, repaired)
	if err != nil || module == nil || module.Semantic == nil || module.Semantic.Info == nil {
		return nil, false
	}
	var literal *ast.CompositeLit
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, element := range lit.Elts {
			key := element
			if pair, ok := element.(*ast.KeyValueExpr); ok {
				key = pair.Key
			}
			if id, ok := key.(*ast.Ident); ok && id.Name == marker {
				literal = lit
				return false
			}
		}
		return true
	})
	if literal == nil {
		return nil, false
	}
	typ := module.Semantic.Info.TypeOf(literal)
	if typ == nil {
		return nil, false
	}
	structure, ok := typ.Underlying().(*types.Struct)
	if !ok {
		return nil, false
	}
	used := map[string]bool{}
	for _, element := range literal.Elts {
		if pair, ok := element.(*ast.KeyValueExpr); ok {
			if id, ok := pair.Key.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
	}
	from, err := document.Position(start)
	if err != nil {
		return nil, false
	}
	to, err := document.Position(end)
	if err != nil {
		return nil, false
	}
	var items []CompletionItem
	for i := 0; i < structure.NumFields(); i++ {
		field := structure.Field(i)
		if used[field.Name()] || !strings.HasPrefix(strings.ToLower(field.Name()), strings.ToLower(prefix)) || !semanticObjectAccessibleFrom(module, field) {
			continue
		}
		insertion := field.Name()
		format := 0
		if !hasColon {
			insertion += ": ${1}"
			if next != token.COMMA {
				insertion += ","
			}
			format = 2
		}
		items = append(items, CompletionItem{Label: field.Name(), Kind: 5, Detail: types.TypeString(field.Type(), semanticCompletionQualifier(module)), SortText: "0_" + field.Name(), InsertText: insertion, InsertTextFormat: format, TextEdit: &CompletionTextEdit{Range: Range{Start: from, End: to}, NewText: insertion}})
	}
	return items, true
}
