package gox

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"strconv"
	"strings"
)

var httpMethodNames = map[string]string{
	"GET":     "GET",
	"Get":     "GET",
	"POST":    "POST",
	"Post":    "POST",
	"PUT":     "PUT",
	"Put":     "PUT",
	"PATCH":   "PATCH",
	"Patch":   "PATCH",
	"DELETE":  "DELETE",
	"Delete":  "DELETE",
	"HEAD":    "HEAD",
	"Head":    "HEAD",
	"OPTIONS": "OPTIONS",
	"Options": "OPTIONS",
}

func AddBundleExports(bundle *Bundle) error {
	bundle.Decls = append(bundle.Decls, buildGoxAppVar())
	bundle.Decls = append(bundle.Decls, buildGoxPageVar(bundle))

	if bundleHasTests(bundle) {
		bundle.Decls = append(bundle.Decls, buildGoxTestsVar(bundle))
	}

	configExport, err := buildConfigExportFunc(bundle)
	if err != nil {
		return err
	}
	if configExport != nil {
		bundle.Decls = append(bundle.Decls, configExport)
	}

	if bundleHasHTTPHandlers(bundle) {
		bundle.Decls = append(bundle.Decls, buildHTTPHandlerFuncDecls(bundle)...)
		bundle.Decls = append(bundle.Decls, buildHTTPHandlersVar(bundle))
		bundle.Decls = append(bundle.Decls, buildHTTPContentTypesVar(bundle))
	}

	if bundleHasActions(bundle) {
		bundle.Decls = append(bundle.Decls, buildActionSpecDecls(bundle)...)

		actionsDecl, err := buildActionsVar(bundle)
		if err != nil {
			return err
		}

		bundle.Decls = append(bundle.Decls, actionsDecl)
	}

	if bundleHasWebSocketHandler(bundle) {
		bundle.Decls = append(bundle.Decls, buildWebSocketHandlerVar())
	}
	if channelName, ok := bundleChannelHandler(bundle); ok {
		bundle.Decls = append(bundle.Decls, buildChannelHandlerVar(channelName))
	}

	return nil
}

func bundleChannelHandler(bundle *Bundle) (string, bool) {
	if bundle == nil || bundle.Entry == nil {
		return "", false
	}
	for _, fn := range bundle.Entry.Funcs {
		if fn.Kind == FuncChannel {
			return fn.Name, true
		}
	}
	return "", false
}

func buildChannelHandlerVar(name string) ast.Decl {
	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{&ast.ValueSpec{
			Names:  []*ast.Ident{ast.NewIdent("GoxChannelHandler")},
			Type:   &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("ChannelHandler")},
			Values: []ast.Expr{&ast.SelectorExpr{X: ast.NewIdent("GoxPage"), Sel: ast.NewIdent(name)}},
		}},
	}
}

func bundleHasWebSocketHandler(bundle *Bundle) bool {
	if bundle == nil || bundle.Entry == nil {
		return false
	}
	for _, fn := range bundle.Entry.Funcs {
		if fn.Kind == FuncWebSocket && fn.Name == "Index" {
			return true
		}
	}
	return false
}

func buildWebSocketHandlerVar() ast.Decl {
	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{&ast.ValueSpec{
			Names:  []*ast.Ident{ast.NewIdent("GoxWebSocketHandler")},
			Type:   &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("WebSocketHandler")},
			Values: []ast.Expr{&ast.SelectorExpr{X: ast.NewIdent("GoxPage"), Sel: ast.NewIdent("Index")}},
		}},
	}
}

func actionParamImportPaths(bundle *Bundle) []string {
	return nil
}

func bundleHasTests(bundle *Bundle) bool {
	for _, fn := range bundle.Entry.Funcs {
		if isGoxTestFunc(fn) {
			return true
		}
	}
	return false
}

func isGoxTestFunc(fn FuncDecl) bool {
	if fn.Kind != FuncNormal {
		return false
	}
	if !strings.HasSuffix(fn.Name, "Test") {
		return false
	}
	return len(fn.Params) == 0
}

