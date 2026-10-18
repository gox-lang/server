package lsp

import (
	"errors"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"unicode"
	"unicode/utf8"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"
)

// semanticSourceError is a go/types error whose generated semantic position
// maps exactly back to user-authored GOX source. Errors that only point at
// projection scaffolding are deliberately excluded: diagnostics must never
// expose generated implementation details.
type semanticSourceError struct {
	Message string
	Offset  int
}

func semanticSourceErrors(module *goxcompiler.Module) []semanticSourceError {
	if module == nil || module.Semantic == nil || module.Semantic.FileSet == nil || module.Semantic.SourceMap == nil {
		return nil
	}
	result := make([]semanticSourceError, 0, len(module.Semantic.Errors))
	for _, err := range module.Semantic.Errors {
		position, ok := semanticTypeErrorPosition(err)
		if !ok || !position.IsValid() {
			continue
		}
		file := module.Semantic.FileSet.File(position)
		if file == nil {
			continue
		}
		generated := file.Offset(position)
		original, mapped := module.Semantic.SourceMap.OriginalPosition(generated)
		if !mapped || !original.Exact || original.URI != module.Path {
			continue
		}
		result = append(result, semanticSourceError{Message: semanticTypeErrorMessage(err), Offset: original.Offset})
	}
	return result
}

func semanticTypeErrorPosition(err error) (token.Pos, bool) {
	var value types.Error
	if errors.As(err, &value) {
		return value.Pos, true
	}
	var pointer *types.Error
	if errors.As(err, &pointer) && pointer != nil {
		return pointer.Pos, true
	}
	return token.NoPos, false
}

func semanticTypeErrorMessage(err error) string {
	var value types.Error
	if errors.As(err, &value) {
		return value.Msg
	}
	var pointer *types.Error
	if errors.As(err, &pointer) && pointer != nil {
		return pointer.Msg
	}
	return err.Error()
}

func semanticErrorTouches(errors []semanticSourceError, source goxmap.Range) bool {
	if !source.Valid() {
		return false
	}
	for _, err := range errors {
		if err.Offset >= source.Start && err.Offset <= source.End {
			return true
		}
	}
	return false
}

func semanticTypeAt(module *goxcompiler.Module, expression ast.Expr, resultIndex int) (types.Type, bool) {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || expression == nil {
		return nil, false
	}
	value, ok := module.Semantic.Info.Types[expression]
	if !ok || value.Type == nil {
		return nil, false
	}
	typ := value.Type
	if strings.Contains(types.TypeString(typ, nil), "invalid type") {
		return nil, false
	}
	if tuple, ok := typ.(*types.Tuple); ok {
		if resultIndex < 0 || resultIndex >= tuple.Len() {
			return nil, false
		}
		typ = tuple.At(resultIndex).Type()
	}
	return typ, typ != nil
}

func semanticTypeText(module *goxcompiler.Module, typ types.Type) string {
	if typ == nil {
		return ""
	}
	return types.TypeString(typ, semanticCompletionQualifier(module))
}

func semanticTypeAssignable(actual, expected types.Type) bool {
	if actual == nil || expected == nil {
		return true
	}
	return types.AssignableTo(actual, expected)
}

func semanticBasicInfo(typ types.Type) (types.BasicInfo, bool) {
	if typ == nil {
		return 0, false
	}
	basic, ok := typ.Underlying().(*types.Basic)
	if !ok {
		return 0, false
	}
	return basic.Info(), true
}

func semanticBooleanType(typ types.Type) bool {
	info, ok := semanticBasicInfo(typ)
	return ok && info&types.IsBoolean != 0
}

func semanticOrderedType(typ types.Type) bool {
	info, ok := semanticBasicInfo(typ)
	return ok && info&types.IsOrdered != 0
}

// semanticDiagnosticRange returns a stable source range beginning at offset.
// For identifier-oriented checker errors this expands to the identifier token;
// other callers normally replace it with an exact NodeOrigin range.
func semanticDiagnosticRange(document Document, offset int) Range {
	if offset < 0 || offset > len(document.Text) {
		return pointRange(Position{})
	}
	end := offset
	if offset < len(document.Text) {
		value, size := utf8.DecodeRuneInString(document.Text[offset:])
		if value == '_' || unicode.IsLetter(value) {
			end = offset + size
			for end < len(document.Text) {
				value, size = utf8.DecodeRuneInString(document.Text[end:])
				if value != '_' && !unicode.IsLetter(value) && !unicode.IsDigit(value) {
					break
				}
				end += size
			}
		} else {
			end = offset + size
		}
	}
	startPosition, startErr := document.Position(offset)
	endPosition, endErr := document.Position(end)
	if startErr != nil || endErr != nil {
		return pointRange(Position{})
	}
	return Range{Start: startPosition, End: endPosition}
}

func semanticErrorMessagesAt(errors []semanticSourceError, offset int) []string {
	var messages []string
	for _, err := range errors {
		if err.Offset == offset {
			messages = append(messages, err.Message)
		}
	}
	return messages
}

