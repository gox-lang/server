package gox

import (
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

const (
	actionResponseHeaderName = "Action-Response"
	actionTypeHeaderName     = "Action-Type"
	contentTypeHeaderName    = "Content-Type"
	contentTypeTextPlain     = "text/plain; charset=utf-8"
	contentTypeTextHTML      = "text/html; charset=utf-8"
	contentTypeJSON          = "application/json; charset=utf-8"
	contentTypeOctetStream   = "application/octet-stream"
)

func actionSetHeaderStmt(key string, value string) ast.Stmt {
	return &ast.ExprStmt{X: &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X: &ast.SelectorExpr{
				X:   &ast.SelectorExpr{X: ast.NewIdent("ctx"), Sel: ast.NewIdent("Response")},
				Sel: ast.NewIdent("Headers"),
			},
			Sel: ast.NewIdent("Set"),
		},
		Args: []ast.Expr{
			&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(key)},
			&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(value)},
		},
	}}
}

func actionSetSuccessHeaderStmt() ast.Stmt {
	return actionSetHeaderStmt(actionResponseHeaderName, "success")
}

func actionSetErrorHeaderStmt() ast.Stmt {
	return actionSetHeaderStmt(actionResponseHeaderName, "error")
}

func actionTemplateHeaderStmts() []ast.Stmt {
	return []ast.Stmt{
		actionSetSuccessHeaderStmt(),
		actionSetHeaderStmt(actionTypeHeaderName, "html"),
		actionSetHeaderStmt(contentTypeHeaderName, contentTypeTextHTML),
	}
}

func buildActionErrorWriteStmts(expr ast.Expr) []ast.Stmt {
	return []ast.Stmt{
		actionSetErrorHeaderStmt(),
		actionSetHeaderStmt(actionTypeHeaderName, "text"),
		actionSetHeaderStmt(contentTypeHeaderName, contentTypeTextPlain),
		&ast.ExprStmt{X: &ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: &ast.SelectorExpr{X: ast.NewIdent("ctx"), Sel: ast.NewIdent("Writer")}, Sel: ast.NewIdent("WriteError")},
			Args: []ast.Expr{expr},
		}},
	}
}

func buildActionErrorResponseStmts(expr ast.Expr) []ast.Stmt {
	stmts := buildActionErrorWriteStmts(expr)
	return append(stmts, &ast.ReturnStmt{})
}

func buildActionSuccessReturnStmts(expr ast.Expr, typeName string) []ast.Stmt {
	typeName = strings.TrimSpace(typeName)
	out := []ast.Stmt{actionSetSuccessHeaderStmt()}
	contentType, methodName := actionContentTypeAndWriteMethod(typeName)
	actionType := actionTypeForType(typeName)
	dynamicInteger := expr != nil && isActionDynamicIntegerType(typeName)

	writeExpr := expr
	if dynamicInteger {
		valueName := "__goxActionResult"
		// Integer constants are frequently untyped. Give the generated temporary
		// an explicit type so values such as math.MaxUint64 are not defaulted to
		// int before they reach the specialized writer.
		typedExpr := &ast.CallExpr{Fun: ast.NewIdent(typeName), Args: []ast.Expr{expr}}
		out = append(out, &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(valueName)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{typedExpr},
		})
		writeExpr = ast.NewIdent(valueName)
		out = append(out, actionSetHeaderExprStmt(actionTypeHeaderName, actionIntegerTypeExpr(writeExpr, typeName)))
	} else if actionType != "" {
		out = append(out, actionSetHeaderStmt(actionTypeHeaderName, actionType))
	}

	if contentType != "" {
		out = append(out, actionSetHeaderStmt(contentTypeHeaderName, contentType))
	}
	if methodName != "" && writeExpr != nil {
		if isActionHTMLType(typeName) {
			writeExpr = &ast.CallExpr{Fun: ast.NewIdent("string"), Args: []ast.Expr{writeExpr}}
		}
		call := &ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: &ast.SelectorExpr{X: ast.NewIdent("ctx"), Sel: ast.NewIdent("Writer")}, Sel: ast.NewIdent(methodName)},
			Args: []ast.Expr{writeExpr},
		}
		if methodName == "WriteByte" {
			out = append(out, &ast.IfStmt{
				Init: &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("__goxWriteErr")}, Tok: token.DEFINE, Rhs: []ast.Expr{call}},
				Cond: &ast.BinaryExpr{X: ast.NewIdent("__goxWriteErr"), Op: token.NEQ, Y: ast.NewIdent("nil")},
				Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{ast.NewIdent("__goxWriteErr")}}}}},
			})
		} else {
			out = append(out, &ast.ExprStmt{X: call})
		}
	}
	out = append(out, &ast.ReturnStmt{})
	if dynamicInteger {
		return []ast.Stmt{&ast.BlockStmt{List: out}}
	}
	return out
}