func buildGoxTestsVar(bundle *Bundle) ast.Decl {
	elts := []ast.Expr{}

	for _, fn := range bundle.Entry.Funcs {
		if !isGoxTestFunc(fn) {
			continue
		}

		elts = append(elts, &ast.KeyValueExpr{
			Key:   &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(fn.Name)},
			Value: &ast.SelectorExpr{X: ast.NewIdent("GoxPage"), Sel: ast.NewIdent(fn.Name)},
		})
	}

	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{ast.NewIdent("GoxTests")},
				Type: &ast.MapType{
					Key:   ast.NewIdent("string"),
					Value: &ast.FuncType{Params: &ast.FieldList{}},
				},
				Values: []ast.Expr{
					&ast.CompositeLit{
						Type: &ast.MapType{
							Key:   ast.NewIdent("string"),
							Value: &ast.FuncType{Params: &ast.FieldList{}},
						},
						Elts: elts,
					},
				},
			},
		},
	}
}

func bundleHasActionParams(bundle *Bundle) bool {
	for _, mod := range bundle.Modules {
		for _, fn := range mod.Funcs {
			if fn.Kind == FuncAction && len(actionUserParams(fn)) > 0 {
				return true
			}
		}
	}

	return false
}

func buildConfigExportFunc(bundle *Bundle) (ast.Decl, error) {
	var configFunc *FuncDecl

	for i := range bundle.Entry.Funcs {
		fn := &bundle.Entry.Funcs[i]
		if fn.Kind != FuncConfig {
			continue
		}

		if fn.Name != "SetGoxConfig" {
			return nil, newCompilerErrorWithHint("invalid-config-export-name", fmt.Sprintf("server.Config export in module %s must be named SetGoxConfig", bundle.Entry.Name), "Rename the server.Config export function to SetGoxConfig.")
		}

		if configFunc != nil {
			return nil, newCompilerErrorWithHint("duplicate-config-export", fmt.Sprintf("module %s declares multiple server.Config exports", bundle.Entry.Name), "Keep only one server.Config export in the entry module.")
		}

		configFunc = fn
	}

	if configFunc == nil {
		return nil, nil
	}

	return &ast.FuncDecl{
		Name: ast.NewIdent("SetGoxConfig"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{},
			Results: &ast.FieldList{
				List: []*ast.Field{
					{
						Type: &ast.SelectorExpr{
							X:   ast.NewIdent("server"),
							Sel: ast.NewIdent("Config"),
						},
					},
				},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{
						&ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X:   ast.NewIdent("GoxPage"),
								Sel: ast.NewIdent(configFunc.Name),
							},
						},
					},
				},
			},
		},
	}, nil
}

func bundleHasHTTPHandlers(bundle *Bundle) bool {
	for _, fn := range bundle.Entry.Funcs {
		if fn.Kind == FuncPage || fn.Kind == FuncHandler {
			return true
		}
	}

	return false
}

func bundleHasActions(bundle *Bundle) bool {
	for _, mod := range bundle.Modules {
		for _, fn := range mod.Funcs {
			if fn.Kind == FuncAction {
				return true
			}
		}
	}

	return false
}

func buildGoxPageVar(bundle *Bundle) ast.Decl {
	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{
					ast.NewIdent("GoxPage"),
				},
				Type: &ast.StarExpr{
					X: ast.NewIdent(GoxModuleName(bundle.Entry.Name)),
				},
				Values: []ast.Expr{
					&ast.SelectorExpr{
						X:   ast.NewIdent("GoxApp"),
						Sel: ast.NewIdent(bundle.Entry.Name),
					},
				},
			},
		},
	}
}

func buildHTTPHandlerFuncDecls(bundle *Bundle) []ast.Decl {
	decls := []ast.Decl{}
	for _, fn := range bundle.Entry.Funcs {
		if fn.Kind != FuncPage && fn.Kind != FuncHandler {
			continue
		}
		handlerName := httpHandlerExportName(fn)
		decls = append(decls, &ast.FuncDecl{
			Name: ast.NewIdent(handlerName),
			Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{
				{Names: []*ast.Ident{ast.NewIdent("ctx")}, Type: &ast.StarExpr{X: &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("Context")}}},
			}}},
			Body: &ast.BlockStmt{List: []ast.Stmt{
				&ast.ExprStmt{X: &ast.CallExpr{
					Fun:  &ast.SelectorExpr{X: ast.NewIdent("GoxPage"), Sel: ast.NewIdent(fn.Name)},
					Args: []ast.Expr{ast.NewIdent("ctx")},
				}},
			}},
		})
	}
	return decls
}

func httpHandlerExportName(fn FuncDecl) string {
	handlerName := fn.Name
	if methodName, ok := httpMethodNames[fn.Name]; ok {
		handlerName = methodName
	}
	return "GoxHTTPHandler" + sanitizeExportedIdentifierPart(handlerName)
}

