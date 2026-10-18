package lsp

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

func (s *Server) callArgumentDiagnostics(document Document) []Diagnostic {
	module, ok := s.semanticModule(document)
	if !ok || module.Semantic == nil || module.Semantic.File == nil || module.Semantic.Info == nil || module.Semantic.Origins == nil {
		return nil
	}
	var diagnostics []Diagnostic
	ast.Inspect(module.Semantic.File, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		callOrigin, exact := module.Semantic.Origins.Get(call)
		if !exact || !callOrigin.Exact() {
			return true
		}
		context, contextOK := sourceSignatureCallContext(document, module, call)
		if !contextOK {
			context = semanticCallContext(module, call)
		}

		// GOX Components are source-callable only through component syntax. The
		// semantic signature still exists after lowering, so preserve that GOX
		// language boundary as metadata rather than treating it as Go callability.
		if candidate, found := semanticCallCandidate(module, call); found && signatureCandidateIsComponent(candidate) {
			typeName := candidate.Type
			if typeName == "" && len(candidate.Results) == 1 {
				typeName = candidate.Results[0]
			}
			diagnostics = append(diagnostics, nonCallableDiagnostic(document, module, call,
				signatureContextName(context), typeName)...)
			return true
		}

		if semanticCallIsConversionOrBuiltin(module, call) {
			return true
		}
		signature := semanticCallSignature(module, call)
		if signature == nil {
			if typ := module.Semantic.Info.TypeOf(call.Fun); typ != nil && !strings.Contains(types.TypeString(typ, nil), "invalid type") {
				diagnostics = append(diagnostics, nonCallableDiagnostic(document, module, call,
					signatureContextName(context), semanticTypeText(module, typ))...)
			}
			return true
		}
		diagnostics = append(diagnostics, s.validateSemanticCallArguments(document, module, call, context, signature)...)
		return true
	})
	return diagnostics
}

func semanticCallSignature(module *goxcompiler.Module, call *ast.CallExpr) *types.Signature {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || call == nil {
		return nil
	}
	typ := module.Semantic.Info.TypeOf(call.Fun)
	if typ == nil {
		return nil
	}
	signature, _ := typ.Underlying().(*types.Signature)
	return signature
}

func semanticCallIsConversionOrBuiltin(module *goxcompiler.Module, call *ast.CallExpr) bool {
	if module == nil || module.Semantic == nil || module.Semantic.Info == nil || call == nil {
		return false
	}
	// Composite and parenthesized type expressions are conversions too.
	if value, ok := module.Semantic.Info.Types[call.Fun]; ok && value.IsType() {
		return true
	}
	switch function := call.Fun.(type) {
	case *ast.Ident:
		object := module.Semantic.Info.ObjectOf(function)
		switch object.(type) {
		case *types.TypeName, *types.Builtin:
			return true
		}
	case *ast.SelectorExpr:
		if object := module.Semantic.Info.ObjectOf(function.Sel); object != nil {
			_, isType := object.(*types.TypeName)
			return isType
		}
	}
	return false
}

func (s *Server) validateSemanticCallArguments(document Document, module *goxcompiler.Module, call *ast.CallExpr, context signatureCallContext, signature *types.Signature) []Diagnostic {
	if signature == nil {
		return nil
	}
	parameters := signature.Params()
	want := parameters.Len()
	variadic := signature.Variadic()
	wantMin := want
	if variadic {
		wantMin--
	}
	have := len(call.Args)
	invalidCount := have < wantMin || !variadic && have > want || call.Ellipsis.IsValid() && have != want
	if invalidCount {
		return callArgumentCountDiagnostic(document, module, call, context, have, want, variadic)
	}

	var diagnostics []Diagnostic
	for index, argument := range call.Args {
		parameterIndex := index
		if parameterIndex >= want {
			parameterIndex = want - 1
		}
		if parameterIndex < 0 {
			continue
		}
		expected := parameters.At(parameterIndex).Type()
		if variadic && parameterIndex == want-1 {
			if slice, ok := expected.(*types.Slice); ok && !call.Ellipsis.IsValid() {
				expected = slice.Elem()
			}
		}
		contextText := fmt.Sprintf("in argument %d to %s", index+1, signatureContextName(context))
		diagnostics = append(diagnostics, s.semanticAssignmentTypeDiagnostic(document, module, argument, expected, 0, contextText)...)
	}
	return diagnostics
}

func nonCallableDiagnostic(document Document, module *goxcompiler.Module, call *ast.CallExpr, name, typeName string) []Diagnostic {
	origin, ok := moduleSourceOrigin(module, call.Fun)
	if !ok || !origin.Exact() {
		return nil
	}
	return nonCallableDiagnosticAtOffsets(document, origin.Source.Range.Start, origin.Source.Range.End, name, typeName)
}

func nonCallableDiagnosticAtOffsets(document Document, startOffset, endOffset int, name, typeName string) []Diagnostic {
	start, startErr := document.Position(startOffset)
	end, endErr := document.Position(endOffset)
	if startErr != nil || endErr != nil {
		return nil
	}
	if strings.TrimSpace(typeName) == "" {
		typeName = "unknown"
	}
	return []Diagnostic{{
		Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
		Code: "non-callable", Source: "gox",
		Message: fmt.Sprintf("invalid operation: cannot call non-function %s (type %s)", name, typeName),
	}}
}

func sourceSignatureCallContext(document Document, module *goxcompiler.Module, call *ast.CallExpr) (signatureCallContext, bool) {
	callOrigin, ok := moduleSourceOrigin(module, call)
	if !ok || !callOrigin.Exact() {
		return signatureCallContext{}, false
	}
	start := callOrigin.Source.Range.Start
	end := callOrigin.Source.Range.End
	if start < 0 || end > len(document.Text) || start >= end {
		return signatureCallContext{}, false
	}
	searchStart := start
	if funOrigin, found := moduleSourceOrigin(module, call.Fun); found && funOrigin.Exact() {
		searchStart = funOrigin.Source.Range.End
	}
	if searchStart < start || searchStart >= end {
		searchStart = start
	}
	relative := strings.IndexByte(document.Text[searchStart:end], '(')
	if relative < 0 {
		return signatureCallContext{}, false
	}
	open := searchStart + relative
	qualifier, name, ok := signatureCallableBefore(document.Text, open)
	if !ok {
		return signatureCallContext{}, false
	}
	return signatureCallContext{Qualifier: qualifier, Name: name, OpenOffset: open}, true
}

func callArgumentCountDiagnostic(document Document, module *goxcompiler.Module, call *ast.CallExpr, context signatureCallContext, have, want int, variadic bool) []Diagnostic {
	origin, ok := moduleSourceOrigin(module, call.Fun)
	if !ok || !origin.Exact() {
		origin, ok = moduleSourceOrigin(module, call)
	}
	if !ok || !origin.Exact() {
		return nil
	}
	start, startErr := document.Position(origin.Source.Range.Start)
	end, endErr := document.Position(origin.Source.Range.End)
	if startErr != nil || endErr != nil {
		return nil
	}
	wantText := fmt.Sprintf("%d", want)
	if variadic {
		wantText = fmt.Sprintf("at least %d", want-1)
	}
	return []Diagnostic{{
		Range: Range{Start: start, End: end}, Severity: diagnosticSeverityError,
		Code: "argument-count", Source: "gox",
		Message: fmt.Sprintf("wrong number of arguments in call to %s: have %d, want %s", signatureContextName(context), have, wantText),
	}}
}

func signatureContextName(context signatureCallContext) string {
	if context.Qualifier == "" {
		return context.Name
	}
	return context.Qualifier + "." + context.Name
}
