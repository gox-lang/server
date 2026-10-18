package gox

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

// LocalSymbol describes a declaration whose identity depends on lexical scope.
// Source and Scope always use offsets in the original .gox document.
type LocalSymbol struct {
	Name              string
	Kind              SourceSymbolKind
	Source            goxmap.SourceRange
	Scope             goxmap.SourceRange
	Type              string
	Initializer       string
	InitializerResult int
	Detail            string
	Signature         bool
}

type SelectorReference struct {
	Name      string
	Kind      SourceSymbolKind
	Source    goxmap.SourceRange
	Target    goxmap.SourceRange
	Container string
}

type UnresolvedIdentifier struct {
	Name   string
	Source goxmap.SourceRange
}

func collectLocalSymbols(file *ast.File, fset *token.FileSet, sourceMap *goxmap.Map, origins *OriginTable, semantic *SemanticModel) ([]LocalSymbol, []SelectorReference, []UnresolvedIdentifier) {
	if file == nil || fset == nil || sourceMap == nil || origins == nil || semantic == nil || semantic.Info == nil {
		return nil, nil, nil
	}
	typeInfo := semantic.Info
	var symbols []LocalSymbol
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			if general, ok := declaration.(*ast.GenDecl); ok {
				collectDeclarationFunctionLiterals(general, fset, sourceMap, origins, typeInfo, &symbols)
			}
			continue
		}
		scope, ok := sourceBlockRange(function.Body, fset, sourceMap, origins)
		if !ok {
			continue
		}
		appendFields := func(fields *ast.FieldList, kind SourceSymbolKind) {
			if fields == nil {
				return
			}
			for _, field := range fields.List {
				for _, name := range field.Names {
					before := len(symbols)
					appendTypedLocalSymbol(&symbols, origins, typeInfo, name, kind, scope, formatSourceNode(field.Type), "")
					if len(symbols) > before {
						symbols[len(symbols)-1].Signature = true
					}
				}
			}
		}
		appendFields(function.Recv, SymbolVariable)
		appendFields(function.Type.Params, SymbolVariable)
		appendFields(function.Type.Results, SymbolVariable)
		collectBlockLocalSymbols(function.Body, scope, fset, sourceMap, origins, typeInfo, &symbols)
	}
	return symbols, collectSelectorReferences(typeInfo, origins), collectUnresolvedIdentifiers(file, typeInfo, origins)
}

func collectDeclarationFunctionLiterals(declaration *ast.GenDecl, fset *token.FileSet, sourceMap *goxmap.Map, origins *OriginTable, typeInfo *types.Info, output *[]LocalSymbol) {
	if declaration == nil {
		return
	}
	for _, specification := range declaration.Specs {
		value, ok := specification.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, expression := range value.Values {
			ast.Inspect(expression, func(node ast.Node) bool {
				literal, ok := node.(*ast.FuncLit)
				if !ok || literal.Body == nil {
					return true
				}
				scope, ok := sourceBlockRange(literal.Body, fset, sourceMap, origins)
				if !ok {
					return false
				}
				appendFunctionLiteralFields(literal.Type.Params, scope, origins, typeInfo, output)
				appendFunctionLiteralFields(literal.Type.Results, scope, origins, typeInfo, output)
				collectBlockLocalSymbols(literal.Body, scope, fset, sourceMap, origins, typeInfo, output)
				return false
			})
		}
	}
}

