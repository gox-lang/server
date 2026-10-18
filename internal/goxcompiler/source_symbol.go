package gox

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

type SourceSymbolKind string

const (
	SymbolModule    SourceSymbolKind = "module"
	SymbolClient    SourceSymbolKind = "client"
	SymbolFunction  SourceSymbolKind = "function"
	SymbolMethod    SourceSymbolKind = "method"
	SymbolType      SourceSymbolKind = "type"
	SymbolStruct    SourceSymbolKind = "struct"
	SymbolInterface SourceSymbolKind = "interface"
	SymbolVariable  SourceSymbolKind = "variable"
	SymbolConstant  SourceSymbolKind = "constant"
	SymbolField     SourceSymbolKind = "field"
)

// SourceSymbol is a user-authored declaration that editor features may
// reference. Its range always points into the original .gox source.
type SourceSymbol struct {
	Name       string
	Kind       SourceSymbolKind
	Source     goxmap.SourceRange
	Container  string
	Exported   bool
	Type       string
	Underlying string
	Alias      bool
	Value      string
	Results    []string
	Parameters []SourceParameter
	Detail     string
}

type SourceParameter struct {
	Name string
	Type string
}

// CollectSourceSymbols exposes the compiler's declaration model without
// duplicating GOX parsing rules in the language server.
func CollectSourceSymbols(path, source string) ([]SourceSymbol, error) {
	module, err := ParseModule(path, source)
	if err != nil {
		return nil, err
	}
	return append([]SourceSymbol(nil), module.Symbols...), nil
}

func collectGoSourceSymbols(file *ast.File, origins *OriginTable) []SourceSymbol {
	if file == nil || origins == nil {
		return nil
	}
	var symbols []SourceSymbol
	appendIdent := func(ident *ast.Ident, kind SourceSymbolKind, container, typeName string, results []string, parameters []SourceParameter, detail string) {
		if ident == nil || ident.Name == "_" {
			return
		}
		origin, ok := origins.Get(ident)
		if !ok || !origin.Exact() {
			return
		}
		symbols = append(symbols, SourceSymbol{
			Name:       ident.Name,
			Kind:       kind,
			Source:     origin.Source,
			Container:  container,
			Exported:   ast.IsExported(ident.Name),
			Type:       typeName,
			Results:    append([]string(nil), results...),
			Parameters: append([]SourceParameter(nil), parameters...),
			Detail:     detail,
		})
	}

	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			kind := SymbolFunction
			container := ""
			if value.Recv != nil {
				kind = SymbolMethod
				container = receiverTypeName(value.Recv)
			}
			results := sourceResultTypes(value.Type.Results)
			appendIdent(value.Name, kind, container, singleResult(results), results, sourceParameters(value.Type.Params), sourceFuncDetail(value))
			collectLocalComponentSymbols(value.Body, value.Name.Name, appendIdent)
		case *ast.GenDecl:
			for _, specification := range value.Specs {
				switch item := specification.(type) {
				case *ast.TypeSpec:
					kind := SymbolType
					switch item.Type.(type) {
					case *ast.StructType:
						kind = SymbolStruct
					case *ast.InterfaceType:
						kind = SymbolInterface
					}
					appendIdent(item.Name, kind, "", item.Name.Name, nil, nil, "type "+item.Name.Name+" "+formatSourceNode(item.Type))
					if len(symbols) > 0 && symbols[len(symbols)-1].Name == item.Name.Name {
						symbols[len(symbols)-1].Underlying = formatSourceNode(item.Type)
						symbols[len(symbols)-1].Alias = item.Assign.IsValid()
					}
					collectTypeMemberSymbols(item.Name.Name, item.Type, appendIdent)
				case *ast.ValueSpec:
					kind := SymbolVariable
					if value.Tok == token.CONST {
						kind = SymbolConstant
					}
					for index, name := range item.Names {
						appendIdent(name, kind, "", sourceValueType(item, index), nil, nil, sourceValueDetail(value.Tok, item, index))
						if kind == SymbolConstant && len(symbols) > 0 && symbols[len(symbols)-1].Name == name.Name && index < len(item.Values) {
							symbols[len(symbols)-1].Value = formatSourceNode(item.Values[index])
						}
					}
				}
			}
		}
	}
	return symbols
}

