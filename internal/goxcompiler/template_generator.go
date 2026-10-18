package gox

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

type TemplateGenContext struct {
	WriterName string
	LoopDepth  int

	StaticVarName  string
	StaticLiterals *[]string

	TypeEnv *TemplateTypeEnv

	Clients map[string]ClientDecl

	InjectActionsClient bool
	InjectClientRuntime bool
	ModulePath          string
}

func GenerateTemplateStmts(tpl *Template, ctx *TemplateGenContext) ([]ast.Stmt, error) {
	if tpl == nil || tpl.Root == nil {
		return nil, newTemplateErrorWithHint("empty-template", TemplatePosition{}, "empty template", "Add template content inside `return <> ... </>`.")
	}

	if ctx == nil {
		ctx = &TemplateGenContext{}
	}

	if ctx.WriterName == "" {
		ctx.WriterName = "ctx.Writer"
	}

	var g templateGenerator

	g.ctx = ctx
	if ctx.InjectActionsClient {
		g.importMapCSPPos = templateImportMapHead(tpl.Root)
	}

	if err := g.generateNode(tpl.Root); err != nil {
		return nil, err
	}

	g.flushText()
	g.resetLine()

	if g.importMapCSPPos != nil {
		// Header preparation must precede every template write, including
		// dynamic html/head attributes and content before the head element.
		var prelude templateGenerator
		prelude.emitLine(*g.importMapCSPPos)
		prelude.stmts = append(prelude.stmts, buildApplyImportMapCSPAST())
		prelude.resetLine()
		g.stmts = append(prelude.stmts, g.stmts...)
	}
	return g.stmts, nil
}

type templateGenerator struct {
	ctx             *TemplateGenContext
	stmts           []ast.Stmt
	buf             strings.Builder
	hasLineMarkers  bool
	clientScripts   map[string]struct{}
	importMapCSPPos *TemplatePosition
}

func (g *templateGenerator) emitLine(pos TemplatePosition) {
	if !pos.IsValid() {
		return
	}
	g.hasLineMarkers = true
	g.stmts = append(g.stmts, buildGoxLineMarkerAST(pos))
}

func (g *templateGenerator) resetLine() {
	if !g.hasLineMarkers {
		return
	}
	g.stmts = append(g.stmts, buildGoxLineResetMarkerAST())
}

func (g *templateGenerator) generateNode(node TemplateNode) error {
	switch n := node.(type) {
	case *FragmentNode:
		for _, child := range n.Children {
			if err := g.generateNode(child); err != nil {
				return err
			}
		}
		return nil

	case *ElementNode:
		return g.generateElement(n)

	case *TextNode:
		text := normalizeHTMLText(n.Value)
		if text == "" {
			return nil
		}

		g.writeText(text)
		return nil

	case *ExprNode:
		g.flushText()
		g.emitLine(n.Pos)

		if n.Mode == ExprRaw {
			g.stmts = append(
				g.stmts,
				buildCallWriterMethodAST(g.writeHelperForExpr(n.Expr, true), g.ctx.WriterName, n.Expr),
			)
		} else {
			g.stmts = append(
				g.stmts,
				buildCallWriterMethodAST(g.writeHelperForExpr(n.Expr, false), g.ctx.WriterName, n.Expr),
			)
		}

		return nil

	case *CommentNode:
		g.writeText("<!--" + n.Value + "-->")
		return nil

	case *DirectiveNode:
		return g.generateDirective(n)
		// return newTemplateErrorfWithHint("unsupported-directive", n.Pos, "This directive is not implemented yet.", "directive <:%s> is not implemented yet", n.Name)

	case *ComponentNode:
		return g.generateComponent(n)

	case *ClientNode:
		return g.generateClient(n)
	default:
		return newTemplateErrorfWithHint("unknown-template-node", templateNodePosition(node), "This template node is not supported by the generator.", "unknown template node %T", node)
	}
}

func (g *templateGenerator) writeHelperForExpr(expr ast.Expr, raw bool) string {
	if g.ctx == nil || g.ctx.TypeEnv == nil {
		if raw {
			return "GoxWriteRawAny"
		}
		return "GoxWriteTextAny"
	}

	typeName := g.ctx.TypeEnv.ExprType(expr)
	return runtimeWriteHelperName(typeName, raw)
}