func semanticMessagesContain(messages []string, fragments ...string) bool {
	for _, message := range messages {
		for _, fragment := range fragments {
			if strings.Contains(message, fragment) {
				return true
			}
		}
	}
	return false
}

// semanticNameDiagnostics owns ordinary Go-name and selector errors. The
// checker error stream decides whether a source position is invalid; Info and
// the semantic AST decide whether that invalid name is a qualifier, selector,
// or ordinary identifier. Template expressions participate automatically
// because F2/F5 project them into the same semantic tree.
func (s *Server) semanticNameDiagnostics(document Document, module *goxcompiler.Module) []Diagnostic {
	if module == nil || module.Semantic == nil || module.Semantic.File == nil || module.Semantic.Info == nil || module.Semantic.Origins == nil {
		return nil
	}
	errors := semanticSourceErrors(module)

	excluded := make(map[*ast.Ident]bool)
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SelectorExpr:
			excluded[value.Sel] = true
		case *ast.KeyValueExpr:
			if key, ok := value.Key.(*ast.Ident); ok {
				excluded[key] = true
			}
		case *ast.ImportSpec:
			if value.Name != nil {
				excluded[value.Name] = true
			}
		}
		return true
	})

	seen := make(map[int]bool)
	var diagnostics []Diagnostic

	// Selectors go first so an unresolved qualifier is reported once at X and a
	// missing member is reported at Sel.
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		origin, exact := module.Semantic.Origins.Get(selector)
		if !exact || !origin.Exact() {
			return true
		}
		// Imported GOX packages are type-checked from the filesystem, while the
		// editor's WorkspaceIndex owns the current open/unsaved version. Overlay
		// package-member existence from that source metadata so diagnostics react
		// immediately to edits in an imported .gox file. This is also the only
		// reliable way to distinguish an unexported top-level GOX declaration from
		// a declaration that does not exist, because the synthetic go/types package
		// intentionally exposes only exported names.
		if resolution, handled := s.semanticGoxPackageSelectorResolution(document, module, selector); handled {
			if resolution == selectorExists {
				return true
			}
			selOrigin, ok := module.Semantic.Origins.Get(selector.Sel)
			if !ok || !selOrigin.Exact() || seen[selOrigin.Source.Range.Start] {
				return true
			}
			seen[selOrigin.Source.Range.Start] = true
			qualifierText := semanticSelectorQualifierText(document, module, selector)
			name := qualifierText + "." + selector.Sel.Name
			code := "undefined-selector"
			message := "undefined selector: " + name
			if resolution == selectorUnexported {
				code = "unexported-selector"
				message = "cannot refer to unexported selector: " + name
			}
			start, startErr := document.Position(selOrigin.Source.Range.Start)
			end, endErr := document.Position(selOrigin.Source.Range.End)
			if startErr == nil && endErr == nil {
				diagnostics = append(diagnostics, Diagnostic{
					Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
					Code: code, Source: "gox", Message: message,
				})
			}
			return true
		}

		qualifier, qualifierOK := selector.X.(*ast.Ident)
		if qualifierOK && module.Semantic.Info.ObjectOf(qualifier) == nil {
			qualifierOrigin, ok := module.Semantic.Origins.Get(qualifier)
			if !ok || !qualifierOrigin.Exact() {
				return true
			}
			messages := semanticErrorMessagesAt(errors, qualifierOrigin.Source.Range.Start)
			if !semanticMessagesContain(messages, "undefined:") {
				return true
			}
			if seen[qualifierOrigin.Source.Range.Start] {
				return true
			}
			seen[qualifierOrigin.Source.Range.Start] = true
			diagnostics = append(diagnostics, Diagnostic{
				Range: semanticDiagnosticRange(document, qualifierOrigin.Source.Range.Start), Severity: diagnosticSeverityError,
				Code: "undefined-identifier", Source: "gox", Message: "undefined: " + qualifier.Name,
			})
			return true
		}

		if module.Semantic.Info.ObjectOf(selector.Sel) != nil || module.Semantic.Info.Selections[selector] != nil {
			return true
		}
		selOrigin, ok := module.Semantic.Origins.Get(selector.Sel)
		if !ok || !selOrigin.Exact() {
			return true
		}
		messages := semanticErrorMessagesAt(errors, selOrigin.Source.Range.Start)
		if len(messages) == 0 && !semanticErrorTouches(errors, origin.Source.Range) {
			return true
		}
		if seen[selOrigin.Source.Range.Start] {
			return true
		}
		seen[selOrigin.Source.Range.Start] = true
		qualifierText := semanticSelectorQualifierText(document, module, selector)
		name := qualifierText + "." + selector.Sel.Name
		code := "undefined-selector"
		message := "undefined selector: " + name
		if semanticMessagesContain(messages, "unexported", "not exported") || semanticSelectorIsUnexported(module, selector) {
			code = "unexported-selector"
			message = "cannot refer to unexported selector: " + name
		}
		start, startErr := document.Position(selOrigin.Source.Range.Start)
		end, endErr := document.Position(selOrigin.Source.Range.End)
		if startErr == nil && endErr == nil {
			diagnostics = append(diagnostics, Diagnostic{
				Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
				Code: code, Source: "gox", Message: message,
			})
		}
		return true
	})

	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name == "_" || excluded[ident] || module.Semantic.Info.ObjectOf(ident) != nil || typesUniverseIdentifier(ident.Name) {
			return true
		}
		origin, exact := module.Semantic.Origins.Get(ident)
		if !exact || !origin.Exact() || seen[origin.Source.Range.Start] {
			return true
		}
		messages := semanticErrorMessagesAt(errors, origin.Source.Range.Start)
		if !semanticMessagesContain(messages, "undefined:") {
			return true
		}
		seen[origin.Source.Range.Start] = true
		start, startErr := document.Position(origin.Source.Range.Start)
		end, endErr := document.Position(origin.Source.Range.End)
		if startErr == nil && endErr == nil {
			diagnostics = append(diagnostics, Diagnostic{
				Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
				Code: "undefined-identifier", Source: "gox", Message: "undefined: " + ident.Name,
			})
		}
		return true
	})
	return diagnostics
}