func sanitizeExportedIdentifierPart(value string) string {
	if value == "" {
		return "Handler"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "Handler"
	}
	out := b.String()
	if out[0] >= '0' && out[0] <= '9' {
		return "Handler" + out
	}
	return out
}

func buildHTTPHandlersVar(bundle *Bundle) ast.Decl {
	elts := []ast.Expr{}

	for _, fn := range bundle.Entry.Funcs {
		if fn.Kind != FuncPage && fn.Kind != FuncHandler {
			continue
		}

		handlerName := fn.Name
		if methodName, ok := httpMethodNames[fn.Name]; ok {
			handlerName = methodName
		}

		elts = append(elts, &ast.KeyValueExpr{
			Key: &ast.BasicLit{
				Kind:  token.STRING,
				Value: strconv.Quote(handlerName),
			},
			Value: ast.NewIdent(httpHandlerExportName(fn)),
		})
	}

	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{
					ast.NewIdent("GoxHTTPHandlers"),
				},
				Type: goxHandlerMapType(),
				Values: []ast.Expr{
					&ast.CompositeLit{
						Type: goxHandlerMapType(),
						Elts: elts,
					},
				},
			},
		},
	}
}

func buildHTTPContentTypesVar(bundle *Bundle) ast.Decl {
	elts := []ast.Expr{}

	for _, fn := range bundle.Entry.Funcs {
		if fn.Kind != FuncPage && fn.Kind != FuncHandler {
			continue
		}

		handlerName := fn.Name
		if methodName, ok := httpMethodNames[fn.Name]; ok {
			handlerName = methodName
		}

		elts = append(elts, &ast.KeyValueExpr{
			Key: &ast.BasicLit{
				Kind:  token.STRING,
				Value: strconv.Quote(handlerName),
			},
			Value: &ast.BasicLit{
				Kind:  token.STRING,
				Value: strconv.Quote("text/html; charset=utf-8"),
			},
		})
	}

	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{
					ast.NewIdent("GoxHTTPContentTypes"),
				},
				Type: &ast.MapType{
					Key:   ast.NewIdent("string"),
					Value: ast.NewIdent("string"),
				},
				Values: []ast.Expr{
					&ast.CompositeLit{
						Type: &ast.MapType{
							Key:   ast.NewIdent("string"),
							Value: ast.NewIdent("string"),
						},
						Elts: elts,
					},
				},
			},
		},
	}
}

func dottedIdent(path string) ast.Expr {
	parts := strings.Split(path, ".")

	var expr ast.Expr = ast.NewIdent(parts[0])

	for i := 1; i < len(parts); i++ {
		expr = &ast.SelectorExpr{
			X:   expr,
			Sel: ast.NewIdent(parts[i]),
		}
	}

	return expr
}

func buildGoxAppVar() ast.Decl {
	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{
					ast.NewIdent("GoxApp"),
				},
				Type: &ast.StarExpr{
					X: ast.NewIdent("GoxBundle"),
				},
				Values: []ast.Expr{
					&ast.CallExpr{
						Fun: ast.NewIdent("NewGoxBundle"),
					},
				},
			},
		},
	}
}

func goxHandlerFuncType() *ast.FuncType {
	return &ast.FuncType{
		Params: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("ctx")},
					Type: &ast.StarExpr{
						X: &ast.SelectorExpr{
							X:   ast.NewIdent("gox"),
							Sel: ast.NewIdent("Context"),
						},
					},
				},
			},
		},
	}
}

func goxHandlerMapType() *ast.MapType {
	return &ast.MapType{
		Key:   ast.NewIdent("string"),
		Value: goxHandlerFuncType(),
	}
}

func buildActionsVar(bundle *Bundle) (ast.Decl, error) {
	elts := []ast.Expr{}
	seen := map[string]string{}

	for _, mod := range bundle.Modules {
		for _, fn := range mod.Funcs {
			if fn.Kind != FuncAction {
				continue
			}

			actionKey := fn.Name

			if owner, exists := seen[actionKey]; exists {
				return nil, newCompilerErrorWithHint(
					"duplicate-action",
					fmt.Sprintf("duplicate action %q declared in modules %s and %s", actionKey, owner, mod.Name),
					"Rename one of the actions so every action key is unique in the bundle.",
				)
			}

			seen[actionKey] = mod.Name

			elts = append(elts, &ast.KeyValueExpr{
				Key: &ast.BasicLit{
					Kind:  token.STRING,
					Value: strconv.Quote(actionKey),
				},
				Value: buildActionWrapper(mod, fn),
			})
		}
	}

	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{
					ast.NewIdent("GoxActions"),
				},
				Type: goxActionHandlerMapType(),
				Values: []ast.Expr{
					&ast.CompositeLit{
						Type: goxActionHandlerMapType(),
						Elts: elts,
					},
				},
			},
		},
	}, nil
}