func runtimeWriteHelperName(typeName string, raw bool) string {
	prefix := "GoxWriteText"
	if raw {
		prefix = "GoxWriteRaw"
	}

	switch typeName {
	case "string":
		return prefix + "String"
	case "[]byte":
		return prefix + "Bytes"
	case "bool":
		return prefix + "Bool"
	case "int":
		return prefix + "Int"
	case "int8":
		return prefix + "Int8"
	case "int16":
		return prefix + "Int16"
	case "int32", "rune":
		return prefix + "Int32"
	case "int64":
		return prefix + "Int64"
	case "uint":
		return prefix + "Uint"
	case "uint8", "byte":
		return prefix + "Uint8"
	case "uint16":
		return prefix + "Uint16"
	case "uint32":
		return prefix + "Uint32"
	case "uint64":
		return prefix + "Uint64"
	case "uintptr":
		return prefix + "Uintptr"
	case "float32":
		return prefix + "Float32"
	case "float64":
		return prefix + "Float64"
	case "*big.Int":
		return prefix + "BigInt"
	case "big.Int":
		return prefix + "BigIntValue"
	case "*big.Float":
		return prefix + "BigFloat"
	case "big.Float":
		return prefix + "BigFloatValue"
	case "*big.Rat":
		return prefix + "BigRat"
	case "big.Rat":
		return prefix + "BigRatValue"
	default:
		return prefix + "Any"
	}
}

func (g *templateGenerator) generateElement(n *ElementNode) error {
	g.writeText("<")
	g.writeText(n.Tag)

	for _, attr := range n.Attrs {
		if err := g.generateAttr(attr); err != nil {
			return err
		}
	}

	if n.SelfClosed {
		if isHTMLVoidTag(n.Tag) {
			g.writeText(">")
			return nil
		}

		g.writeText("/>")
		return nil
	}

	g.writeText(">")

	if strings.EqualFold(n.Tag, "head") && g.ctx != nil && g.ctx.InjectActionsClient {
		g.flushText()
		g.stmts = append(g.stmts, buildWriteActionsClientIfHTMLAST(g.ctx.WriterName))
	}

	if err := g.generateChildren(n.Children); err != nil {
		return err
	}

	g.trimTrailingSpace()

	if strings.EqualFold(n.Tag, "head") && g.ctx != nil && g.ctx.InjectClientRuntime {
		g.flushText()
		g.stmts = append(g.stmts, &ast.ExprStmt{X: &ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("WriteClientRuntime")},
			Args: []ast.Expr{ast.NewIdent("ctx")},
		}})
	}

	g.writeText("</")
	g.writeText(n.Tag)
	g.writeText(">")

	return nil
}

func (g *templateGenerator) generateChildren(children []TemplateNode) error {
	for i, child := range children {
		if text, ok := child.(*TextNode); ok {
			normalized := normalizeHTMLText(text.Value)

			if normalized == " " && isStructuralWhitespace(children, i) {
				continue
			}

			g.writeText(normalized)
			continue
		}

		if err := g.generateNode(child); err != nil {
			return err
		}
	}

	return nil
}

func (g *templateGenerator) generateAttr(attr TemplateAttr) error {
	switch attr.Kind {
	case AttrBoolean:
		g.writeText(" ")
		g.writeText(attr.Name)
		return nil

	case AttrStatic:
		g.writeText(" ")
		g.writeText(attr.Name)
		g.writeText(`="`)
		g.writeText(htmlEscapeAttr(attr.Value))
		g.writeText(`"`)
		return nil

	case AttrExpr:
		g.writeText(" ")
		g.writeText(attr.Name)
		g.writeText(`="`)
		g.flushText()
		g.emitLine(attr.Pos)

		g.stmts = append(
			g.stmts,
			buildCallWriterMethodAST(
				g.writeHelperForExpr(attr.Expr, false),
				g.ctx.WriterName,
				attr.Expr,
			),
		)

		g.writeText(`"`)
		return nil

	case AttrSpecial:
		if attr.Name != ":spread" {
			return newTemplateErrorfWithHint("unknown-special-attribute", attr.Pos, "Use :spread={attrs}, or remove the special attribute.", "unknown special attribute %q", attr.Name)
		}

		if attr.Expr == nil {
			return newTemplateErrorWithHint("spread-requires-expression", attr.Pos, ":spread requires expression value", "Use :spread={props}.")
		}

		g.flushText()
		g.emitLine(attr.Pos)
		g.stmts = append(
			g.stmts,
			buildCallWriterMethodAST(
				"GoxWriteSpreadAttrs",
				g.ctx.WriterName,
				attr.Expr,
			),
		)

		return nil
	}

	return newTemplateErrorfWithHint("unknown-attribute-kind", attr.Pos, "This attribute form is not supported by the template generator.", "unknown attribute kind %q", attr.Kind)
}

func (g *templateGenerator) writeText(s string) {
	if s == "" {
		return
	}
	g.buf.WriteString(s)
}

