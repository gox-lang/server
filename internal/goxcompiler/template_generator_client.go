package gox

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"html"
	"strconv"
	"strings"

	"github.com/gox-lang/server/internal/clientbuilder"
)

func (g *templateGenerator) generateClient(n *ClientNode) error {
	client, ok := g.clientDeclFor(n.Name)
	if !ok {
		return newTemplateErrorfWithHint("unknown-client-component", n.Pos, "Declare this client component before using it in a template.", "unknown client component <@%s>", n.Name)
	}
	if err := validateClientAttrs(n); err != nil {
		return err
	}
	g.flushText()
	g.emitLine(n.Pos)
	openCallback, err := g.buildClientOpenCallback(n)
	if err != nil {
		return err
	}
	childrenCallback, err := g.buildClientChildrenCallback(n)
	if err != nil {
		return err
	}
	clientKey := strings.TrimSpace(client.Key)
	if clientKey == "" {
		clientKey = ClientDeclarationKey(g.ctx.ModulePath, client.Name, strings.ToLower(string(client.Framework)), client.Entry, client.Dev)
	}
	g.stmts = append(g.stmts, &ast.ExprStmt{X: &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("WriteClientIsland")},
		Args: []ast.Expr{ast.NewIdent("ctx"), &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(clientKey)}, openCallback, childrenCallback},
	}})
	return nil
}

func (g *templateGenerator) buildClientOpenCallback(n *ClientNode) (ast.Expr, error) {
	reserved := make([]TemplateAttr, 0, len(n.Attrs))
	props := make([]TemplateAttr, 0, len(n.Attrs))

	for _, attr := range n.Attrs {
		switch attr.Name {
		case "hydrate", "transition", "isolate", "adapt":
			reserved = append(reserved, attr)
		default:
			props = append(props, attr)
		}
	}

	if len(reserved) == 0 && len(props) == 0 {
		return ast.NewIdent("nil"), nil
	}

	sub := &templateGenerator{ctx: &TemplateGenContext{
		WriterName:     "ctx.Writer",
		StaticVarName:  g.ctx.StaticVarName,
		StaticLiterals: g.ctx.StaticLiterals,
		TypeEnv:        g.ctx.TypeEnv,
		Clients:        g.ctx.Clients,
	}}

	for _, attr := range reserved {
		if err := sub.generateClientControlAttr(attr); err != nil {
			return nil, err
		}
	}
	sub.flushText()

	if len(props) > 0 {
		sub.stmts = append(sub.stmts, buildWriteClientPropsAST("ctx.Writer", props))
	}

	return buildClientCallbackAST(sub.stmts), nil
}

func (g *templateGenerator) buildClientChildrenCallback(n *ClientNode) (ast.Expr, error) {
	if n.SelfClosed || len(n.Children) == 0 {
		return ast.NewIdent("nil"), nil
	}

	sub := &templateGenerator{ctx: &TemplateGenContext{
		WriterName:     "ctx.Writer",
		StaticVarName:  g.ctx.StaticVarName,
		StaticLiterals: g.ctx.StaticLiterals,
		TypeEnv:        g.ctx.TypeEnv,
		Clients:        g.ctx.Clients,
	}}
	if err := sub.generateChildren(n.Children); err != nil {
		return nil, err
	}
	sub.trimTrailingSpace()
	sub.flushText()
	return buildClientCallbackAST(sub.stmts), nil
}