func buildActionSpecDecls(bundle *Bundle) []ast.Decl {
	decls := []ast.Decl{}
	for _, mod := range bundle.Modules {
		for _, fn := range mod.Funcs {
			if fn.Kind != FuncAction {
				continue
			}
			decls = append(decls, buildActionSpecDecl(mod, fn))
		}
	}
	return decls
}

func buildActionSpecDecl(mod *Module, fn FuncDecl) ast.Decl {
	params := actionUserParams(fn)
	elts := []ast.Expr{}
	for _, param := range params {
		elts = append(elts, &ast.CompositeLit{
			Type: &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("ActionParamSpec")},
			Elts: []ast.Expr{
				&ast.KeyValueExpr{Key: ast.NewIdent("Name"), Value: strLit(param.Name)},
				&ast.KeyValueExpr{Key: ast.NewIdent("Type"), Value: actionParamSpecTypeExpr(mod, param)},
				&ast.KeyValueExpr{Key: ast.NewIdent("JSName"), Value: strLit(actionParamJSName(mod, param))},
			},
		})
	}
	return &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{
		Names: []*ast.Ident{ast.NewIdent(actionSpecVarName(fn.Name))},
		Values: []ast.Expr{&ast.CompositeLit{
			Type: &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("ActionSpec")},
			Elts: []ast.Expr{
				&ast.KeyValueExpr{Key: ast.NewIdent("Name"), Value: strLit(fn.Name)},
				&ast.KeyValueExpr{Key: ast.NewIdent("Params"), Value: &ast.CompositeLit{
					Type: &ast.ArrayType{Elt: &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("ActionParamSpec")}},
					Elts: elts,
				}},
			},
		}},
	}}}
}

func actionSpecVarName(action string) string {
	return "__goxActionSpec" + upperFirst(action)
}

func actionParamSpecTypeExpr(mod *Module, param flatParam) ast.Expr {
	t := resolveActionParamType(mod, exprSource(param.Type))
	name := "ActionJSON"
	switch {
	case isGoxFilesType(t):
		name = "ActionFiles"
	case isGoxFileType(t) || t == "[]byte":
		name = "ActionFile"
	case t == "string":
		name = "ActionString"
	case t == "bool":
		name = "ActionBoolean"
	case isNumericActionType(t):
		name = "ActionNumber"
	case t == "time.Time":
		name = "ActionDate"
	}
	return &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent(name)}
}

func actionParamJSName(mod *Module, param flatParam) string {
	t := resolveActionParamType(mod, exprSource(param.Type))
	switch {
	case isGoxFilesType(t):
		return "File[]"
	case isGoxFileType(t) || t == "[]byte":
		return "File"
	case t == "string":
		return "string"
	case t == "bool":
		return "boolean"
	case isNumericActionType(t):
		return "number"
	case t == "time.Time":
		return "Date"
	default:
		return actionJSONTypeName(t)
	}
}

func resolveActionParamType(mod *Module, t string) string {
	for _, typ := range mod.Types {
		if typ.Node == nil || typ.Node.Name == nil {
			continue
		}
		if t == typ.Node.Name.Name || t == typ.Name {
			return resolveActionParamType(mod, exprSource(typ.Node.Type))
		}
	}
	return t
}

func isNumericActionType(t string) bool {
	switch t {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "float32", "float64", "byte", "rune":
		return true
	}
	return false
}

func actionJSONTypeName(t string) string {
	if strings.HasPrefix(t, "[]") {
		return actionJSONTypeName(strings.TrimPrefix(t, "[]")) + "[]"
	}
	if i := strings.LastIndex(t, "."); i >= 0 && i < len(t)-1 {
		return t[i+1:]
	}
	if t == "" {
		return "object"
	}
	return t
}