func (g *templateGenerator) flushText() {
	if g.buf.Len() == 0 {
		return
	}

	text := g.buf.String()
	if g.ctx.StaticVarName != "" && g.ctx.StaticLiterals != nil {
		index := len(*g.ctx.StaticLiterals)
		*g.ctx.StaticLiterals = append(*g.ctx.StaticLiterals, text)
		g.stmts = append(g.stmts, buildWriteStaticAST(g.ctx.WriterName, g.ctx.StaticVarName, index))
	} else {
		g.stmts = append(g.stmts, buildWriteStringAST(g.ctx.WriterName, text))
	}
	g.buf.Reset()
}

// =========================
// AST BUILDERS
// =========================

func buildGoxLineMarkerAST(pos TemplatePosition) ast.Stmt {
	return &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: ast.NewIdent("__goxLine"),
			Args: []ast.Expr{
				&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(pos.File)},
				&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(pos.Line)},
				&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(pos.Column)},
			},
		},
	}
}

func buildWriteActionsClientIfHTMLAST(writerName string) ast.Stmt {
	writeCall := func(arg ast.Expr) ast.Stmt {
		return &ast.ExprStmt{X: &ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: dottedIdent(writerName), Sel: ast.NewIdent("Write")},
			Args: []ast.Expr{arg},
		}}
	}

	lenGreaterThanZero := func(name string) ast.Expr {
		return &ast.BinaryExpr{
			X: &ast.CallExpr{
				Fun:  ast.NewIdent("len"),
				Args: []ast.Expr{ast.NewIdent(name)},
			},
			Op: token.GTR,
			Y:  &ast.BasicLit{Kind: token.INT, Value: "0"},
		}
	}

	return &ast.IfStmt{
		Cond: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X: &ast.SelectorExpr{
					X:   &ast.SelectorExpr{X: ast.NewIdent("ctx"), Sel: ast.NewIdent("Response")},
					Sel: ast.NewIdent("Headers"),
				},
				Sel: ast.NewIdent("Contains"),
			},
			Args: []ast.Expr{
				&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("Content-Type")},
				&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("text/html")},
			},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.IfStmt{
				Cond: lenGreaterThanZero(goxActionsClientScriptVar),
				Body: &ast.BlockStmt{List: []ast.Stmt{
					writeCall(ast.NewIdent(goxActionsClientScriptVar)),
				}},
			},
			&ast.ExprStmt{X: &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X: &ast.CallExpr{Fun: &ast.SelectorExpr{
						X: &ast.SelectorExpr{
							X:   &ast.SelectorExpr{X: ast.NewIdent("ctx"), Sel: ast.NewIdent("Response")},
							Sel: ast.NewIdent("ImportMap"),
						},
						Sel: ast.NewIdent("Runtime"),
					}},
					Sel: ast.NewIdent("WriteCompiled"),
				},
				Args: []ast.Expr{
					dottedIdent(writerName),
					ast.NewIdent(goxStaticImportMapEntriesVar),
					ast.NewIdent(goxStaticImportMapScriptVar),
					ast.NewIdent(goxStaticImportMapHashVar),
				},
			}},
		}},
	}
}

func buildApplyImportMapCSPAST() ast.Stmt {
	importMap := &ast.SelectorExpr{X: &ast.SelectorExpr{X: ast.NewIdent("ctx"), Sel: ast.NewIdent("Response")}, Sel: ast.NewIdent("ImportMap")}
	runtimeImportMap := &ast.CallExpr{Fun: &ast.SelectorExpr{X: importMap, Sel: ast.NewIdent("Runtime")}}
	return &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: runtimeImportMap, Sel: ast.NewIdent("ApplyCSP")}, Args: []ast.Expr{ast.NewIdent(goxStaticImportMapHashVar)}}}
}

func buildGoxLineResetMarkerAST() ast.Stmt {
	return &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: ast.NewIdent("__goxLineReset"),
		},
	}
}

func buildWriteStringAST(writerName string, value string) ast.Stmt {
	return &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   dottedIdent(writerName),
				Sel: ast.NewIdent("Write"),
			},
			Args: []ast.Expr{
				byteSliceConversion(&ast.BasicLit{
					Kind:  token.STRING,
					Value: strconv.Quote(value),
				}),
			},
		},
	}
}

func buildWriteStaticAST(writerName string, staticVarName string, index int) ast.Stmt {
	return &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   dottedIdent(writerName),
				Sel: ast.NewIdent("Write"),
			},
			Args: []ast.Expr{
				&ast.IndexExpr{
					X: ast.NewIdent(staticVarName),
					Index: &ast.BasicLit{
						Kind:  token.INT,
						Value: strconv.Itoa(index),
					},
				},
			},
		},
	}
}

