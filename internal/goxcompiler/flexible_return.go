package gox

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

func rewriteFlexibleReturnStmt(ret *ast.ReturnStmt, typeEnv *TemplateTypeEnv, writerName string, returnKind GoxReturnKind) ([]ast.Stmt, bool, error) {
	if returnKind == GoxReturnNone || returnKind == GoxReturnWebSocket || returnKind == GoxReturnChannel || returnKind == GoxReturnEventReply {
		return nil, false, nil
	}
	if ret == nil || len(ret.Results) != 1 {
		return nil, false, nil
	}

	expr := ret.Results[0]
	if returnKind == GoxReturnAction {
		typeName := actionReturnExprType(expr, typeEnv)
		if isActionErrorType(typeName) {
			return buildActionErrorResponseStmts(expr), true, nil
		}
		if isTemplateFileType(typeName) {
			return buildActionFileReturnStmts(expr), true, nil
		}
		if isTemplateFilesType(typeName) {
			return buildActionFilesReturnStmts(expr), true, nil
		}
		return buildActionSuccessReturnStmts(expr, typeName), true, nil
	}

	typeName := templateReturnExprType(expr, typeEnv)
	if writerName == "ctx.Writer" && isTemplateFilesType(typeName) {
		err := newCompilerErrorWithHint(
			"invalid-page-return",
			fmt.Sprintf("pages cannot return %s", typeName),
			"Return one gox.File from a page. Multiple files are supported only by Actions.",
		)
		return nil, false, err
	}

	methodName := flexibleReturnWriteMethodName(expr, typeEnv, writerName, returnKind)
	if methodName == "" {
		return nil, false, nil
	}
	if methodName == "WritePageFile" {
		return buildPageFileReturnStmts(expr), true, nil
	}

	return []ast.Stmt{
		&ast.ExprStmt{
			X: &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   dottedIdent(writerName),
					Sel: ast.NewIdent(methodName),
				},
				Args: []ast.Expr{expr},
			},
		},
		&ast.ReturnStmt{},
	}, true, nil
}

func templateReturnExprType(expr ast.Expr, typeEnv *TemplateTypeEnv) string {
	typeName := "any"
	if typeEnv != nil {
		typeName = typeEnv.ExprType(expr)
	}
	if typeName == "" || typeName == "any" {
		typeName = inferTemplateExprType(expr, typeEnv)
	}
	return strings.TrimSpace(typeName)
}

func flexibleReturnWriteMethodName(expr ast.Expr, typeEnv *TemplateTypeEnv, writerName string, returnKind GoxReturnKind) string {
	typeName := templateReturnExprType(expr, typeEnv)

	if writerName == "ctx.Writer" && isTemplateFileType(typeName) {
		return "WritePageFile"
	}
	if isTemplateStreamType(typeName) {
		return "WriteStream"
	}

	switch typeName {
	case "[]byte":
		return "WriteBytes"
	case "byte":
		return "WriteByte"
	case "string":
		return "WriteTextString"
	case "bool":
		return "WriteTextBool"
	case "int":
		return "WriteTextInt"
	case "int8":
		return "WriteTextInt8"
	case "int16":
		return "WriteTextInt16"
	case "int32", "rune":
		return "WriteTextInt32"
	case "int64":
		return "WriteTextInt64"
	case "uint":
		return "WriteTextUint"
	case "uint8":
		return "WriteTextUint8"
	case "uint16":
		return "WriteTextUint16"
	case "uint32":
		return "WriteTextUint32"
	case "uint64":
		return "WriteTextUint64"
	case "uintptr":
		return "WriteTextUintptr"
	case "float32":
		return "WriteTextFloat32"
	case "float64":
		return "WriteTextFloat64"
	case "*big.Int":
		return "WriteTextBigInt"
	case "big.Int":
		return "WriteTextBigIntValue"
	case "*big.Float":
		return "WriteTextBigFloat"
	case "big.Float":
		return "WriteTextBigFloatValue"
	case "*big.Rat":
		return "WriteTextBigRat"
	case "big.Rat":
		return "WriteTextBigRatValue"
	}

	if returnKind == GoxReturnBinary {
		return "WriteRawAny"
	}

	return "WriteTextAny"
}

func isTemplateStreamType(typeName string) bool {
	switch strings.TrimSpace(typeName) {
	case "io.Reader",
		"*os.File", "os.File",
		"*bytes.Buffer", "bytes.Buffer",
		"*bytes.Reader", "bytes.Reader",
		"*strings.Reader", "strings.Reader",
		"*bufio.Reader", "bufio.Reader":
		return true
	default:
		return false
	}
}

func isTemplateFileType(typeName string) bool {
	return isExactGoxFileType(typeName)
}

func isTemplateFilesType(typeName string) bool {
	typeName = strings.TrimSpace(typeName)
	if !strings.HasPrefix(typeName, "[]") {
		return false
	}
	return isExactGoxFileType(strings.TrimSpace(strings.TrimPrefix(typeName, "[]")))
}

func isExactGoxFileType(typeName string) bool {
	switch strings.TrimSpace(typeName) {
	case "File", "gox.File", "github.com/gox-lang/gox.File":
		return true
	default:
		return false
	}
}

func buildPageFileReturnStmts(expr ast.Expr) []ast.Stmt {
	return buildPageFileHelperReturnStmts("WritePageFile", "goxFileErr", expr)
}

func buildPageFileHelperReturnStmts(helperName string, errName string, expr ast.Expr) []ast.Stmt {
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
			Body: &ast.BlockStmt{List: []ast.Stmt{
				&ast.ExprStmt{X: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.SelectorExpr{X: ast.NewIdent("ctx"), Sel: ast.NewIdent("Writer")},
						Sel: ast.NewIdent("WriteError"),
					},
					Args: []ast.Expr{errIdent},
				}},
			}},
		},
		&ast.ReturnStmt{},
	}
}