func buildActionWrapper(mod *Module, fn FuncDecl) ast.Expr {
	userParams := actionUserParams(fn)

	body := []ast.Stmt{}
	destArgs := []ast.Expr{}
	for _, param := range userParams {
		body = append(body, &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{Names: []*ast.Ident{ast.NewIdent(actionParamLocalName(param))}, Type: param.Type}}}})
		destArgs = append(destArgs, &ast.UnaryExpr{Op: token.AND, X: ast.NewIdent(actionParamLocalName(param))})
	}

	validateArgs := []ast.Expr{ast.NewIdent("ctx"), &ast.UnaryExpr{Op: token.AND, X: ast.NewIdent(actionSpecVarName(fn.Name))}}
	validateArgs = append(validateArgs, destArgs...)
	body = append(body, &ast.IfStmt{
		Init: &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("ValidateActionParams")}, Args: validateArgs}}},
		Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(ast.NewIdent("err"))},
	})

	callArgs := []ast.Expr{}
	for _, param := range userParams {
		callArgs = append(callArgs, ast.NewIdent(actionParamLocalName(param)))
	}
	callArgs = append(callArgs, ast.NewIdent("ctx"))

	body = append(body,
		&ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent("BenchmarkHandlerSection")},
				Args: []ast.Expr{
					&ast.FuncLit{
						Type: &ast.FuncType{Params: &ast.FieldList{}},
						Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X:   &ast.SelectorExpr{X: ast.NewIdent("GoxApp"), Sel: ast.NewIdent(mod.Name)},
								Sel: ast.NewIdent(GoxActionName(mod.Name, fn.Name)),
							},
							Args: callArgs,
						}}}},
					},
				},
			},
		},
	)

	return &ast.FuncLit{Type: goxActionHandlerFuncType(), Body: &ast.BlockStmt{List: body}}
}

func actionParamLocalName(param flatParam) string {
	return "__goxActionParam" + upperFirst(param.Name)
}

func buildActionParamDecodeStmt(action string, index int, param flatParam) []ast.Stmt {
	typeName := exprSource(param.Type)
	local := actionParamLocalName(param)
	key := strconv.Itoa(index)
	name := param.Name
	if name == "" {
		name = key
	}

	if isGoxFileType(typeName) {
		return buildActionFileParam(action, key, name, local)
	}
	if typeName == "[]byte" {
		return buildActionBytesParam(action, key, name, local)
	}

	textName := local + "Text"
	stmts := []ast.Stmt{
		buildActionFormHasCheck(action, key, name, typeName),
		&ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(textName)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{formGetExpr(key)},
		},
	}

	stmts = append(stmts, buildActionTextConvert(action, name, typeName, local, textName, param.Type)...)
	return stmts
}

func buildActionFormHasCheck(action, key, name, typeName string) ast.Stmt {
	msg := fmt.Sprintf("Action %s argument %q expects %s, but it was not provided", action, name, typeName)
	return &ast.IfStmt{
		Cond: &ast.UnaryExpr{Op: token.NOT, X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: formExpr(), Sel: ast.NewIdent("Has")}, Args: []ast.Expr{strLit(key)}}},
		Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errorsNewExpr(msg))},
	}
}

func buildActionFileParam(action, key, name, local string) []ast.Stmt {
	msg := fmt.Sprintf("Action %s argument %q expects gox.File", action, name)
	return []ast.Stmt{
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(local)}, Tok: token.DEFINE, Rhs: []ast.Expr{filesGetExpr(key)}},
		&ast.IfStmt{
			Cond: &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent(local), Sel: ast.NewIdent("IsZero")}},
			Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errorsNewExpr(msg))},
		},
	}
}

func buildActionBytesParam(action, key, name, local string) []ast.Stmt {
	msg := fmt.Sprintf("Action %s argument %q expects []byte", action, name)
	return []ast.Stmt{
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("__goxActionFile" + upperFirst(name))}, Tok: token.DEFINE, Rhs: []ast.Expr{filesGetExpr(key)}},
		&ast.IfStmt{
			Cond: &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("__goxActionFile" + upperFirst(name)), Sel: ast.NewIdent("IsZero")}},
			Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errorsNewExpr(msg))},
		},
		&ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(local), ast.NewIdent("err")},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("io"), Sel: ast.NewIdent("ReadAll")}, Args: []ast.Expr{ast.NewIdent("__goxActionFile" + upperFirst(name))}}},
		},
		&ast.IfStmt{Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")}, Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(ast.NewIdent("err"))}},
	}
}

