package gox

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

type TemplateTypeEnv struct {
	Vars             map[string]string
	StructFields     map[string]map[string]string
	Components       map[string]ComponentContract
	ComponentValues  map[string]bool
	ComponentCalls   *[]pendingComponentCall
	StrictComponents bool
}

func NewTemplateTypeEnv() *TemplateTypeEnv {
	return &TemplateTypeEnv{
		Vars:            map[string]string{},
		StructFields:    map[string]map[string]string{},
		ComponentValues: map[string]bool{},
	}
}

func (e *TemplateTypeEnv) Clone() *TemplateTypeEnv {
	out := NewTemplateTypeEnv()
	out.Components = e.Components
	out.ComponentCalls = e.ComponentCalls
	out.StrictComponents = e.StrictComponents
	for name, value := range e.ComponentValues {
		out.ComponentValues[name] = value
	}
	for k, v := range e.Vars {
		out.Vars[k] = v
	}
	for k, fields := range e.StructFields {
		out.StructFields[k] = fields
	}
	return out
}

func (e *TemplateTypeEnv) ExprType(expr ast.Expr) string {
	if e == nil || expr == nil {
		return "any"
	}

	switch v := expr.(type) {
	case *ast.Ident:
		if typ, ok := e.Vars[v.Name]; ok && typ != "" {
			return typ
		}
		switch v.Name {
		case "true", "false":
			return "bool"
		}
		return "any"

	case *ast.BasicLit:
		switch v.Kind {
		case token.STRING:
			return "string"
		case token.INT:
			return inferIntegerLiteralType(v.Value)
		case token.FLOAT:
			return "float64"
		case token.CHAR:
			return "rune"
		}

	case *ast.SelectorExpr:
		if pkg, ok := v.X.(*ast.Ident); ok {
			if pkg.Name == "big" {
				return "big." + v.Sel.Name
			}
			if typ := inferKnownSelectorType(pkg.Name, v.Sel.Name); typ != "" {
				return typ
			}
		}
		baseType := derefTemplateType(e.ExprType(v.X))
		if fields, ok := e.StructFields[baseType]; ok {
			if typ, exists := fields[v.Sel.Name]; exists {
				return typ
			}
		}
		return "any"

	case *ast.StarExpr:
		return "*" + normalizeTemplateType(v.X)

	case *ast.ParenExpr:
		return e.ExprType(v.X)

	case *ast.UnaryExpr:
		if v.Op == token.AND {
			return "*" + e.ExprType(v.X)
		}
		return e.ExprType(v.X)

	case *ast.CallExpr:
		if typ := inferKnownCallReturnType(v); typ != "" {
			return typ
		}
		return normalizeTemplateType(v.Fun)

	case *ast.BinaryExpr:
		return inferTemplateBinaryExprType(v, e)

	case *ast.IndexExpr:
		return templateIndexExprType(e.ExprType(v.X))

	case *ast.CompositeLit:
		return normalizeTemplateType(v.Type)
	}

	return "any"
}

func buildTemplateBaseTypeEnv(file *ast.File) *TemplateTypeEnv {
	env := NewTemplateTypeEnv()
	env.Components = make(map[string]ComponentContract)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || detectGoxReturnName(file, fn) != "Component" {
			continue
		}
		contract := ComponentContract{Name: fn.Name.Name, ChildrenLast: true}
		params := flattenFuncParams(fn.Type.Params)
		for i, param := range params {
			prop := ComponentPropContract{Name: param.Name, Type: param.Type}
			if isComponentChildrenParameter(file, param.Name, param.Type) {
				contract.Children = &prop
				contract.ChildrenLast = i == len(params)-1
			} else {
				contract.Props = append(contract.Props, prop)
			}
		}
		env.Components[contract.Name] = contract
	}

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}

		switch gen.Tok {
		case token.VAR, token.CONST:
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					typ := normalizeTemplateType(vs.Type)
					if typ == "" && i < len(vs.Values) {
						typ = inferTemplateExprType(vs.Values[i], env)
					}
					if typ == "" {
						typ = "any"
					}
					env.Vars[name.Name] = typ
				}
			}

		case token.TYPE:
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				fields := map[string]string{}
				for _, field := range st.Fields.List {
					typ := normalizeTemplateType(field.Type)
					for _, name := range field.Names {
						fields[name.Name] = typ
					}
				}
				env.StructFields[ts.Name.Name] = fields
			}
		}
	}

	return env
}

func templateTypeEnvForFunc(fn *ast.FuncDecl, base *TemplateTypeEnv) *TemplateTypeEnv {
	env := base.Clone()
	if fn.Body != nil {
		values := map[string]struct{}{}
		collectLocalComponentFuncLiterals(fn.Body, values)
		for name := range values {
			env.ComponentValues[name] = true
		}
	}
	if fn.Recv != nil {
		for _, recv := range fn.Recv.List {
			recvType := normalizeTemplateType(recv.Type)
			for _, name := range recv.Names {
				if name.Name != "" {
					env.Vars[name.Name] = recvType
				}
			}
		}
	}
	if fn.Type != nil && fn.Type.Params != nil {
		for _, p := range flattenFuncParams(fn.Type.Params) {
			if p.Name == "" {
				continue
			}
			env.Vars[p.Name] = normalizeTemplateType(p.Type)
		}
	}
	if fn.Body != nil {
		collectTemplateLocalTypes(fn.Body.List, env)
	}
	return env
}

