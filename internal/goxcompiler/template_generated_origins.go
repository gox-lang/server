package gox

import (
	"go/ast"
	"strconv"

	"github.com/gox-lang/server/internal/goxmap"
)

// Preserve the template construct's origin in the persisted map before later
// passes add/reset Go line directives. A whole return-template anchor is too
// broad for diagnostics on a nested component invocation.
func anchorTemplateGeneratedStatements(file *ast.File, origins *OriginTable, path, source string) {
	index := goxmap.NewLineIndex(source)
	ast.Inspect(file, func(node ast.Node) bool {
		var statements []ast.Stmt
		switch n := node.(type) {
		case *ast.BlockStmt:
			statements = n.List
		case *ast.CaseClause:
			statements = n.Body
		case *ast.CommClause:
			statements = n.Body
		default:
			return true
		}
		var origin *NodeOrigin
		for _, stmt := range statements {
			if isGoxLineMarkerStmt(stmt) {
				origin = nil
				call := stmt.(*ast.ExprStmt).X.(*ast.CallExpr)
				if len(call.Args) != 3 {
					continue
				}
				literal := func(i int) string {
					if lit, ok := call.Args[i].(*ast.BasicLit); ok {
						return lit.Value
					}
					return ""
				}
				uri, err := strconv.Unquote(literal(0))
				if err != nil || uri != path {
					continue
				}
				line, _ := strconv.Atoi(literal(1))
				column, _ := strconv.Atoi(literal(2))
				offset, err := index.ByteOffset(goxmap.BytePosition{Line: line - 1, Column: column - 1})
				if err != nil || offset >= len(source) {
					continue
				}
				origin = &NodeOrigin{Kind: goxmap.Anchor, Source: goxmap.SourceRange{URI: path, Range: goxmap.Range{Start: offset, End: offset + 1}}}
				continue
			}
			if origin == nil {
				continue
			}
			ast.Inspect(stmt, func(child ast.Node) bool {
				if child == nil {
					return false
				}
				if existing, ok := origins.Get(child); !ok || !existing.Exact() {
					origins.Set(child, *origin)
				}
				return true
			})
		}
		return true
	})
}
