package gox

import (
	"go/ast"
	"go/token"
)

// SemanticGoxReturnMode describes how a GOX marker return is represented in
// the source-level semantic projection. The runtime/compiler transforms remain
// authoritative for execution; this lowering exists only so go/types can
// analyze the Go expressions written by the user without treating GOX marker
// types as ordinary Go return types.
type SemanticGoxReturnMode string

const (
	// SemanticGoxReturnFlexibleRequired keeps one semantic result. Any single Go
	// value is accepted, but normal Go control-flow still requires a return.
	SemanticGoxReturnFlexibleRequired SemanticGoxReturnMode = "flexible-required"

	// SemanticGoxReturnFlexibleOptional keeps one semantic result while allowing
	// both bare returns and control flow to reach the closing brace.
	SemanticGoxReturnFlexibleOptional SemanticGoxReturnMode = "flexible-optional"

	// SemanticGoxReturnVoid removes the marker result completely. Return values
	// therefore remain ordinary Go errors, while a body with no return is valid.
	SemanticGoxReturnVoid SemanticGoxReturnMode = "void"
)

// SemanticGoxReturn records one GOX marker function lowered for semantic
// tooling. Function is either *ast.FuncDecl or *ast.FuncLit and remains the
// node used by the semantic projection after lowering.
type SemanticGoxReturn struct {
	Function   ast.Node
	ReturnName string
	Kind       GoxReturnKind
	Mode       SemanticGoxReturnMode
}

// lowerSemanticGoxFunctions makes GOX marker functions type-checkable without
// teaching go/types GOX runtime semantics. It deliberately preserves every
// user-authored return expression in place; only the synthetic semantic result
// signature changes.
//
// The contracts mirror the existing GOX language rules:
//   - Page and Action may return one arbitrary value, use bare return, or fall
//     through without an explicit return.
//   - Component and the other flexible response markers accept one arbitrary
//     value but retain Go's requirement that the function terminate with a
//     return. Named results keep their source names, so a source-level bare
//     return remains valid when Go would normally allow it.
//   - WebSocket and Channel are marker-only results. They become void functions
//     semantically, so returning a value remains a normal Go error.
//   - EventReply is the flexible return marker used by WebSocket/Channel
//     callbacks and is lowered like a required flexible result.
//
// Original marker type expressions are retained in generated blank variable
// declarations. This both preserves their exact source nodes for tooling and
// keeps imports such as github.com/gox-lang/gox semantically used after the
// function result itself is lowered.
func lowerSemanticGoxFunctions(file *ast.File, origins *OriginTable) []SemanticGoxReturn {
	return lowerSemanticGoxFunctionsWithBareMarkers(file, origins, false)
}

// lowerSemanticGoxFunctionsForProjectionFixtures retains the original
// BuildSemanticProjection contract for synthetic semantic-architecture
// fixtures that declare marker names locally. Production ParseModule paths use
// lowerSemanticGoxFunctions above and therefore remain import-identity strict.
func lowerSemanticGoxFunctionsForProjectionFixtures(file *ast.File, origins *OriginTable) []SemanticGoxReturn {
	return lowerSemanticGoxFunctionsWithBareMarkers(file, origins, true)
}