func collectTemplateLocalTypes(stmts []ast.Stmt, env *TemplateTypeEnv) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE {
				for i, lhs := range s.Lhs {
					id, ok := lhs.(*ast.Ident)
					if !ok || id.Name == "_" || i >= len(s.Rhs) {
						continue
					}
					env.Vars[id.Name] = inferTemplateExprType(s.Rhs[i], env)
				}
			}
		case *ast.DeclStmt:
			gen, ok := s.Decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					typ := normalizeTemplateType(vs.Type)
					if typ == "" && i < len(vs.Values) {
						typ = inferTemplateExprType(vs.Values[i], env)
					}
					env.Vars[name.Name] = typ
				}
			}
		case *ast.IfStmt:
			if s.Init != nil {
				collectTemplateLocalTypes([]ast.Stmt{s.Init}, env)
			}
			collectTemplateLocalTypes(s.Body.List, env)
			if b, ok := s.Else.(*ast.BlockStmt); ok {
				collectTemplateLocalTypes(b.List, env)
			}
		case *ast.ForStmt:
			if s.Init != nil {
				collectTemplateLocalTypes([]ast.Stmt{s.Init}, env)
			}
			collectTemplateLocalTypes(s.Body.List, env)
		case *ast.RangeStmt:
			collectTemplateLocalTypes(s.Body.List, env)
		}
	}
}

func inferTemplateExprType(expr ast.Expr, env *TemplateTypeEnv) string {
	if expr == nil {
		return "any"
	}
	if env != nil {
		if typ := env.ExprType(expr); typ != "any" && typ != "" {
			return typ
		}
	}
	switch v := expr.(type) {
	case *ast.BasicLit:
		switch v.Kind {
		case token.STRING:
			return "string"
		case token.INT:
			return inferIntegerLiteralType(v.Value)
		case token.FLOAT:
			return "float64"
		case token.CHAR:
			return "rune"
		}
	case *ast.Ident:
		switch v.Name {
		case "true", "false":
			return "bool"
		}
	case *ast.CompositeLit:
		return normalizeTemplateType(v.Type)
	case *ast.CallExpr:
		if typ := inferKnownCallReturnType(v); typ != "" {
			return typ
		}
		return normalizeTemplateType(v.Fun)
	case *ast.BinaryExpr:
		return inferTemplateBinaryExprType(v, env)
	case *ast.IndexExpr:
		if env != nil {
			return templateIndexExprType(env.ExprType(v.X))
		}
	}
	return "any"
}

func inferIntegerLiteralType(literal string) string {
	normalized := strings.ReplaceAll(literal, "_", "")
	value, err := strconv.ParseUint(normalized, 0, 64)
	if err == nil && value > uint64(^uint64(0)>>1) {
		return "uint64"
	}
	return "int"
}

func inferKnownCallReturnType(call *ast.CallExpr) string {
	if call == nil {
		return ""
	}
	if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "new" && len(call.Args) == 1 {
		typ := normalizeTemplateType(call.Args[0])
		if typ != "" {
			return "*" + typ
		}
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}

	switch pkg.Name + "." + sel.Sel.Name {
	case "os.Open", "os.Create", "os.OpenFile":
		return "*os.File"
	case "bytes.NewReader":
		return "*bytes.Reader"
	case "bytes.NewBuffer", "bytes.NewBufferString":
		return "*bytes.Buffer"
	case "strings.NewReader":
		return "*strings.Reader"
	case "bufio.NewReader", "bufio.NewReaderSize":
		return "*bufio.Reader"
	case "io.ReadAll":
		return "[]byte"
	case "gox.NewFile":
		return "gox.File"
	case "errors.New", "fmt.Errorf":
		return "error"
	}

	return ""
}

func inferKnownSelectorType(pkg string, name string) string {
	if pkg != "math" {
		return ""
	}
	switch name {
	case "MaxUint", "MaxUint8", "MaxUint16", "MaxUint32", "MaxUint64":
		return map[string]string{
			"MaxUint": "uint", "MaxUint8": "uint8", "MaxUint16": "uint16",
			"MaxUint32": "uint32", "MaxUint64": "uint64",
		}[name]
	case "MaxInt", "MinInt", "MaxInt8", "MinInt8", "MaxInt16", "MinInt16",
		"MaxInt32", "MinInt32", "MaxInt64", "MinInt64":
		return map[string]string{
			"MaxInt": "int", "MinInt": "int",
			"MaxInt8": "int8", "MinInt8": "int8",
			"MaxInt16": "int16", "MinInt16": "int16",
			"MaxInt32": "int32", "MinInt32": "int32",
			"MaxInt64": "int64", "MinInt64": "int64",
		}[name]
	}
	return ""
}