func collectLocalComponentSymbols(body *ast.BlockStmt, container string, appendIdent func(*ast.Ident, SourceSymbolKind, string, string, []string, []SourceParameter, string)) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for index, expression := range value.Rhs {
				if index >= len(value.Lhs) || !isSourceComponentFuncLiteral(expression) {
					continue
				}
				if ident, ok := value.Lhs[index].(*ast.Ident); ok {
					appendIdent(ident, SymbolVariable, container, "gox.Component", nil, nil, sourceLocalComponentDetail(ident.Name, expression))
				}
			}
		case *ast.DeclStmt:
			declaration, ok := value.Decl.(*ast.GenDecl)
			if !ok {
				break
			}
			for _, specification := range declaration.Specs {
				item, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, expression := range item.Values {
					if index < len(item.Names) && isSourceComponentFuncLiteral(expression) {
						appendIdent(item.Names[index], SymbolVariable, container, "gox.Component", nil, nil, sourceLocalComponentDetail(item.Names[index].Name, expression))
					}
				}
			}
		}
		return true
	})
}

func sourceLocalComponentDetail(name string, expression ast.Expr) string {
	function, ok := expression.(*ast.FuncLit)
	if ok && !isComponentFuncLiteral(expression) {
		return name + " := " + formatSourceNode(function.Type)
	}
	return name + " := gox.Component"
}

func isSourceComponentFuncLiteral(expression ast.Expr) bool {
	if isComponentFuncLiteral(expression) {
		return true
	}
	function, ok := expression.(*ast.FuncLit)
	if !ok || function.Type == nil || function.Type.Results == nil || len(function.Type.Results.List) != 1 {
		return false
	}
	selector, ok := function.Type.Results.List[0].Type.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil || selector.Sel.Name != "Component" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "gox"
}

func collectTypeMemberSymbols(container string, expression ast.Expr, appendIdent func(*ast.Ident, SourceSymbolKind, string, string, []string, []SourceParameter, string)) {
	switch value := expression.(type) {
	case *ast.StructType:
		if value.Fields == nil {
			return
		}
		for _, field := range value.Fields.List {
			for _, name := range field.Names {
				appendIdent(name, SymbolField, container, formatSourceNode(field.Type), nil, nil, "field "+name.Name+" "+formatSourceNode(field.Type))
			}
		}
	case *ast.InterfaceType:
		if value.Methods == nil {
			return
		}
		for _, method := range value.Methods.List {
			for _, name := range method.Names {
				results := interfaceMethodResultTypes(method.Type)
				appendIdent(name, SymbolMethod, container, singleResult(results), results, interfaceMethodParameters(method.Type), name.Name+formatFuncTypeSuffix(method.Type))
			}
		}
	}
}

func sourceResultTypes(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	var results []string
	for _, field := range fields.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for index := 0; index < count; index++ {
			results = append(results, formatSourceNode(field.Type))
		}
	}
	return results
}

func sourceParameters(fields *ast.FieldList) []SourceParameter {
	if fields == nil {
		return nil
	}
	var parameters []SourceParameter
	for _, field := range fields.List {
		typeName := formatSourceNode(field.Type)
		if len(field.Names) == 0 {
			parameters = append(parameters, SourceParameter{Type: typeName})
			continue
		}
		for _, name := range field.Names {
			if name.Name != "_" {
				parameters = append(parameters, SourceParameter{Name: name.Name, Type: typeName})
			}
		}
	}
	return parameters
}

func singleResult(results []string) string {
	if len(results) != 1 {
		return ""
	}
	return results[0]
}

func interfaceMethodResultTypes(expression ast.Expr) []string {
	function, ok := expression.(*ast.FuncType)
	if !ok {
		return nil
	}
	return sourceResultTypes(function.Results)
}

func interfaceMethodParameters(expression ast.Expr) []SourceParameter {
	function, ok := expression.(*ast.FuncType)
	if !ok {
		return nil
	}
	return sourceParameters(function.Params)
}

func sourceValueType(specification *ast.ValueSpec, index int) string {
	if specification == nil {
		return ""
	}
	if specification.Type != nil {
		return formatSourceNode(specification.Type)
	}
	if index < len(specification.Values) {
		return sourceExpressionType(specification.Values[index])
	}
	return ""
}

