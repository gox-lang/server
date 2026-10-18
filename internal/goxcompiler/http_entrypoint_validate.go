package gox

import (
	"fmt"
	"go/ast"
)

func ValidatePageEntrypoints(mod *Module) error {
	for _, fn := range mod.Funcs {
		if fn.Kind != FuncPage && fn.Kind != FuncHandler {
			continue
		}

		if err := validatePageEntrypointParams(fn); err != nil {
			return newCompilerErrorWithCause(
				"invalid-page-entrypoint",
				fmt.Sprintf("invalid Page entrypoint %s.%s", mod.Name, fn.Name),
				err,
			)
		}
	}

	return nil
}

func validateHTTPEntrypointFunc(fn *ast.FuncDecl) error {
	if fn.Type == nil || fn.Type.Params == nil {
		return newCompilerErrorWithHint("invalid-page-signature", "expected params (ctx *gox.Context)", "Use func Default(ctx *gox.Context) gox.Page.")
	}

	params := flattenFuncParams(fn.Type.Params)
	if len(params) != 1 || params[0].Name == "" || !isGoxContext(params[0].Type) {
		return newCompilerErrorWithHint("invalid-page-signature", "expected exactly one param: (ctx *gox.Context)", "Use exactly one parameter: ctx *gox.Context.")
	}

	return nil
}

func validatePageEntrypointParams(fn FuncDecl) error {
	params := fn.Params

	if len(params) == 1 && params[0].Name != "" && isGoxContext(params[0].Type) {
		return nil
	}

	return newCompilerErrorWithHint(
		"invalid-page-signature",
		fmt.Sprintf("invalid Page signature. expected: func %s(ctx *gox.Context) gox.Page", fn.Name),
		"Use func Default(ctx *gox.Context) gox.Page.",
	)
}

func isHTTPResponse(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return pkg.Name == "http" && sel.Sel.Name == "Response"
}

func isIOWriter(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return pkg.Name == "io" && sel.Sel.Name == "Writer"
}

type flatParam struct {
	Name string
	Type ast.Expr
}

func flattenFuncParams(params *ast.FieldList) []flatParam {
	var out []flatParam

	for _, field := range params.List {
		if len(field.Names) == 0 {
			out = append(out, flatParam{Type: field.Type})
			continue
		}

		for _, name := range field.Names {
			out = append(out, flatParam{Name: name.Name, Type: field.Type})
		}
	}

	return out
}

func isHTTPResponseWriter(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return pkg.Name == "http" && sel.Sel.Name == "ResponseWriter"
}

func isHTTPPointerRequest(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}

	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return pkg.Name == "http" && sel.Sel.Name == "Request"
}