func (g *templateGenerator) generateClientControlAttr(attr TemplateAttr) error {
	switch attr.Name {
	case "hydrate":
		attr.Name = "data-hydrate"
	case "adapt":
		attr.Name = "data-adapt"
	case "transition":
		attr.Name = "data-transition"
	case "isolate":
		switch attr.Kind {
		case AttrBoolean:
			g.writeText(` data-isolate="true"`)
			g.flushText()
			g.stmts = append(g.stmts, buildWriteClientIslandDataAssetsAST())
			return nil
		case AttrStatic:
			if attr.Value == "false" {
				g.writeText(` data-isolate="false"`)
				return nil
			}
			g.writeText(` data-isolate="true"`)
			g.flushText()
			g.stmts = append(g.stmts, buildWriteClientIslandDataAssetsAST())
			return nil
		case AttrExpr:
			g.flushText()
			g.emitLine(attr.Pos)
			g.stmts = append(g.stmts, &ast.IfStmt{
				Cond: attr.Expr,
				Body: &ast.BlockStmt{List: []ast.Stmt{
					buildCallWriterMethodAST("GoxWriteRawString", g.ctx.WriterName, &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(` data-isolate="true"`)}),
					buildWriteClientIslandDataAssetsAST(),
				}},
			})
			return nil
		}
	}
	return g.generateAttr(attr)
}

func buildWriteClientIslandDataAssetsAST() ast.Stmt {
	return &ast.ExprStmt{X: &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("WriteClientIslandDataAssets")},
		Args: []ast.Expr{ast.NewIdent("ctx")},
	}}
}

func buildClientCallbackAST(stmts []ast.Stmt) ast.Expr {
	return &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{
			Names: []*ast.Ident{ast.NewIdent("ctx")},
			Type: &ast.StarExpr{X: &ast.SelectorExpr{
				X:   ast.NewIdent("gox"),
				Sel: ast.NewIdent("Context"),
			}},
		}}}},
		Body: &ast.BlockStmt{List: stmts},
	}
}

func buildWriteClientPropsAST(writerName string, attrs []TemplateAttr) ast.Stmt {
	allStatic := true
	values := make(map[string]any, len(attrs))
	for _, attr := range attrs {
		if attr.Kind == AttrSpecial && attr.Name == ":spread" {
			allStatic = false
			break
		}
		switch attr.Kind {
		case AttrBoolean:
			values[attr.Name] = true
		case AttrStatic:
			values[attr.Name] = attr.Value
		default:
			allStatic = false
		}
	}
	if allStatic {
		encoded, _ := json.Marshal(values)
		value := ` data-props="` + html.EscapeString(string(encoded)) + `"`
		return &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: dottedIdent(writerName), Sel: ast.NewIdent("WriteRawString")}, Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(value)}}}}
	}
	elts := make([]ast.Expr, 0, len(attrs))
	spreads := make([]ast.Expr, 0)
	for _, attr := range attrs {
		if attr.Kind == AttrSpecial && attr.Name == ":spread" {
			if attr.Expr != nil {
				spreads = append(spreads, attr.Expr)
			}
			continue
		}
		var value ast.Expr
		switch attr.Kind {
		case AttrBoolean:
			value = ast.NewIdent("true")
		case AttrStatic:
			value = &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(attr.Value)}
		case AttrExpr:
			value = attr.Expr
		default:
			continue
		}
		elts = append(elts, &ast.KeyValueExpr{Key: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(attr.Name)}, Value: value})
	}
	args := []ast.Expr{dottedIdent(writerName), &ast.CompositeLit{Type: &ast.MapType{Key: ast.NewIdent("string"), Value: ast.NewIdent("any")}, Elts: elts}}
	args = append(args, spreads...)
	return &ast.ExprStmt{X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("WriteClientPropsAttribute")}, Args: args}}
}

func clientMethodName(name string) string {
	return "__goxClient" + sanitizeExportedIdentifierPart(name)
}

func clientScriptURL(client ClientDecl) string {
	framework := strings.ToLower(string(client.Framework))
	mode := "prod"
	if client.Dev {
		mode = "dev"
	}
	path := client.EntryPath
	if strings.TrimSpace(path) == "" {
		path = client.Entry
	}
	return clientbuilder.PublicEntryURL(client.Name, framework, path, mode)
}