func collectUnresolvedIdentifiers(file *ast.File, typeInfo *types.Info, origins *OriginTable) []UnresolvedIdentifier {
	excluded := make(map[*ast.Ident]bool)
	dotImport := false
	for _, specification := range file.Imports {
		if specification.Name != nil && specification.Name.Name == "." {
			dotImport = true
		}
	}
	if dotImport {
		return nil
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncDecl:
			excluded[value.Name] = true
		case *ast.TypeSpec:
			excluded[value.Name] = true
		case *ast.ValueSpec:
			for _, name := range value.Names {
				excluded[name] = true
			}
		case *ast.Field:
			for _, name := range value.Names {
				excluded[name] = true
			}
		case *ast.AssignStmt:
			if value.Tok == token.DEFINE {
				for _, expression := range value.Lhs {
					if name, ok := expression.(*ast.Ident); ok {
						excluded[name] = true
					}
				}
			}
		case *ast.RangeStmt:
			if value.Tok == token.DEFINE {
				if name, ok := value.Key.(*ast.Ident); ok {
					excluded[name] = true
				}
				if name, ok := value.Value.(*ast.Ident); ok {
					excluded[name] = true
				}
			}
		case *ast.SelectorExpr:
			if qualifier, ok := value.X.(*ast.Ident); ok {
				excluded[qualifier] = true
			}
			excluded[value.Sel] = true
		case *ast.KeyValueExpr:
			if key, ok := value.Key.(*ast.Ident); ok {
				excluded[key] = true
			}
		case *ast.BranchStmt:
			excluded[value.Label] = true
		case *ast.LabeledStmt:
			excluded[value.Label] = true
		}
		return true
	})
	seen := make(map[goxmap.Range]bool)
	var unresolved []UnresolvedIdentifier
	ast.Inspect(file, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name == "_" || excluded[ident] {
			return true
		}
		if _, declared := typeInfo.Defs[ident]; declared {
			return true
		}
		if object, used := typeInfo.Uses[ident]; used && object != nil {
			return true
		}
		source, ok := exactSourceRange(origins, ident)
		if !ok || seen[source.Range] {
			return true
		}
		seen[source.Range] = true
		unresolved = append(unresolved, UnresolvedIdentifier{Name: ident.Name, Source: source})
		return true
	})
	return unresolved
}

func collectSelectorReferences(typeInfo *types.Info, origins *OriginTable) []SelectorReference {
	declarations := make(map[types.Object]goxmap.SourceRange)
	for ident, object := range typeInfo.Defs {
		if object == nil {
			continue
		}
		if source, ok := exactSourceRange(origins, ident); ok {
			declarations[object] = source
		}
	}
	var references []SelectorReference
	for expression, selection := range typeInfo.Selections {
		if expression == nil || expression.Sel == nil || selection == nil || selection.Obj() == nil {
			continue
		}
		source, sourceOK := exactSourceRange(origins, expression.Sel)
		target, targetOK := declarations[selection.Obj()]
		if !sourceOK || !targetOK || source.URI != target.URI {
			continue
		}
		kind := SymbolMethod
		if _, ok := selection.Obj().(*types.Var); ok {
			kind = SymbolField
		}
		references = append(references, SelectorReference{
			Name: expression.Sel.Name, Kind: kind, Source: source, Target: target,
			Container: selectedTypeName(selection.Recv()),
		})
	}
	return references
}

func selectedTypeName(value types.Type) string {
	if pointer, ok := value.(*types.Pointer); ok {
		value = pointer.Elem()
	}
	if named, ok := value.(*types.Named); ok && named.Obj() != nil {
		return named.Obj().Name()
	}
	return ""
}

func collectBlockLocalSymbols(block *ast.BlockStmt, scope goxmap.SourceRange, fset *token.FileSet, sourceMap *goxmap.Map, origins *OriginTable, typeInfo *types.Info, output *[]LocalSymbol) {
	if block == nil {
		return
	}
	if blockScope, ok := sourceBlockRange(block, fset, sourceMap, origins); ok {
		scope = blockScope
	}
	collectStatementLocalSymbols(block.List, scope, fset, sourceMap, origins, typeInfo, output)
}

