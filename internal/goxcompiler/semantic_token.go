package gox

import (
	"sort"
	"strings"
)

type SemanticTokenKind string

const (
	SemanticNamespace          SemanticTokenKind = "namespace"
	SemanticType               SemanticTokenKind = "type"
	SemanticStruct             SemanticTokenKind = "struct"
	SemanticInterface          SemanticTokenKind = "interface"
	SemanticParameter          SemanticTokenKind = "parameter"
	SemanticVariable           SemanticTokenKind = "variable"
	SemanticProperty           SemanticTokenKind = "property"
	SemanticEnumMember         SemanticTokenKind = "enumMember"
	SemanticFunction           SemanticTokenKind = "function"
	SemanticMethod             SemanticTokenKind = "method"
	SemanticLabel              SemanticTokenKind = "label"
	SemanticComment            SemanticTokenKind = "comment"
	SemanticString             SemanticTokenKind = "string"
	SemanticNumber             SemanticTokenKind = "number"
	SemanticKeyword            SemanticTokenKind = "keyword"
	SemanticGoxModuleKeyword   SemanticTokenKind = "goxModuleKeyword"
	SemanticGoxClientKeyword   SemanticTokenKind = "goxClientKeyword"
	SemanticGoxClientFramework SemanticTokenKind = "goxClientFramework"
	SemanticGoxTag             SemanticTokenKind = "goxTag"
	SemanticGoxAttribute       SemanticTokenKind = "goxAttribute"
	SemanticGoxAttributeValue  SemanticTokenKind = "goxAttributeValue"
	SemanticGoxDirective       SemanticTokenKind = "goxDirective"
	SemanticGoxComponent       SemanticTokenKind = "goxComponent"
	SemanticGoxClient          SemanticTokenKind = "goxClient"
)

type SemanticToken struct {
	Line   int
	Column int
	Length int
	Kind   SemanticTokenKind
}

func CollectSemanticTokens(path, source string) ([]SemanticToken, error) {
	module, err := ParseModule(path, source)
	if err != nil {
		return nil, err
	}
	tokens := moduleSemanticTokens(source, module)
	tokens = append(tokens, module.GoSemanticTokens...)
	for _, template := range module.Templates {
		line, column := sourceLineColumn(source, template.Start)
		lexer := NewTemplateLexerAt(template.Raw, path, line, column)
		templateTokens, lexErr := lexer.LexAll()
		if lexErr != nil {
			return nil, lexErr
		}
		tokens = append(tokens, classifyTemplateTokens(templateTokens)...)
	}
	sort.SliceStable(tokens, func(i, j int) bool {
		if tokens[i].Line != tokens[j].Line {
			return tokens[i].Line < tokens[j].Line
		}
		return tokens[i].Column < tokens[j].Column
	})
	return tokens, nil
}

func moduleSemanticTokens(source string, module *Module) []SemanticToken {
	var tokens []SemanticToken
	if moduleOffset := findModuleKeywordOffset(source); moduleOffset >= 0 {
		tokens = append(tokens, semanticTokenAtOffset(source, moduleOffset, len("module"), SemanticGoxModuleKeyword))
		nameOffset := moduleOffset + len("module")
		for nameOffset < len(source) && (source[nameOffset] == ' ' || source[nameOffset] == '\t') {
			nameOffset++
		}
		tokens = append(tokens, semanticTokenAtOffset(source, nameOffset, len(module.DeclaredName), SemanticNamespace))
	}
	for _, client := range module.Clients {
		start := client.SourceOffset
		nameStart := client.NameOffset
		if start >= 0 {
			tokens = append(tokens, semanticTokenAtOffset(source, start, len("client"), SemanticGoxClientKeyword))
		}
		if nameStart >= 0 {
			tokens = append(tokens, semanticTokenAtOffset(source, nameStart, len(client.Name), SemanticGoxClient))
			frameworkStart := client.FrameworkOffset
			tokens = append(tokens, semanticTokenAtOffset(source, frameworkStart, len(client.Framework), SemanticGoxClientFramework))
		}
	}
	return tokens
}

func findModuleKeywordOffset(source string) int {
	for lineStart := 0; lineStart <= len(source); {
		lineEnd := strings.IndexByte(source[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(source)
		} else {
			lineEnd += lineStart
		}
		content := source[lineStart:lineEnd]
		trimmed := strings.TrimLeft(content, " \t\r")
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			if strings.HasPrefix(trimmed, "module ") {
				return lineStart + len(content) - len(trimmed)
			}
			return -1
		}
		if lineEnd == len(source) {
			break
		}
		lineStart = lineEnd + 1
	}
	return -1
}

func classifyTemplateTokens(input []TemplateToken) []SemanticToken {
	var tokens []SemanticToken
	expectTagName := false
	expectedTagKind := SemanticTokenKind("")
	for _, token := range input {
		switch token.Kind {
		case TokenOpenTagStart:
			expectTagName = true
			expectedTagKind = SemanticGoxTag
		case TokenDirectiveTagStart:
			expectTagName = true
			expectedTagKind = SemanticGoxDirective
		case TokenComponentTagStart:
			expectTagName = true
			expectedTagKind = SemanticGoxComponent
		case TokenClientTagStart:
			expectTagName = true
			expectedTagKind = SemanticGoxClient
		case TokenCloseTagStart:
			expectTagName = true
			expectedTagKind = ""
		case TokenTagEnd, TokenSelfCloseEnd:
			expectTagName = false
			expectedTagKind = ""
		case TokenIdent:
			kind := SemanticGoxAttribute
			startDelta := 0
			if expectTagName {
				if expectedTagKind != "" {
					kind = expectedTagKind
				} else {
					kind, startDelta = templateTagSemanticKind(token.Lit)
				}
				expectTagName = false
				expectedTagKind = ""
			} else if strings.HasPrefix(token.Lit, ":") {
				kind = SemanticGoxDirective
			}
			length := len(token.Lit) - startDelta
			if length > 0 {
				tokens = append(tokens, SemanticToken{Line: token.Line - 1, Column: token.Col - 1 + startDelta, Length: length, Kind: kind})
			}
		case TokenString:
			if !strings.ContainsAny(token.Lit, "\r\n") {
				tokens = append(tokens, SemanticToken{Line: token.Line - 1, Column: token.Col - 1, Length: len(token.Lit) + 2, Kind: SemanticGoxAttributeValue})
			}
		}
	}
	return tokens
}

func templateTagSemanticKind(name string) (SemanticTokenKind, int) {
	switch {
	case strings.HasPrefix(name, ":"):
		return SemanticGoxDirective, 1
	case strings.HasPrefix(name, "&"):
		return SemanticGoxComponent, 1
	case strings.HasPrefix(name, "@"):
		return SemanticGoxClient, 1
	default:
		return SemanticGoxTag, 0
	}
}

func semanticTokenAtOffset(source string, offset, length int, kind SemanticTokenKind) SemanticToken {
	line, column := lineColAt(source, offset)
	return SemanticToken{Line: line - 1, Column: column - 1, Length: length, Kind: kind}
}