func buildActionTextConvert(action, name, typeName, local, textName string, typ ast.Expr) []ast.Stmt {
	switch typeName {
	case "string":
		return []ast.Stmt{&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(local)}, Tok: token.DEFINE, Rhs: []ast.Expr{ast.NewIdent(textName)}}}
	case "bool":
		return parseAssign(local, textName, "strconv", "ParseBool", actionParseError(action, name, typeName, textName))
	case "int":
		return parseIntAssign(local, textName, "int", "0", actionParseError(action, name, typeName, textName))
	case "int8":
		return parseIntAssign(local, textName, "int8", "8", actionParseError(action, name, typeName, textName))
	case "int16":
		return parseIntAssign(local, textName, "int16", "16", actionParseError(action, name, typeName, textName))
	case "int32", "rune":
		return parseIntAssign(local, textName, "int32", "32", actionParseError(action, name, typeName, textName))
	case "int64":
		return parseIntAssign(local, textName, "int64", "64", actionParseError(action, name, typeName, textName))
	case "uint":
		return parseUintAssign(local, textName, "uint", "0", actionParseError(action, name, typeName, textName))
	case "uint8", "byte":
		return parseUintAssign(local, textName, "uint8", "8", actionParseError(action, name, typeName, textName))
	case "uint16":
		return parseUintAssign(local, textName, "uint16", "16", actionParseError(action, name, typeName, textName))
	case "uint32":
		return parseUintAssign(local, textName, "uint32", "32", actionParseError(action, name, typeName, textName))
	case "uint64":
		return parseUintAssign(local, textName, "uint64", "64", actionParseError(action, name, typeName, textName))
	case "float32":
		return parseFloatAssign(local, textName, "float32", "32", actionParseError(action, name, typeName, textName))
	case "float64":
		return parseFloatAssign(local, textName, "float64", "64", actionParseError(action, name, typeName, textName))
	case "time.Time":
		return parseTimeAssign(local, textName, actionParseError(action, name, typeName, textName))
	case "time.Duration":
		return parseAssign(local, textName, "time", "ParseDuration", actionParseError(action, name, typeName, textName))
	case "big.Int":
		return parseBigIntAssign(local, textName, actionParseError(action, name, typeName, textName))
	case "gox.Email":
		return parseAssign(local, textName, "gox", "NewEmail", actionParseError(action, name, typeName, textName))
	}

	return jsonParamAssign(local, textName, typ, actionParseError(action, name, typeName, textName))
}

func actionParseError(action, name, typeName, textName string) ast.Expr {
	return &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("fmt"), Sel: ast.NewIdent("Errorf")}, Args: []ast.Expr{
		strLit(fmt.Sprintf("Action %s argument %q expects %s, received %%q", action, name, typeName)),
		ast.NewIdent(textName),
	}}
}

func parseAssign(local, textName, pkg, fn string, errExpr ast.Expr) []ast.Stmt {
	return []ast.Stmt{
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(local), ast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent(pkg), Sel: ast.NewIdent(fn)}, Args: []ast.Expr{ast.NewIdent(textName)}}}},
		&ast.IfStmt{Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")}, Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errExpr)}},
	}
}

func parseIntAssign(local, textName, castType, bitSize string, errExpr ast.Expr) []ast.Stmt {
	parsed := local + "Parsed"
	return []ast.Stmt{
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(parsed), ast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("strconv"), Sel: ast.NewIdent("ParseInt")}, Args: []ast.Expr{ast.NewIdent(textName), intLit("10"), intLit(bitSize)}}}},
		&ast.IfStmt{Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")}, Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errExpr)}},
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(local)}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent(castType), Args: []ast.Expr{ast.NewIdent(parsed)}}}},
	}
}

func parseUintAssign(local, textName, castType, bitSize string, errExpr ast.Expr) []ast.Stmt {
	parsed := local + "Parsed"
	return []ast.Stmt{
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(parsed), ast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("strconv"), Sel: ast.NewIdent("ParseUint")}, Args: []ast.Expr{ast.NewIdent(textName), intLit("10"), intLit(bitSize)}}}},
		&ast.IfStmt{Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")}, Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errExpr)}},
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(local)}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent(castType), Args: []ast.Expr{ast.NewIdent(parsed)}}}},
	}
}

func parseFloatAssign(local, textName, castType, bitSize string, errExpr ast.Expr) []ast.Stmt {
	parsed := local + "Parsed"
	return []ast.Stmt{
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(parsed), ast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("strconv"), Sel: ast.NewIdent("ParseFloat")}, Args: []ast.Expr{ast.NewIdent(textName), intLit(bitSize)}}}},
		&ast.IfStmt{Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")}, Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errExpr)}},
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(local)}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent(castType), Args: []ast.Expr{ast.NewIdent(parsed)}}}},
	}
}

