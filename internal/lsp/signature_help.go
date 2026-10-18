package lsp

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
)

type SignatureHelpParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature int                    `json:"activeSignature,omitempty"`
	ActiveParameter int                    `json:"activeParameter,omitempty"`
}

type SignatureInformation struct {
	Label      string                 `json:"label"`
	Parameters []ParameterInformation `json:"parameters,omitempty"`
}

type ParameterInformation struct {
	Label string `json:"label"`
}

type signatureCallContext struct {
	Qualifier      string
	Name           string
	OpenOffset     int
	ActiveArgument int
}

type signatureCandidate struct {
	Label      string
	Parameters []goxcompiler.SourceParameter
	Results    []string
	Type       string
	Component  bool
}

func (s *Server) signatureHelp(document Document, position Position) *SignatureHelp {
	offset, err := document.Offset(position)
	if err != nil {
		return nil
	}
	if !s.contextAt(document, offset).goCode() {
		return nil
	}
	context, ok := signatureCallAt(document.Text, offset)
	if !ok {
		return nil
	}
	candidate, semanticOK := s.semanticSignatureCandidate(document, context.OpenOffset)
	if !semanticOK {
		candidate, ok = s.resolveSignatureCandidate(document, context)
	} else {
		ok = true
	}
	if !ok || signatureCandidateIsComponent(candidate) {
		return nil
	}
	parameters := make([]ParameterInformation, 0, len(candidate.Parameters))
	for _, parameter := range candidate.Parameters {
		parameters = append(parameters, ParameterInformation{Label: signatureParameterLabel(parameter)})
	}
	active := context.ActiveArgument
	if len(parameters) > 0 && active >= len(parameters) {
		active = len(parameters) - 1
	}
	if len(parameters) == 0 {
		active = 0
	}
	return &SignatureHelp{
		Signatures:      []SignatureInformation{{Label: candidate.Label, Parameters: parameters}},
		ActiveParameter: active,
	}
}

func (s *Server) resolveSignatureCandidate(document Document, context signatureCallContext) (signatureCandidate, bool) {
	if context.Qualifier == "" {
		if local, ok := s.symbols.LocalSymbolAt(document.URI, context.Name, context.OpenOffset); ok {
			if candidate, ok := signatureCandidateFromFuncType(context.Name, local.Type); ok {
				return candidate, true
			}
		}
		for _, indexed := range s.symbols.DocumentSymbols(document.URI) {
			symbol := indexed.Symbol
			if symbol.Name == context.Name && symbol.Container == "" && symbol.Kind == goxcompiler.SymbolFunction {
				return signatureCandidateFromSourceSymbol(symbol), true
			}
			if symbol.Name == context.Name && symbol.Container == "" && symbol.Kind == goxcompiler.SymbolVariable {
				if candidate, ok := signatureCandidateFromFuncType(context.Name, symbol.Type); ok {
					return candidate, true
				}
			}
		}
		if candidate, ok := goBuiltinSignatureCandidate(context.Name); ok {
			return candidate, true
		}
		return signatureCandidate{}, false
	}

	if item, ok := s.typedValueMember(document, context.Qualifier, context.Name, context.OpenOffset); ok {
		if item.Kind == 2 || strings.HasPrefix(strings.TrimSpace(item.Detail), "func ") {
			return signatureCandidateFromCompletion(item), true
		}
		if candidate, ok := signatureCandidateFromFuncType(context.Name, item.CandidateType); ok {
			return candidate, true
		}
		return signatureCandidate{}, false
	}
	if imported := s.importedSymbol(document, context.Qualifier, context.Name); imported != nil {
		if imported.Symbol.Kind == goxcompiler.SymbolFunction {
			return signatureCandidateFromSourceSymbol(imported.Symbol), true
		}
		if imported.Symbol.Kind == goxcompiler.SymbolVariable {
			if candidate, ok := signatureCandidateFromFuncType(context.Name, imported.Symbol.Type); ok {
				return candidate, true
			}
		}
	}
	if _, packageInfo, alias, _, ok := s.resolveGoPackageImport(document, context.Qualifier); ok && alias == context.Qualifier {
		for _, exported := range packageInfo.Exports {
			if exported.Name != context.Name {
				continue
			}
			if exported.Kind == "func" {
				return signatureCandidate{Label: exported.Detail, Parameters: exported.Parameters, Results: exported.Results, Type: exported.Type}, true
			}
			if exported.Kind == "var" {
				if candidate, ok := signatureCandidateFromFuncType(context.Name, exported.Type); ok {
					return candidate, true
				}
			}
		}
	}
	return signatureCandidate{}, false
}

func signatureCandidateFromSourceSymbol(symbol goxcompiler.SourceSymbol) signatureCandidate {
	return signatureCandidate{Label: symbol.Detail, Parameters: symbol.Parameters, Results: symbol.Results, Type: symbol.Type}
}

func signatureCandidateFromCompletion(item CompletionItem) signatureCandidate {
	return signatureCandidate{Label: item.Detail, Parameters: item.CandidateParameters, Results: item.CandidateResults, Type: item.CandidateType}
}