func actionSetHeaderExprStmt(key string, value ast.Expr) ast.Stmt {
	return &ast.ExprStmt{X: &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X: &ast.SelectorExpr{
				X:   &ast.SelectorExpr{X: ast.NewIdent("ctx"), Sel: ast.NewIdent("Response")},
				Sel: ast.NewIdent("Headers"),
			},
			Sel: ast.NewIdent("Set"),
		},
		Args: []ast.Expr{
			&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(key)},
			value,
		},
	}}
}

func actionTypeForType(typeName string) string {
	typeName = strings.TrimSpace(typeName)
	if isActionHTMLType(typeName) {
		return "html"
	}
	if isTemplateStreamType(typeName) {
		return "stream"
	}
	if isTemplateFileType(typeName) {
		return "file"
	}
	if isTemplateFilesType(typeName) {
		return "files"
	}
	switch typeName {
	case "", "nil":
		return "null"
	case "[]byte":
		return "binary"
	case "byte", "int", "int8", "int16", "int32", "rune", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return "number"
	case "string":
		return "text"
	case "bool":
		return "boolean"
	case "float32", "float64":
		return "number"
	case "*big.Float", "big.Float", "*big.Rat", "big.Rat":
		return "text"
	case "*big.Int", "big.Int":
		return "bigint"
	default:
		return "json"
	}
}

func isActionDynamicIntegerType(typeName string) bool {
	switch strings.TrimSpace(typeName) {
	case "byte", "int", "int8", "int16", "int32", "rune", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return true
	default:
		return false
	}
}

func actionIntegerTypeExpr(value ast.Expr, typeName string) ast.Expr {
	unsigned := false
	switch strings.TrimSpace(typeName) {
	case "byte", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		unsigned = true
	}
	method := "ActionSignedIntegerType"
	cast := "int64"
	if unsigned {
		method = "ActionUnsignedIntegerType"
		cast = "uint64"
	}
	return &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent(method)},
		Args: []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent(cast), Args: []ast.Expr{value}}},
	}
}

func actionContentTypeAndWriteMethod(typeName string) (string, string) {
	typeName = strings.TrimSpace(typeName)
	if isActionHTMLType(typeName) {
		return contentTypeTextHTML, "WriteRawString"
	}
	if isTemplateStreamType(typeName) {
		return contentTypeOctetStream, "WriteStream"
	}
	if isTemplateFileType(typeName) || isTemplateFilesType(typeName) {
		return "", ""
	}
	switch typeName {
	case "", "nil":
		return "", ""
	case "[]byte":
		return contentTypeOctetStream, "WriteBytes"
	case "byte":
		return contentTypeTextPlain, "WriteByte"
	case "string":
		return contentTypeTextPlain, "WriteTextString"
	case "bool":
		return contentTypeTextPlain, "WriteTextBool"
	case "int":
		return contentTypeTextPlain, "WriteTextInt"
	case "int8":
		return contentTypeTextPlain, "WriteTextInt8"
	case "int16":
		return contentTypeTextPlain, "WriteTextInt16"
	case "int32", "rune":
		return contentTypeTextPlain, "WriteTextInt32"
	case "int64":
		return contentTypeTextPlain, "WriteTextInt64"
	case "uint":
		return contentTypeTextPlain, "WriteTextUint"
	case "uint8":
		return contentTypeTextPlain, "WriteTextUint8"
	case "uint16":
		return contentTypeTextPlain, "WriteTextUint16"
	case "uint32":
		return contentTypeTextPlain, "WriteTextUint32"
	case "uint64":
		return contentTypeTextPlain, "WriteTextUint64"
	case "uintptr":
		return contentTypeTextPlain, "WriteTextUintptr"
	case "float32":
		return contentTypeTextPlain, "WriteTextFloat32"
	case "float64":
		return contentTypeTextPlain, "WriteTextFloat64"
	case "*big.Int":
		return contentTypeTextPlain, "WriteTextBigInt"
	case "big.Int":
		return contentTypeTextPlain, "WriteTextBigIntValue"
	case "*big.Float":
		return contentTypeTextPlain, "WriteTextBigFloat"
	case "big.Float":
		return contentTypeTextPlain, "WriteTextBigFloatValue"
	case "*big.Rat":
		return contentTypeTextPlain, "WriteTextBigRat"
	case "big.Rat":
		return contentTypeTextPlain, "WriteTextBigRatValue"
	}
	if isActionJSONType(typeName) {
		return contentTypeJSON, "WriteRawAny"
	}
	return contentTypeJSON, "WriteRawAny"
}