func collectStatementLocalSymbols(statements []ast.Stmt, scope goxmap.SourceRange, fset *token.FileSet, sourceMap *goxmap.Map, origins *OriginTable, typeInfo *types.Info, output *[]LocalSymbol) {
	for _, statement := range statements {
		switch value := statement.(type) {
		case *ast.CaseClause:
			collectStatementLocalSymbols(value.Body, statementLocalScope(value, scope, origins), fset, sourceMap, origins, typeInfo, output)
			continue
		case *ast.CommClause:
			clauseScope := statementLocalScope(value, scope, origins)
			collectInitLocalSymbols(value.Comm, clauseScope, origins, typeInfo, output)
			collectStatementLocalSymbols(value.Body, clauseScope, fset, sourceMap, origins, typeInfo, output)
			continue
		case *ast.AssignStmt:
			if value.Tok == token.DEFINE {
				for index, expression := range value.Lhs {
					name, ok := expression.(*ast.Ident)
					if !ok || name.Name == "_" {
						continue
					}
					fallbackType, initializer, resultIndex := assignmentInference(value.Rhs, len(value.Lhs), index)
					appendTypedLocalSymbol(output, origins, typeInfo, name, SymbolVariable, scope, fallbackType, initializer, resultIndex)
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
				for index, name := range item.Names {
					_, initializer, resultIndex := assignmentInference(item.Values, len(item.Names), index)
					appendTypedLocalSymbol(output, origins, typeInfo, name, SymbolVariable, scope, formatSourceNode(item.Type), initializer, resultIndex)
				}
			}
		case *ast.IfStmt:
			collectInitLocalSymbols(value.Init, statementLocalScope(value, scope, origins), origins, typeInfo, output)
		case *ast.ForStmt:
			collectInitLocalSymbols(value.Init, statementLocalScope(value, scope, origins), origins, typeInfo, output)
		case *ast.SwitchStmt:
			collectInitLocalSymbols(value.Init, statementLocalScope(value, scope, origins), origins, typeInfo, output)
		case *ast.RangeStmt:
			if value.Tok == token.DEFINE {
				declarationScope := scope
				if nestedScope, ok := sourceBlockRange(value.Body, fset, sourceMap, origins); ok {
					declarationScope = nestedScope
				}
				if name, ok := value.Key.(*ast.Ident); ok && typeInfo.Defs[name] != nil {
					appendTypedLocalSymbol(output, origins, typeInfo, name, SymbolVariable, declarationScope, "", "")
				}
				if name, ok := value.Value.(*ast.Ident); ok && typeInfo.Defs[name] != nil {
					appendTypedLocalSymbol(output, origins, typeInfo, name, SymbolVariable, declarationScope, "", "")
				}
			}
		}
		ast.Inspect(statement, func(node ast.Node) bool {
			if literal, ok := node.(*ast.FuncLit); ok && literal.Body != nil {
				literalScope, ok := sourceBlockRange(literal.Body, fset, sourceMap, origins)
				if !ok {
					literalScope = scope
				}
				appendFunctionLiteralFields(literal.Type.Params, literalScope, origins, typeInfo, output)
				appendFunctionLiteralFields(literal.Type.Results, literalScope, origins, typeInfo, output)
				collectBlockLocalSymbols(literal.Body, literalScope, fset, sourceMap, origins, typeInfo, output)
				return false
			}
			nested, ok := node.(*ast.BlockStmt)
			if !ok {
				return true
			}
			collectBlockLocalSymbols(nested, scope, fset, sourceMap, origins, typeInfo, output)
			return false
		})
	}
}

func appendFunctionLiteralFields(fields *ast.FieldList, scope goxmap.SourceRange, origins *OriginTable, typeInfo *types.Info, output *[]LocalSymbol) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			before := len(*output)
			appendTypedLocalSymbol(output, origins, typeInfo, name, SymbolVariable, scope, formatSourceNode(field.Type), "")
			if len(*output) > before {
				(*output)[len(*output)-1].Signature = true
			}
		}
	}
}