func signatureCandidateIsComponent(candidate signatureCandidate) bool {
	return candidate.Component || completionTypeIsComponent(candidate.Type) || len(candidate.Results) == 1 && completionTypeIsComponent(candidate.Results[0])
}

func signatureParameterLabel(parameter goxcompiler.SourceParameter) string {
	if parameter.Name == "" {
		return parameter.Type
	}
	return parameter.Name + " " + parameter.Type
}

func signatureCandidateFromFuncType(name, typeName string) (signatureCandidate, bool) {
	expression, err := parser.ParseExpr(strings.TrimSpace(typeName))
	if err != nil {
		return signatureCandidate{}, false
	}
	function, ok := expression.(*ast.FuncType)
	if !ok {
		return signatureCandidate{}, false
	}
	parameters := signatureSourceParameters(function.Params)
	results := signatureSourceResultTypes(function.Results)
	detail := signatureFormatNode(function)
	if !strings.HasPrefix(detail, "func") {
		return signatureCandidate{}, false
	}
	detail = "func " + name + strings.TrimPrefix(detail, "func")
	typeName = ""
	if len(results) == 1 {
		typeName = results[0]
	}
	return signatureCandidate{Label: detail, Parameters: parameters, Results: results, Type: typeName}, true
}

func signatureSourceParameters(fields *ast.FieldList) []goxcompiler.SourceParameter {
	if fields == nil {
		return nil
	}
	var result []goxcompiler.SourceParameter
	for _, field := range fields.List {
		typeName := signatureFormatNode(field.Type)
		if len(field.Names) == 0 {
			result = append(result, goxcompiler.SourceParameter{Type: typeName})
			continue
		}
		for _, name := range field.Names {
			if name.Name != "_" {
				result = append(result, goxcompiler.SourceParameter{Name: name.Name, Type: typeName})
			}
		}
	}
	return result
}

func signatureSourceResultTypes(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	var result []string
	for _, field := range fields.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for index := 0; index < count; index++ {
			result = append(result, signatureFormatNode(field.Type))
		}
	}
	return result
}

func signatureFormatNode(node any) string {
	var output bytes.Buffer
	if err := format.Node(&output, token.NewFileSet(), node); err != nil {
		return ""
	}
	return output.String()
}

type signatureDelimiter struct {
	value  byte
	offset int
	commas int
}

func signatureCallAt(source string, offset int) (signatureCallContext, bool) {
	if offset < 0 || offset > len(source) {
		return signatureCallContext{}, false
	}
	stack := make([]signatureDelimiter, 0)
	state := byte(0)
	escaped := false
	for index := 0; index < offset; index++ {
		value := source[index]
		switch state {
		case 1: // line comment
			if value == '\n' {
				state = 0
			}
			continue
		case 2: // block comment
			if value == '*' && index+1 < offset && source[index+1] == '/' {
				state = 0
				index++
			}
			continue
		case 3, 4: // quoted string or rune
			if escaped {
				escaped = false
			} else if value == '\\' {
				escaped = true
			} else if state == 3 && value == '"' || state == 4 && value == '\'' {
				state = 0
			}
			continue
		case 5: // raw string
			if value == '`' {
				state = 0
			}
			continue
		}
		if value == '/' && index+1 < offset {
			if source[index+1] == '/' {
				state = 1
				index++
				continue
			}
			if source[index+1] == '*' {
				state = 2
				index++
				continue
			}
		}
		switch value {
		case '"':
			state = 3
		case '\'':
			state = 4
		case '`':
			state = 5
		case '(', '[', '{':
			stack = append(stack, signatureDelimiter{value: value, offset: index})
		case ')', ']', '}':
			opening := byte('(')
			if value == ']' {
				opening = '['
			} else if value == '}' {
				opening = '{'
			}
			for len(stack) > 0 {
				last := len(stack) - 1
				found := stack[last].value == opening
				stack = stack[:last]
				if found {
					break
				}
			}
		case ',':
			if len(stack) > 0 && stack[len(stack)-1].value == '(' {
				stack[len(stack)-1].commas++
			}
		}
	}
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index].value != '(' {
			continue
		}
		qualifier, name, _ := signatureCallableBefore(source, stack[index].offset)
		return signatureCallContext{Qualifier: qualifier, Name: name, OpenOffset: stack[index].offset, ActiveArgument: stack[index].commas}, true
	}
	return signatureCallContext{}, false
}

func signatureCallableBefore(source string, open int) (string, string, bool) {
	end := skipSourceSpacesBackward(source, open)
	start := identifierStartBefore(source, end)
	if start == end {
		return "", "", false
	}
	name := source[start:end]
	if signatureCallKeyword(name) {
		return "", "", false
	}
	before := skipSourceSpacesBackward(source, start)
	if before == 0 || source[before-1] != '.' {
		return "", name, true
	}
	qualifierEnd := skipSourceSpacesBackward(source, before-1)
	qualifierStart := identifierStartBefore(source, qualifierEnd)
	if qualifierStart == qualifierEnd {
		return "", "", false
	}
	return source[qualifierStart:qualifierEnd], name, true
}

func signatureCallKeyword(name string) bool {
	switch name {
	case "if", "for", "switch", "select", "func", "return", "var", "const", "type", "import", "package":
		return true
	}
	return false
}
