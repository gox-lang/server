package gox

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
)

// collectGoSemanticTokens classifies the Go portion of a GOX module while the
// parsed tree still represents user source. Generated and anchored nodes are
// deliberately excluded by requiring an exact source origin.
func collectGoSemanticTokens(source string, file *ast.File, origins *OriginTable) []SemanticToken {
	if file == nil || origins == nil {
		return nil
	}

	kinds := make(map[*ast.Ident]SemanticTokenKind)
	objectKinds := make(map[*ast.Object]SemanticTokenKind)
	imports := make(map[string]struct{})

	mark := func(ident *ast.Ident, kind SemanticTokenKind) {
		if ident == nil {
			return
		}
		kinds[ident] = kind
		if ident.Obj != nil {
			objectKinds[ident.Obj] = kind
		}
	}
	markFields := func(fields *ast.FieldList, kind SemanticTokenKind) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			for _, name := range field.Names {
				mark(name, kind)
			}
		}
	}

	for _, spec := range file.Imports {
		name := ""
		if spec.Name != nil {
			name = spec.Name.Name
			if name != "_" && name != "." {
				mark(spec.Name, SemanticNamespace)
			}
		} else if path, err := strconv.Unquote(spec.Path.Value); err == nil {
			name = filepath.Base(path)
		}
		if name != "" && name != "_" && name != "." {
			imports[name] = struct{}{}
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncDecl:
			kind := SemanticFunction
			if value.Recv != nil {
				kind = SemanticMethod
				markFields(value.Recv, SemanticParameter)
			}
			mark(value.Name, kind)
			markFields(value.Type.Params, SemanticParameter)
			markFields(value.Type.Results, SemanticParameter)
		case *ast.FuncType:
			markFields(value.Params, SemanticParameter)
			markFields(value.Results, SemanticParameter)
		case *ast.GenDecl:
			for _, spec := range value.Specs {
				switch item := spec.(type) {
				case *ast.TypeSpec:
					kind := SemanticType
					switch item.Type.(type) {
					case *ast.StructType:
						kind = SemanticStruct
					case *ast.InterfaceType:
						kind = SemanticInterface
					}
					mark(item.Name, kind)
				case *ast.ValueSpec:
					kind := SemanticVariable
					if value.Tok == token.CONST {
						kind = SemanticEnumMember
					}
					for _, name := range item.Names {
						mark(name, kind)
					}
				}
			}
		case *ast.StructType:
			if value.Fields != nil {
				for _, field := range value.Fields.List {
					for _, name := range field.Names {
						mark(name, SemanticProperty)
					}
				}
			}
		case *ast.InterfaceType:
			if value.Methods != nil {
				for _, method := range value.Methods.List {
					for _, name := range method.Names {
						mark(name, SemanticMethod)
					}
				}
			}
		case *ast.AssignStmt:
			if value.Tok == token.DEFINE {
				for _, expression := range value.Lhs {
					if ident, ok := expression.(*ast.Ident); ok && ident.Name != "_" {
						mark(ident, SemanticVariable)
					}
				}
			}
		case *ast.RangeStmt:
			if value.Tok == token.DEFINE {
				if ident, ok := value.Key.(*ast.Ident); ok && ident.Name != "_" {
					mark(ident, SemanticVariable)
				}
				if ident, ok := value.Value.(*ast.Ident); ok && ident.Name != "_" {
					mark(ident, SemanticVariable)
				}
			}
		case *ast.LabeledStmt:
			mark(value.Label, SemanticLabel)
		case *ast.BranchStmt:
			mark(value.Label, SemanticLabel)
		case *ast.CallExpr:
			switch function := value.Fun.(type) {
			case *ast.Ident:
				if function.Obj == nil {
					mark(function, SemanticFunction)
				}
			case *ast.SelectorExpr:
				kind := SemanticMethod
				if qualifier, ok := function.X.(*ast.Ident); ok {
					if _, imported := imports[qualifier.Name]; imported {
						kind = SemanticFunction
						mark(qualifier, SemanticNamespace)
					}
				}
				mark(function.Sel, kind)
			}
		case *ast.SelectorExpr:
			if _, exists := kinds[value.Sel]; !exists {
				mark(value.Sel, SemanticProperty)
			}
			if qualifier, ok := value.X.(*ast.Ident); ok {
				if _, imported := imports[qualifier.Name]; imported {
					mark(qualifier, SemanticNamespace)
				}
			}
		}
		return true
	})

	var tokens []SemanticToken
	seen := make(map[[2]int]struct{})
	appendNode := func(node ast.Node, length int, kind SemanticTokenKind) {
		if node == nil || length <= 0 || kind == "" {
			return
		}
		origin, ok := origins.Get(node)
		if !ok || !origin.Exact() {
			return
		}
		offset := origin.Source.Range.Start
		key := [2]int{offset, length}
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		tokens = append(tokens, semanticTokenAtOffset(source, offset, length, kind))
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			kind, ok := kinds[value]
			if !ok && value.Obj != nil {
				kind, ok = objectKinds[value.Obj]
			}
			if !ok {
				kind = semanticKindForObject(value.Obj)
			}
			appendNode(value, len(value.Name), kind)
		case *ast.BasicLit:
			kind := SemanticNumber
			if value.Kind == token.STRING || value.Kind == token.CHAR {
				kind = SemanticString
			}
			appendNode(value, len(value.Value), kind)
		case *ast.Comment:
			appendNode(value, len(value.Text), SemanticComment)
		}
		return true
	})
	return tokens
}

func semanticKindForObject(object *ast.Object) SemanticTokenKind {
	if object == nil {
		return ""
	}
	switch object.Kind {
	case ast.Con:
		return SemanticEnumMember
	case ast.Typ:
		return SemanticType
	case ast.Var:
		return SemanticVariable
	case ast.Fun:
		return SemanticFunction
	case ast.Lbl:
		return SemanticLabel
	default:
		return ""
	}
}