func collectInitLocalSymbols(statement ast.Stmt, scope goxmap.SourceRange, origins *OriginTable, typeInfo *types.Info, output *[]LocalSymbol) {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE {
		return
	}
	for index, expression := range assignment.Lhs {
		name, ok := expression.(*ast.Ident)
		if !ok || name.Name == "_" {
			continue
		}
		fallbackType, initializer, resultIndex := assignmentInference(assignment.Rhs, len(assignment.Lhs), index)
		appendTypedLocalSymbol(output, origins, typeInfo, name, SymbolVariable, scope, fallbackType, initializer, resultIndex)
	}
}

func statementLocalScope(statement ast.Stmt, fallback goxmap.SourceRange, origins *OriginTable) goxmap.SourceRange {
	if source, ok := exactSourceRange(origins, statement); ok {
		return source
	}
	return fallback
}

func assignmentInference(expressions []ast.Expr, targetCount, index int) (string, string, int) {
	if index < 0 || len(expressions) == 0 {
		return "", "", 0
	}
	if len(expressions) == 1 && targetCount > 1 {
		expression := expressions[0]
		return "", formatSourceNode(expression), index
	}
	if index >= len(expressions) {
		return "", "", 0
	}
	expression := expressions[index]
	return sourceExpressionType(expression), formatSourceNode(expression), 0
}

func appendLocalSymbol(output *[]LocalSymbol, origins *OriginTable, ident *ast.Ident, kind SourceSymbolKind, scope goxmap.SourceRange, typeName, initializer string, initializerResult int, detail string) {
	if ident == nil || ident.Name == "_" {
		return
	}
	origin, ok := origins.Get(ident)
	if !ok || !origin.Exact() || origin.Source.URI != scope.URI {
		return
	}
	*output = append(*output, LocalSymbol{Name: ident.Name, Kind: kind, Source: origin.Source, Scope: scope, Type: strings.TrimSpace(typeName), Initializer: strings.TrimSpace(initializer), InitializerResult: initializerResult, Detail: strings.TrimSpace(detail)})
}

func appendTypedLocalSymbol(output *[]LocalSymbol, origins *OriginTable, typeInfo *types.Info, ident *ast.Ident, kind SourceSymbolKind, scope goxmap.SourceRange, fallbackType, initializer string, initializerResult ...int) {
	object := typeInfo.Defs[ident]
	typeName := ""
	if object != nil {
		typeName = types.TypeString(object.Type(), func(pkg *types.Package) string {
			if pkg == nil || pkg.Name() == "main" {
				return ""
			}
			return pkg.Name()
		})
	}
	if typeName == "invalid type" || typeName == "" {
		typeName = fallbackType
	}
	resultIndex := 0
	if len(initializerResult) > 0 {
		resultIndex = initializerResult[0]
	}
	appendLocalSymbol(output, origins, ident, kind, scope, typeName, initializer, resultIndex, localDetail(ident.Name, typeName))
}

func exactSourceRange(origins *OriginTable, node ast.Node) (goxmap.SourceRange, bool) {
	origin, ok := origins.Get(node)
	return origin.Source, ok && origin.Exact()
}

func sourceBlockRange(block *ast.BlockStmt, fset *token.FileSet, sourceMap *goxmap.Map, origins *OriginTable) (goxmap.SourceRange, bool) {
	if block == nil {
		return goxmap.SourceRange{}, false
	}
	if source, ok := exactSourceRange(origins, block); ok {
		return source, true
	}
	left := fset.PositionFor(block.Lbrace, false).Offset
	right := fset.PositionFor(block.Rbrace, false).Offset
	start, _, startOK := sourceMap.OriginalRange(goxmap.Range{Start: left, End: left + 1})
	end, _, endOK := sourceMap.OriginalRange(goxmap.Range{Start: right, End: right + 1})
	if !startOK || !endOK || start.URI != end.URI {
		return goxmap.SourceRange{}, false
	}
	return goxmap.SourceRange{URI: start.URI, Range: goxmap.Range{Start: start.Range.Start, End: end.Range.End}}, true
}

func localDetail(name, typeName string) string {
	if typeName == "" {
		return "var " + name
	}
	return "var " + name + " " + typeName
}