func (g *templateGenerator) generateTemporaryClientScript(client ClientDecl) {
	framework := strings.ToLower(string(client.Framework))
	mode := "prod"
	if client.Dev {
		mode = "dev"
	}
	path := client.EntryPath
	if strings.TrimSpace(path) == "" {
		path = client.Entry
	}
	url := clientbuilder.PublicEntryURL(client.Name, framework, path, mode)
	if strings.TrimSpace(url) == "" {
		return
	}
	if g.clientScripts == nil {
		g.clientScripts = make(map[string]struct{})
	}
	if _, ok := g.clientScripts[url]; ok {
		return
	}
	g.clientScripts[url] = struct{}{}
	g.writeText(`<script type="module" src="` + url + `"></script>`)
}

func (g *templateGenerator) clientDeclFor(name string) (ClientDecl, bool) {
	if g.ctx == nil || g.ctx.Clients == nil {
		return ClientDecl{}, false
	}
	client, ok := g.ctx.Clients[name]
	return client, ok
}

func validateClientAttrs(n *ClientNode) error {
	if err := validateAdaptAttrs(n.Attrs, true); err != nil {
		return err
	}
	for _, attr := range n.Attrs {
		switch attr.Name {
		case "data-props", "data-script", "data-assets", "data-hydrate", "data-transition", "data-isolate", "data-adapt":
			return newTemplateErrorfWithHint(
				"reserved-client-attribute",
				attr.Pos,
				"Use hydrate, transition, adapt, and isolate for GoX controls. Internal data-* attributes are generated by GoX; every other attribute, including props, is collected automatically into data-props.",
				"client component <@%s> cannot declare reserved attribute %q",
				n.Name, attr.Name,
			)
		case "hydrate":
			if attr.Kind == AttrExpr {
				continue
			}
			if attr.Kind != AttrStatic {
				return newTemplateErrorfWithHint(
					"invalid-client-hydrate",
					attr.Pos,
					"Use load, idle, visible, hover, interaction, or a dynamic expression.",
					"client component <@%s> hydrate must be a string or expression",
					n.Name,
				)
			}
			if !isValidHydrateMode(attr.Value) {
				return newTemplateErrorfWithHint(
					"invalid-client-hydrate",
					attr.Pos,
					"Allowed hydrate modes are: load, idle, visible, hover, interaction.",
					"invalid hydrate mode %q on client component <@%s>",
					attr.Value,
					n.Name,
				)
			}

		case "isolate":
			if attr.Kind == AttrSpecial {
				return newTemplateErrorfWithHint(
					"invalid-client-isolate",
					attr.Pos,
					"Use isolate, isolate=\"true\", isolate=\"false\", or isolate={{expr}}.",
					"invalid isolate attribute on client component <@%s>",
					n.Name,
				)
			}
			if attr.Kind == AttrStatic && attr.Value != "true" && attr.Value != "false" {
				return newTemplateErrorfWithHint(
					"invalid-client-isolate",
					attr.Pos,
					"Use isolate=\"true\" or isolate=\"false\".",
					"client component <@%s> isolate must be boolean",
					n.Name,
				)
			}
		}
	}
	return nil
}

func isValidHydrateMode(mode string) bool {
	switch mode {
	case "load", "idle", "visible", "hover", "interaction":
		return true
	default:
		return false
	}
}

func clientHTMLTag(client ClientDecl) string {
	framework := strings.ToLower(string(client.Framework))
	return framework + "-" + kebabIdent(client.Name)
}

func kebabIdent(name string) string {
	if name == "" {
		return name
	}
	var b strings.Builder
	var prevDash bool
	for i, r := range name {
		if r == '_' || r == '-' || r == '.' {
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
			continue
		}
		if r >= 'A' && r <= 'Z' {
			if i > 0 && !prevDash {
				b.WriteByte('-')
			}
			b.WriteRune(r + ('a' - 'A'))
			prevDash = false
			continue
		}
		b.WriteRune(r)
		prevDash = false
	}
	return strings.Trim(b.String(), "-")
}