func parseTimeAssign(local, textName string, errExpr ast.Expr) []ast.Stmt {
	return []ast.Stmt{
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(local), ast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("time"), Sel: ast.NewIdent("Parse")}, Args: []ast.Expr{&ast.SelectorExpr{X: ast.NewIdent("time"), Sel: ast.NewIdent("RFC3339Nano")}, ast.NewIdent(textName)}}}},
		&ast.IfStmt{Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")}, Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errExpr)}},
	}
}

func parseBigIntAssign(local, textName string, errExpr ast.Expr) []ast.Stmt {
	return []ast.Stmt{
		&ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{Names: []*ast.Ident{ast.NewIdent(local)}, Type: &ast.SelectorExpr{X: ast.NewIdent("big"), Sel: ast.NewIdent("Int")}}}}},
		&ast.IfStmt{Cond: &ast.UnaryExpr{Op: token.NOT, X: &ast.CallExpr{Fun: &ast.SelectorExpr{X: &ast.UnaryExpr{Op: token.AND, X: ast.NewIdent(local)}, Sel: ast.NewIdent("SetString")}, Args: []ast.Expr{ast.NewIdent(textName), intLit("10")}}}, Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errExpr)}},
	}
}

func jsonParamAssign(local, textName string, typ ast.Expr, errExpr ast.Expr) []ast.Stmt {
	return []ast.Stmt{
		&ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{Names: []*ast.Ident{ast.NewIdent(local)}, Type: typ}}}},
		&ast.IfStmt{Init: &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("json"), Sel: ast.NewIdent("Unmarshal")}, Args: []ast.Expr{&ast.CallExpr{Fun: byteSliceTypeExpr(), Args: []ast.Expr{ast.NewIdent(textName)}}, &ast.UnaryExpr{Op: token.AND, X: ast.NewIdent(local)}}}}}, Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")}, Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errExpr)}},
	}
}

func formExpr() ast.Expr {
	return &ast.SelectorExpr{X: &ast.SelectorExpr{X: ast.NewIdent("ctx"), Sel: ast.NewIdent("Request")}, Sel: ast.NewIdent("Form")}
}

func filesExpr() ast.Expr {
	return &ast.SelectorExpr{X: &ast.SelectorExpr{X: ast.NewIdent("ctx"), Sel: ast.NewIdent("Request")}, Sel: ast.NewIdent("Files")}
}

func formGetExpr(key string) ast.Expr {
	return &ast.CallExpr{Fun: &ast.SelectorExpr{X: formExpr(), Sel: ast.NewIdent("Get")}, Args: []ast.Expr{strLit(key)}}
}

func filesGetExpr(key string) ast.Expr {
	return &ast.CallExpr{Fun: &ast.SelectorExpr{X: filesExpr(), Sel: ast.NewIdent("Get")}, Args: []ast.Expr{strLit(key)}}
}

func strLit(value string) ast.Expr {
	return &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(value)}
}
func intLit(value string) ast.Expr { return &ast.BasicLit{Kind: token.INT, Value: value} }
func errorsNewExpr(message string) ast.Expr {
	return &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("errors"), Sel: ast.NewIdent("New")}, Args: []ast.Expr{strLit(message)}}
}
func byteSliceTypeExpr() ast.Expr { return &ast.ArrayType{Elt: ast.NewIdent("byte")} }

func isGoxFilesType(typeName string) bool {
	typeName = strings.TrimSpace(typeName)
	if !strings.HasPrefix(typeName, "[]") {
		return false
	}
	return isGoxFileType(strings.TrimSpace(strings.TrimPrefix(typeName, "[]")))
}

func isGoxFileType(typeName string) bool {
	typeName = strings.TrimSpace(typeName)
	if strings.HasPrefix(typeName, "[]") {
		return false
	}
	return typeName == "gox.File" || strings.HasSuffix(typeName, ".File")
}

func exprSource(expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, token.NewFileSet(), expr); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

func goxActionHandlerFuncType() *ast.FuncType {
	return &ast.FuncType{
		Params: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("ctx")},
					Type: &ast.StarExpr{
						X: &ast.SelectorExpr{
							X:   ast.NewIdent("gox"),
							Sel: ast.NewIdent("Context"),
						},
					},
				},
			},
		},
	}
}

func goxActionHandlerMapType() *ast.MapType {
	return &ast.MapType{
		Key: ast.NewIdent("string"),
		Value: &ast.SelectorExpr{
			X:   ast.NewIdent("gox"),
			Sel: ast.NewIdent("ActionHandler"),
		},
	}
}

type actionParam struct {
	Name string
	Type ast.Expr
}

