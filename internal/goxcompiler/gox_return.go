package gox

import (
	"go/ast"
	pathpkg "path"
	"strconv"
)

const (
	goxSDKImportPath    = "github.com/gox-lang/gox"
	goxServerImportPath = "github.com/gox-lang/gox/server"
)

// canonicalizeGoxSDKRuntimeImport lowers a source-level SDK alias to the
// canonical runtime qualifier used by generated GOX code. The semantic model
// is built before this pass, so editor/source semantics keep the user's alias.
func canonicalizeGoxSDKRuntimeImport(file *ast.File) {
	if file == nil {
		return
	}

	var sdkImport *ast.ImportSpec
	alias := ""
	for _, spec := range file.Imports {
		if spec == nil || spec.Path == nil {
			continue
		}
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != goxSDKImportPath {
			continue
		}
		if spec.Name == nil {
			return
		}
		alias = spec.Name.Name
		if alias == "" || alias == "gox" || alias == "." || alias == "_" {
			return
		}
		sdkImport = spec
		break
	}
	if sdkImport == nil {
		return
	}

	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector == nil {
			return true
		}
		qualifier, ok := selector.X.(*ast.Ident)
		if !ok || qualifier == nil || qualifier.Name != alias || qualifier.Obj != nil {
			return true
		}
		qualifier.Name = "gox"
		return true
	})

	// A nil import name restores the package's canonical binding: gox.
	sdkImport.Name = nil
}

// goxSourceTypeIdentity resolves one source-level qualified type expression to
// the concrete import path that owns it. GOX semantic markers are language
// contracts attached to the SDK packages, not to arbitrary source identifiers
// that happen to share names such as Component or Page.
func goxSourceTypeIdentity(file *ast.File, expr ast.Expr) (importPath, name string, ok bool) {
	for {
		star, isStar := expr.(*ast.StarExpr)
		if !isStar {
			break
		}
		expr = star.X
	}

	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector == nil || selector.Sel == nil {
		return "", "", false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	if !ok || qualifier == nil {
		return "", "", false
	}

	path, ok := goxImportPathForQualifier(file, qualifier.Name)
	if !ok {
		return "", "", false
	}
	return path, selector.Sel.Name, true
}

func goxImportPathForQualifier(file *ast.File, qualifier string) (string, bool) {
	if file == nil || qualifier == "" {
		return "", false
	}
	for _, spec := range file.Imports {
		if spec == nil || spec.Path == nil {
			continue
		}
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}

		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
		} else {
			switch importPath {
			case goxSDKImportPath:
				name = "gox"
			case goxServerImportPath:
				name = "server"
			default:
				// For non-GOX imports the path base is a conservative package-name
				// approximation. Its main purpose here is to avoid treating a real
				// foreign `gox`/`server` binding as the implicit GOX namespace below.
				name = pathpkg.Base(importPath)
			}
		}
		if name == "." || name == "_" {
			continue
		}
		if name == qualifier {
			return importPath, true
		}
	}

	// Editor projections intentionally accept the canonical GOX qualifiers in
	// incomplete source while the import is still being typed. This preserves
	// the long-standing tolerant-tooling contract without reintroducing lexical
	// recognition for bare local names such as `Component` or `Page`.
	switch qualifier {
	case "gox":
		return goxSDKImportPath, true
	case "server":
		return goxServerImportPath, true
	default:
		return "", false
	}
}

// goxReturnNameFromResults returns the semantic GOX marker name carried by a
// single result field, but only when that field is owned by the GOX SDK import.
func goxReturnNameFromResults(file *ast.File, results *ast.FieldList) string {
	if results == nil || len(results.List) != 1 {
		return ""
	}
	path, name, ok := goxSourceTypeIdentity(file, results.List[0].Type)
	if !ok || path != goxSDKImportPath || !isGoxSemanticReturnMarker(name) {
		return ""
	}
	return name
}

func isGoxSemanticReturnMarker(name string) bool {
	switch name {
	case "Page", "Component", "Action", "WebSocket", "Channel", "EventReply":
		return true
	default:
		return false
	}
}

func goxReturnKindFromName(name string) GoxReturnKind {
	switch name {
	case "Page", "Component":
		return GoxReturnHTML
	case "Action":
		return GoxReturnAction
	case "WebSocket":
		return GoxReturnWebSocket
	case "Channel":
		return GoxReturnChannel
	case "EventReply":
		return GoxReturnEventReply
	default:
		return GoxReturnNone
	}
}