func isActionHTMLType(typeName string) bool {
	typeName = strings.TrimSpace(typeName)
	return typeName == "HTML" || typeName == "gox.HTML"
}

func isActionJSONType(typeName string) bool {
	typeName = strings.TrimSpace(typeName)
	return strings.HasPrefix(typeName, "map[") || strings.HasPrefix(typeName, "[]") || strings.HasPrefix(typeName, "[") || strings.Contains(typeName, "struct")
}

func isActionErrorType(typeName string) bool {
	typeName = strings.TrimSpace(typeName)
	return typeName == "error" || strings.HasSuffix(typeName, ".error")
}

func collectActionReturnTypeNames(fn *ast.FuncDecl, env *TemplateTypeEnv) []string {
	seen := map[string]bool{}
	if fn != nil && fn.Body != nil {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			ret, ok := n.(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				return true
			}
			typeName := actionReturnExprType(ret.Results[0], env)
			if !isActionErrorType(typeName) {
				seen[typeName] = true
			}
			return true
		})
	}
	out := make([]string, 0, len(seen))
	for typeName := range seen {
		out = append(out, typeName)
	}
	sort.Strings(out)
	return out
}

func actionReturnExprType(expr ast.Expr, typeEnv *TemplateTypeEnv) string {
	if expr == nil {
		return "nil"
	}
	if id, ok := expr.(*ast.Ident); ok && id.Name == "nil" {
		return "nil"
	}
	typeName := "any"
	if typeEnv != nil {
		typeName = typeEnv.ExprType(expr)
	}
	if typeName == "" || typeName == "any" {
		typeName = inferTemplateExprType(expr, typeEnv)
	}
	return strings.TrimSpace(typeName)
}

func buildActionFileReturnStmts(expr ast.Expr) []ast.Stmt {
	return buildActionFileHelperReturnStmts("WriteActionFile", "goxActionFileErr", expr)
}

func buildActionFilesReturnStmts(expr ast.Expr) []ast.Stmt {
	return buildActionFileHelperReturnStmts("WriteActionFiles", "goxActionFilesErr", expr)
}

func buildActionFileHelperReturnStmts(helperName string, errName string, expr ast.Expr) []ast.Stmt {
	errIdent := ast.NewIdent(errName)
	return []ast.Stmt{
		&ast.IfStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{errIdent},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{&ast.CallExpr{
					Fun:  &ast.SelectorExpr{X: ast.NewIdent("gox"), Sel: ast.NewIdent(helperName)},
					Args: []ast.Expr{ast.NewIdent("ctx"), expr},
				}},
			},
			Cond: &ast.BinaryExpr{X: errIdent, Op: token.NEQ, Y: ast.NewIdent("nil")},
			Body: &ast.BlockStmt{List: buildActionErrorWriteStmts(errIdent)},
		},
		&ast.ReturnStmt{},
	}
}
