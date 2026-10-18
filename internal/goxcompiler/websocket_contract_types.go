package gox

import (
	"go/ast"
	"go/token"
	"sort"
	"strings"
)

type webSocketContractTypeArgument struct{ Name, Type string }
type webSocketContractType struct {
	Scope      WebSocketContractScope
	Direction  WebSocketContractDirection
	Channel    string
	Event      string
	Arguments  []webSocketContractTypeArgument
	HasReply   bool
	ReplyTypes []string
}

func collectModuleWebSocketContractTypes(mod *Module) []webSocketContractType {
	if mod == nil || mod.File == nil {
		return nil
	}
	base := buildTemplateBaseTypeEnv(mod.File)
	out := make([]webSocketContractType, 0, len(mod.WebSocketContracts))
	for _, c := range mod.WebSocketContracts {
		env := webSocketEnvAtTokenPos(mod, c.Source, base)
		d := webSocketContractType{Scope: c.Scope, Direction: c.Direction, Channel: c.Channel, Event: c.Event, HasReply: c.HasReply}
		for _, arg := range c.Arguments {
			typ := "unknown"
			if arg.Type != nil {
				typ = clientJSONExprTSType(mod, arg.Type, map[string]bool{})
			} else if arg.Expr != nil {
				typ = clientTypeNameTSType(mod, webSocketExprType(arg.Expr, env))
			}
			d.Arguments = append(d.Arguments, webSocketContractTypeArgument{Name: arg.Name, Type: typ})
		}
		if c.HasReply {
			if c.Direction == WebSocketContractClientEmit {
				d.ReplyTypes = webSocketOnReplyTypes(mod, c.ReplyHandler, env)
			} else if c.ReplyTarget != nil {
				d.ReplyTypes = []string{clientTypeNameTSType(mod, webSocketAwaitTargetType(c.ReplyTarget, env))}
			}
			d.ReplyTypes = uniqueSortedTypes(d.ReplyTypes)
		}
		out = append(out, d)
	}
	return out
}

func webSocketExprType(expr ast.Expr, env *TemplateTypeEnv) string {
	typ := "any"
	if env != nil {
		typ = env.ExprType(expr)
	}
	if typ == "" || typ == "any" {
		typ = inferTemplateExprType(expr, env)
	}
	if typ == "" || typ == "any" {
		return "unknown"
	}
	return strings.TrimSpace(typ)
}
func webSocketAwaitTargetType(expr ast.Expr, env *TemplateTypeEnv) string {
	if unary, ok := expr.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		return webSocketExprType(unary.X, env)
	}
	return "unknown"
}
func webSocketEnvAtTokenPos(mod *Module, pos token.Pos, base *TemplateTypeEnv) *TemplateTypeEnv {
	for _, decl := range mod.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Body != nil && pos >= fn.Pos() && pos <= fn.End() {
			return templateTypeEnvForFunc(fn, base)
		}
	}
	return base
}
func webSocketOnReplyTypes(mod *Module, handler ast.Expr, enclosing *TemplateTypeEnv) []string {
	if handler == nil {
		return nil
	}
	var body *ast.BlockStmt
	env := enclosing
	switch h := handler.(type) {
	case *ast.FuncLit:
		body = h.Body
		env = enclosing.Clone()
		for _, p := range flattenFuncParams(h.Type.Params) {
			if p.Name != "" {
				env.Vars[p.Name] = normalizeTemplateType(p.Type)
			}
		}
		collectTemplateLocalTypes(body.List, env)
	case *ast.Ident:
		for _, decl := range mod.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == h.Name {
				body = fn.Body
				env = templateTypeEnvForFunc(fn, buildTemplateBaseTypeEnv(mod.File))
				break
			}
		}
	}
	if body == nil {
		return nil
	}
	seen := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok && lit.Body != body {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		typ := actionReturnExprType(ret.Results[0], env)
		if !isActionErrorType(typ) {
			seen[clientTypeNameTSType(mod, typ)] = true
		}
		return true
	})
	out := make([]string, 0, len(seen))
	for typ := range seen {
		out = append(out, typ)
	}
	sort.Strings(out)
	return out
}
func uniqueSortedTypes(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if v == "" {
			v = "unknown"
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