func sourceExpressionType(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.BasicLit:
		switch value.Kind {
		case token.STRING:
			return "string"
		case token.INT:
			return "int"
		case token.FLOAT:
			return "float64"
		case token.CHAR:
			return "rune"
		}
	case *ast.CompositeLit:
		return formatSourceNode(value.Type)
	case *ast.FuncLit:
		if isSourceComponentFuncLiteral(value) {
			return "gox.Component"
		}
	case *ast.Ident:
		if value.Name == "true" || value.Name == "false" {
			return "bool"
		}
	}
	return ""
}

func sourceFuncDetail(function *ast.FuncDecl) string {
	if function == nil || function.Name == nil || function.Type == nil {
		return ""
	}
	detail := "func "
	if function.Recv != nil {
		detail += formatReceiver(function.Recv) + " "
	}
	return detail + function.Name.Name + formatFuncTypeSuffix(function.Type)
}

func formatReceiver(receiver *ast.FieldList) string {
	if receiver == nil || len(receiver.List) == 0 {
		return ""
	}
	parts := make([]string, 0, len(receiver.List))
	for _, field := range receiver.List {
		typeName := formatSourceNode(field.Type)
		if len(field.Names) == 0 {
			parts = append(parts, typeName)
			continue
		}
		names := make([]string, 0, len(field.Names))
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
		parts = append(parts, strings.Join(names, ", ")+" "+typeName)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func formatFuncTypeSuffix(expression ast.Expr) string {
	formatted := formatSourceNode(expression)
	return strings.TrimPrefix(formatted, "func")
}

func sourceValueDetail(keyword token.Token, specification *ast.ValueSpec, index int) string {
	if specification == nil || index >= len(specification.Names) {
		return ""
	}
	detail := strings.ToLower(keyword.String()) + " " + specification.Names[index].Name
	if specification.Type != nil {
		detail += " " + formatSourceNode(specification.Type)
	}
	if index < len(specification.Values) {
		detail += " = " + formatSourceNode(specification.Values[index])
	}
	return detail
}

func formatSourceNode(node ast.Node) string {
	if node == nil {
		return ""
	}
	var output bytes.Buffer
	if err := format.Node(&output, token.NewFileSet(), node); err != nil {
		return ""
	}
	return output.String()
}

func receiverTypeName(fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}
	return sourceTypeName(fields.List[0].Type)
}

func sourceTypeName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return sourceTypeName(value.X)
	case *ast.IndexExpr:
		return sourceTypeName(value.X)
	case *ast.IndexListExpr:
		return sourceTypeName(value.X)
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func collectNativeSourceSymbols(source string, module *Module) []SourceSymbol {
	if module == nil {
		return nil
	}
	var symbols []SourceSymbol
	if keywordOffset := findModuleKeywordOffset(source); keywordOffset >= 0 && module.DeclaredName != "" {
		nameOffset := keywordOffset + len("module")
		for nameOffset < len(source) && (source[nameOffset] == ' ' || source[nameOffset] == '\t') {
			nameOffset++
		}
		symbol := sourceSymbolAtOffset(module.Path, module.DeclaredName, SymbolModule, nameOffset)
		symbol.Detail = "module " + module.DeclaredName
		symbols = append(symbols, symbol)
	}
	for _, client := range module.Clients {
		if client.NameOffset >= 0 {
			symbol := sourceSymbolAtOffset(module.Path, client.Name, SymbolClient, client.NameOffset)
			symbol.Detail = clientDeclarationDetail(client)
			symbols = append(symbols, symbol)
		}
	}
	return symbols
}

func clientDeclarationDetail(client ClientDecl) string {
	return fmt.Sprintf("client %s %s {\n\tsrc: %q\n\tdev: %t\n}", client.Name, strings.ToLower(string(client.Framework)), client.Entry, client.Dev)
}

func sourceSymbolAtOffset(path, name string, kind SourceSymbolKind, offset int) SourceSymbol {
	return SourceSymbol{
		Name:     name,
		Kind:     kind,
		Source:   goxmap.SourceRange{URI: path, Range: goxmap.Range{Start: offset, End: offset + len(name)}},
		Exported: ast.IsExported(name),
	}
}