func lowerSemanticGoxFunctionsWithBareMarkers(file *ast.File, origins *OriginTable, allowBareMarkers bool) []SemanticGoxReturn {
	if file == nil {
		return nil
	}

	var (
		lowered     []SemanticGoxReturn
		markerTypes []ast.Expr
	)

	ast.Inspect(file, func(node ast.Node) bool {
		var functionType *ast.FuncType
		var body *ast.BlockStmt
		switch function := node.(type) {
		case *ast.FuncDecl:
			functionType = function.Type
			body = function.Body
		case *ast.FuncLit:
			functionType = function.Type
			body = function.Body
		default:
			return true
		}
		if functionType == nil {
			return true
		}

		returnName := goxReturnNameFromResults(file, functionType.Results)
		if returnName == "" && allowBareMarkers {
			returnName = bareSemanticGoxReturnName(functionType.Results)
		}
		kind := goxReturnKindFromName(returnName)
		mode, ok := semanticGoxReturnMode(returnName, kind)
		if !ok {
			return true
		}

		if functionType.Results != nil {
			for _, field := range functionType.Results.List {
				if field != nil && field.Type != nil {
					markerTypes = append(markerTypes, field.Type)
				}
			}
		}

		originalResults := functionType.Results
		switch mode {
		case SemanticGoxReturnVoid:
			functionType.Results = nil
		case SemanticGoxReturnFlexibleRequired:
			functionType.Results = semanticAnyResults(originalResults, false, origins)
		case SemanticGoxReturnFlexibleOptional:
			functionType.Results = semanticAnyResults(originalResults, true, origins)
			appendSemanticBareReturn(body, originalResults, origins)
		}

		lowered = append(lowered, SemanticGoxReturn{
			Function:   node,
			ReturnName: returnName,
			Kind:       kind,
			Mode:       mode,
		})
		return true
	})

	appendSemanticMarkerTypeUses(file, markerTypes, origins)
	return lowered
}

func bareSemanticGoxReturnName(results *ast.FieldList) string {
	if results == nil || len(results.List) != 1 || results.List[0] == nil {
		return ""
	}
	ident, ok := results.List[0].Type.(*ast.Ident)
	if !ok || ident == nil || !isGoxSemanticReturnMarker(ident.Name) {
		return ""
	}
	return ident.Name
}

func semanticGoxReturnMode(returnName string, kind GoxReturnKind) (SemanticGoxReturnMode, bool) {
	switch returnName {
	case "Page", "Action":
		return SemanticGoxReturnFlexibleOptional, true
	case "WebSocket", "Channel":
		return SemanticGoxReturnVoid, true
	case "Component", "Handler", "Html", "HTML", "Text", "JSON", "API", "Binary", "Blob", "File", "Image", "Video", "EventReply":
		return SemanticGoxReturnFlexibleRequired, true
	}
	if kind != GoxReturnNone {
		return SemanticGoxReturnFlexibleRequired, true
	}
	return "", false
}

func semanticAnyResults(results *ast.FieldList, allowBare bool, origins *OriginTable) *ast.FieldList {
	field := &ast.Field{Type: ast.NewIdent("any")}
	var source ast.Node
	if results != nil && len(results.List) > 0 {
		original := results.List[0]
		source = original
		if original != nil && len(original.Names) > 0 {
			// Reuse the source identifiers so named-result references keep exact
			// provenance and remain in scope with their semantic type (any).
			field.Names = original.Names
		}
	}
	if len(field.Names) == 0 && allowBare {
		// A blank named result permits a bare return without introducing a
		// source-visible identifier or a possible name collision.
		field.Names = []*ast.Ident{ast.NewIdent("_")}
	}
	lowered := &ast.FieldList{List: []*ast.Field{field}}
	if origins != nil && source != nil {
		origins.InheritTree(lowered, source)
	}
	return lowered
}

func appendSemanticBareReturn(body *ast.BlockStmt, source ast.Node, origins *OriginTable) {
	if body == nil {
		return
	}
	statement := &ast.ReturnStmt{}
	body.List = append(body.List, statement)
	if origins != nil && source != nil {
		origins.InheritTree(statement, source)
	}
}

func appendSemanticMarkerTypeUses(file *ast.File, markerTypes []ast.Expr, origins *OriginTable) {
	if file == nil || len(markerTypes) == 0 {
		return
	}
	for _, markerType := range markerTypes {
		if markerType == nil {
			continue
		}
		declaration := &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{&ast.ValueSpec{
				Names: []*ast.Ident{ast.NewIdent("_")},
				Type:  markerType,
			}},
		}
		file.Decls = append(file.Decls, declaration)
		if origins != nil {
			origins.InheritTree(declaration, markerType)
		}
	}
}