func (s *Server) semanticGoxPackageSelectorResolution(document Document, module *goxcompiler.Module, selector *ast.SelectorExpr) (selectorResolution, bool) {
	if s == nil || module == nil || module.Semantic == nil || module.Semantic.Info == nil || selector == nil || selector.Sel == nil {
		return selectorUnknownReceiver, false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return selectorUnknownReceiver, false
	}
	if _, ok := module.Semantic.Info.ObjectOf(qualifier).(*types.PkgName); !ok {
		return selectorUnknownReceiver, false
	}
	_, targetURI, ok := s.resolveImportedModule(document, qualifier.Name)
	if !ok {
		return selectorUnknownReceiver, false
	}
	// Only claim the overlay when the target is actually represented by the
	// workspace index. Otherwise the semantic package remains authoritative.
	if _, indexed := s.symbols.Document(targetURI); !indexed {
		return selectorUnknownReceiver, false
	}
	for _, candidate := range s.symbols.DocumentSymbols(targetURI) {
		symbol := candidate.Symbol
		if symbol.Name != selector.Sel.Name || symbol.Container != "" || !moduleMemberKind(symbol.Kind) {
			continue
		}
		if !symbol.Exported {
			return selectorUnexported, true
		}
		return selectorExists, true
	}
	return selectorMissing, true
}

func semanticSelectorIsUnexported(module *goxcompiler.Module, selector *ast.SelectorExpr) bool {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || selector == nil || selector.Sel == nil {
		return false
	}
	if qualifier, ok := selector.X.(*ast.Ident); ok {
		if packageName, ok := module.Semantic.Info.ObjectOf(qualifier).(*types.PkgName); ok && packageName.Imported() != nil {
			object := packageName.Imported().Scope().Lookup(selector.Sel.Name)
			return object != nil && !object.Exported()
		}
	}
	typ := module.Semantic.Info.TypeOf(selector.X)
	if typ == nil {
		return false
	}
	base := typ
	if pointer, ok := base.(*types.Pointer); ok {
		base = pointer.Elem()
	}
	if named, ok := base.(*types.Named); ok {
		if structure, ok := named.Underlying().(*types.Struct); ok {
			for index := 0; index < structure.NumFields(); index++ {
				field := structure.Field(index)
				if field.Name() == selector.Sel.Name && !field.Exported() {
					return module.Semantic.Package == nil || field.Pkg() == nil || field.Pkg().Path() != module.Semantic.Package.Path()
				}
			}
		}
	}
	methodSet := types.NewMethodSet(typ)
	for index := 0; index < methodSet.Len(); index++ {
		method := methodSet.At(index).Obj()
		if method.Name() == selector.Sel.Name && !method.Exported() {
			return module.Semantic.Package == nil || method.Pkg() == nil || method.Pkg().Path() != module.Semantic.Package.Path()
		}
	}
	if _, ok := typ.(*types.Pointer); !ok {
		methodSet = types.NewMethodSet(types.NewPointer(typ))
		for index := 0; index < methodSet.Len(); index++ {
			method := methodSet.At(index).Obj()
			if method.Name() == selector.Sel.Name && !method.Exported() {
				return module.Semantic.Package == nil || method.Pkg() == nil || method.Pkg().Path() != module.Semantic.Package.Path()
			}
		}
	}
	return false
}

func semanticSelectorQualifierText(document Document, module *goxcompiler.Module, selector *ast.SelectorExpr) string {
	if module != nil && module.Semantic != nil && module.Semantic.Origins != nil && selector != nil {
		if origin, ok := module.Semantic.Origins.Get(selector.X); ok && origin.Exact() {
			start, end := origin.Source.Range.Start, origin.Source.Range.End
			if start >= 0 && end >= start && end <= len(document.Text) {
				return strings.TrimSpace(document.Text[start:end])
			}
		}
	}
	if ident, ok := selector.X.(*ast.Ident); ok {
		return ident.Name
	}
	return strings.TrimSpace(types.ExprString(selector.X))
}