func collectActionUserParams(fn *ast.FuncDecl) []flatParam {
	if fn == nil || fn.Type == nil || fn.Type.Params == nil {
		return nil
	}

	params := flattenFuncParams(fn.Type.Params)
	out := []flatParam{}

	for _, p := range params {
		if p.Name == "ctx" && isGoxContext(p.Type) {
			continue
		}
		out = append(out, p)
	}

	return out
}

func buildActionArgsLenCheck(expected int) ast.Stmt {
	errorExpr := &ast.CallExpr{
		Fun: &ast.SelectorExpr{X: ast.NewIdent("fmt"), Sel: ast.NewIdent("Errorf")},
		Args: []ast.Expr{
			&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("action expects " + strconv.Itoa(expected) + " args, got %d")},
			&ast.CallExpr{Fun: ast.NewIdent("len"), Args: []ast.Expr{ast.NewIdent("__goxActionArgs")}},
		},
	}

	return &ast.IfStmt{
		Cond: &ast.BinaryExpr{
			X:  &ast.CallExpr{Fun: ast.NewIdent("len"), Args: []ast.Expr{ast.NewIdent("__goxActionArgs")}},
			Op: token.NEQ,
			Y:  &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(expected)},
		},
		Body: &ast.BlockStmt{List: buildActionErrorResponseStmts(errorExpr)},
	}
}

func buildActionParamsDecode(paramsType string, userParams []flatParam) []ast.Stmt {
	stmts := []ast.Stmt{
		&ast.DeclStmt{
			Decl: &ast.GenDecl{
				Tok: token.VAR,
				Specs: []ast.Spec{
					&ast.ValueSpec{
						Names: []*ast.Ident{ast.NewIdent("args")},
						Type:  ast.NewIdent(paramsType),
					},
				},
			},
		},

		&ast.DeclStmt{
			Decl: &ast.GenDecl{
				Tok: token.VAR,
				Specs: []ast.Spec{
					&ast.ValueSpec{
						Names: []*ast.Ident{ast.NewIdent("__goxActionArgs")},
						Type: &ast.ArrayType{Elt: &ast.SelectorExpr{
							X:   ast.NewIdent("json"),
							Sel: ast.NewIdent("RawMessage"),
						}},
					},
				},
			},
		},

		&ast.IfStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent("err")},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					&ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X: &ast.SelectorExpr{
								X: &ast.SelectorExpr{
									X:   ast.NewIdent("ctx"),
									Sel: ast.NewIdent("Request"),
								},
								Sel: ast.NewIdent("Body"),
							},
							Sel: ast.NewIdent("ToJSON"),
						},
						Args: []ast.Expr{
							&ast.UnaryExpr{Op: token.AND, X: ast.NewIdent("__goxActionArgs")},
						},
					},
				},
			},
			Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")},
			Body: actionPayloadErrorReturn(),
		},

		buildActionArgsLenCheck(len(userParams)),
	}

	for i, param := range userParams {
		fieldName := upperFirst(param.Name)
		stmts = append(stmts, &ast.IfStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent("err")},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					&ast.CallExpr{
						Fun: &ast.SelectorExpr{X: ast.NewIdent("json"), Sel: ast.NewIdent("Unmarshal")},
						Args: []ast.Expr{
							&ast.IndexExpr{X: ast.NewIdent("__goxActionArgs"), Index: &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(i)}},
							&ast.UnaryExpr{Op: token.AND, X: &ast.SelectorExpr{X: ast.NewIdent("args"), Sel: ast.NewIdent(fieldName)}},
						},
					},
				},
			},
			Cond: &ast.BinaryExpr{X: ast.NewIdent("err"), Op: token.NEQ, Y: ast.NewIdent("nil")},
			Body: actionPayloadErrorReturn(),
		})
	}

	return stmts
}

func actionPayloadErrorReturn() *ast.BlockStmt {
	return &ast.BlockStmt{List: buildActionErrorResponseStmts(&ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: ast.NewIdent("errors"), Sel: ast.NewIdent("New")},
		Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("invalid action payload")}},
	})}
}

func buildActionMissingParamCheck(param flatParam) ast.Stmt {
	fieldName := upperFirst(param.Name)

	return &ast.IfStmt{
		Cond: &ast.BinaryExpr{
			X: &ast.SelectorExpr{
				X:   ast.NewIdent("args"),
				Sel: ast.NewIdent(fieldName),
			},
			Op: token.EQL,
			Y:  ast.NewIdent("nil"),
		},
		Body: actionPayloadErrorReturn(),
	}
}