func buildWriteExprAST(writerName string, expr ast.Expr) ast.Stmt {
	return &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   dottedIdent(writerName),
				Sel: ast.NewIdent("Write"),
			},
			Args: []ast.Expr{
				byteSliceConversion(expr),
			},
		},
	}
}

func byteSliceConversion(expr ast.Expr) ast.Expr {
	return &ast.CallExpr{
		Fun: &ast.ArrayType{
			Elt: ast.NewIdent("byte"),
		},
		Args: []ast.Expr{expr},
	}
}

func (g *templateGenerator) trimTrailingSpace() {
	s := g.buf.String()
	if s == "" {
		return
	}

	s = strings.TrimRight(s, " \t\n\r")

	g.buf.Reset()
	g.buf.WriteString(s)
}

// =========================
// ESCAPE HELPERS
// =========================

func htmlEscapeAttr(s string) string {
	s = strings.ReplaceAll(s, `&`, `&amp;`)
	s = strings.ReplaceAll(s, `"`, `&quot;`)
	s = strings.ReplaceAll(s, `<`, `&lt;`)
	s = strings.ReplaceAll(s, `>`, `&gt;`)
	return s
}

func buildCallWriterMethodAST(helperName string, writerName string, expr ast.Expr) ast.Stmt {
	return &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   dottedIdent(writerName),
				Sel: ast.NewIdent(runtimeWriteMethodName(helperName)),
			},
			Args: []ast.Expr{expr},
		},
	}
}

func runtimeWriteMethodName(helperName string) string {
	if strings.HasPrefix(helperName, "Gox") {
		return strings.TrimPrefix(helperName, "Gox")
	}
	return helperName
}

func normalizeHTMLText(s string) string {
	if s == "" {
		return ""
	}

	return collapseWhitespace(s)
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	inSpace := false

	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
		default:
			b.WriteRune(r)
			inSpace = false
		}
	}

	return b.String()
}

func (g *templateGenerator) generateDirective(n *DirectiveNode) error {
	switch n.Name {
	case "if":
		return g.generateIfDirective(n)
	case "if-group":
		return g.generateIfGroupDirective(n)
	case "switch":
		return g.generateSwitchDirective(n)
	case "for":
		return g.generateForDirective(n)
	case "break":
		return g.generateBreakDirective(n)
	case "continue":
		return g.generateContinueDirective(n)
	case "repeat":
		return g.generateRepeatDirective(n)
	default:
		return newTemplateErrorfWithHint("unsupported-directive", n.Pos, "This directive is not implemented yet.", "directive <:%s> is not implemented yet", n.Name)
	}
}

func isStructuralWhitespace(children []TemplateNode, index int) bool {
	prev := previousMeaningfulNode(children, index)
	next := nextMeaningfulNode(children, index)

	if prev == nil || next == nil {
		return true
	}

	return isStructuralNode(prev) && isStructuralNode(next)
}

func previousMeaningfulNode(children []TemplateNode, index int) TemplateNode {
	for i := index - 1; i >= 0; i-- {
		if isEmptyTextNode(children[i]) {
			continue
		}

		return children[i]
	}

	return nil
}

func nextMeaningfulNode(children []TemplateNode, index int) TemplateNode {
	for i := index + 1; i < len(children); i++ {
		if isEmptyTextNode(children[i]) {
			continue
		}

		return children[i]
	}

	return nil
}

func isEmptyTextNode(node TemplateNode) bool {
	text, ok := node.(*TextNode)
	if !ok {
		return false
	}

	return strings.TrimSpace(text.Value) == ""
}

func isIgnorableDirectiveChild(node TemplateNode) bool {
	if isEmptyTextNode(node) {
		return true
	}

	_, ok := node.(*CommentNode)
	return ok
}

func hasMeaningfulDirectiveChildren(children []TemplateNode) bool {
	for _, child := range children {
		if !isIgnorableDirectiveChild(child) {
			return true
		}
	}

	return false
}

func isStructuralNode(node TemplateNode) bool {
	switch node.(type) {
	case *ElementNode, *DirectiveNode, *ComponentNode, *CommentNode:
		return true
	default:
		return false
	}
}

// Only these nodes inherit page injection. Component/client children are
// independent callbacks and do not inject the document import map.
func templateImportMapHead(node TemplateNode) *TemplatePosition {
	var children []TemplateNode
	switch n := node.(type) {
	case *ElementNode:
		if strings.EqualFold(n.Tag, "head") && !n.SelfClosed {
			pos := n.Pos
			return &pos
		}
		children = n.Children
	case *FragmentNode:
		children = n.Children
	case *DirectiveNode:
		children = n.Children
	}
	for _, child := range children {
		if pos := templateImportMapHead(child); pos != nil {
			return pos
		}
	}
	return nil
}