func derefTemplateType(typeName string) string {
	for len(typeName) > 0 && typeName[0] == '*' {
		typeName = typeName[1:]
	}
	return typeName
}

func normalizeTemplateType(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		left := normalizeTemplateType(v.X)
		if left == "" {
			return v.Sel.Name
		}
		return left + "." + v.Sel.Name
	case *ast.StarExpr:
		return "*" + normalizeTemplateType(v.X)
	case *ast.ArrayType:
		elt := normalizeTemplateType(v.Elt)
		if elt == "" || elt == "any" {
			return "any"
		}
		if v.Len == nil {
			return "[]" + elt
		}
		return "[...]" + elt
	case *ast.MapType:
		key := normalizeTemplateType(v.Key)
		val := normalizeTemplateType(v.Value)
		if key == "" || val == "" || key == "any" || val == "any" {
			return "any"
		}
		return "map[" + key + "]" + val
	case *ast.ChanType:
		element := normalizeTemplateType(v.Value)
		if element == "" || element == "any" {
			return "any"
		}
		switch v.Dir {
		case ast.RECV:
			return "<-chan " + element
		case ast.SEND:
			return "chan<- " + element
		default:
			return "chan " + element
		}
	case *ast.ParenExpr:
		return normalizeTemplateType(v.X)
	}
	return "any"
}

func templateTypeEnvWithRangeBindings(base *TemplateTypeEnv, rangeExpr ast.Expr, key ast.Expr, value ast.Expr) *TemplateTypeEnv {
	if base == nil {
		base = NewTemplateTypeEnv()
	}
	env := base.Clone()
	rangeType := env.ExprType(rangeExpr)
	keyType, valueType := templateRangeBindingTypes(rangeType)

	if id, ok := key.(*ast.Ident); ok && id.Name != "" && id.Name != "_" {
		env.Vars[id.Name] = keyType
	}
	if id, ok := value.(*ast.Ident); ok && id.Name != "" && id.Name != "_" {
		env.Vars[id.Name] = valueType
	}

	return env
}

func templateTypeEnvWithRepeatBinding(base *TemplateTypeEnv, idxName string) *TemplateTypeEnv {
	if base == nil {
		base = NewTemplateTypeEnv()
	}
	env := base.Clone()
	if idxName != "" && idxName != "_" {
		env.Vars[idxName] = "int"
	}
	return env
}

func templateRangeBindingTypes(rangeType string) (keyType string, valueType string) {
	rangeType = strings.TrimSpace(rangeType)
	if rangeType == "string" {
		return "int", "rune"
	}
	if strings.HasPrefix(rangeType, "[]") || strings.HasPrefix(rangeType, "[...") {
		return "int", templateIndexExprType(rangeType)
	}
	if strings.HasPrefix(rangeType, "*[") {
		return "int", templateIndexExprType(strings.TrimPrefix(rangeType, "*"))
	}
	if strings.HasPrefix(rangeType, "map[") {
		if key, val, ok := splitTemplateMapType(rangeType); ok {
			return key, val
		}
	}
	if strings.HasPrefix(rangeType, "<-chan ") {
		return strings.TrimSpace(strings.TrimPrefix(rangeType, "<-chan ")), "any"
	}
	if strings.HasPrefix(rangeType, "chan ") {
		return strings.TrimSpace(strings.TrimPrefix(rangeType, "chan ")), "any"
	}
	if isTemplateIntegerType(rangeType) {
		return rangeType, "any"
	}
	return "int", "any"
}

func isTemplateIntegerType(typeName string) bool {
	switch typeName {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return true
	}
	return false
}

func templateIndexExprType(containerType string) string {
	if strings.HasPrefix(containerType, "[]") {
		return containerType[2:]
	}
	if strings.HasPrefix(containerType, "[...") {
		if idx := strings.Index(containerType, "]"); idx >= 0 && idx+1 < len(containerType) {
			return containerType[idx+1:]
		}
	}
	if strings.HasPrefix(containerType, "map[") {
		_, val, ok := splitTemplateMapType(containerType)
		if ok {
			return val
		}
	}
	return "any"
}

func splitTemplateMapType(typeName string) (string, string, bool) {
	if !strings.HasPrefix(typeName, "map[") {
		return "", "", false
	}
	depth := 0
	for i := len("map["); i < len(typeName); i++ {
		switch typeName[i] {
		case '[':
			depth++
		case ']':
			if depth == 0 {
				key := typeName[len("map["):i]
				val := typeName[i+1:]
				if key == "" || val == "" {
					return "", "", false
				}
				return key, val, true
			}
			depth--
		}
	}
	return "", "", false
}

func inferTemplateBinaryExprType(expr *ast.BinaryExpr, env *TemplateTypeEnv) string {
	if expr == nil {
		return "any"
	}
	switch expr.Op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ, token.LAND, token.LOR:
		return "bool"
	}
	left := "any"
	right := "any"
	if env != nil {
		left = env.ExprType(expr.X)
		right = env.ExprType(expr.Y)
	}
	if left != "any" && left != "" {
		return left
	}
	if right != "any" && right != "" {
		return right
	}
	return "any"
}
