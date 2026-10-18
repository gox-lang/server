package lsp

import (
	"strings"

	goxcompiler "github.com/gox-lang/server/internal/goxcompiler"
	"github.com/gox-lang/server/internal/goxmap"
)

// cursorContextKind is the single language/context classification used by LSP
// features at one source offset. Phase 8 centralizes this decision; later
// phases migrate each feature's semantic behavior independently.
type cursorContextKind uint8

const (
	cursorContextUnknown cursorContextKind = iota
	cursorContextGo
	cursorContextTemplateExpression
	cursorContextTemplateMarkup
	cursorContextTemplateSigil
	cursorContextTemplateAttribute
	cursorContextImportPath
	cursorContextClientSource
	cursorContextEmbeddedStyle
	cursorContextEmbeddedScript
)

type cursorContext struct {
	Kind cursorContextKind

	Lexical      lexicalCompletionContext
	HasLexical   bool
	Qualified    qualifiedCompletionContext
	HasQualified bool

	Sigil        templateSigilContext
	HasSigil     bool
	Attribute    templateAttributeCompletionContext
	HasAttribute bool
	ImportPath   importPathCompletionContext
	HasImport    bool
	ClientSource clientSourceCompletionContext
	HasClient    bool
}

func (c cursorContext) goCode() bool {
	return c.Kind == cursorContextGo || c.Kind == cursorContextTemplateExpression
}

func (c cursorContext) template() bool {
	switch c.Kind {
	case cursorContextTemplateExpression, cursorContextTemplateMarkup, cursorContextTemplateSigil, cursorContextTemplateAttribute, cursorContextEmbeddedStyle, cursorContextEmbeddedScript:
		return true
	default:
		return false
	}
}

func (c cursorContext) passiveEmbedded() bool {
	return c.Kind == cursorContextEmbeddedStyle || c.Kind == cursorContextEmbeddedScript
}

// contextAt is the Phase 8 authority for deciding which language surface owns
// the cursor. Parsed TemplateOccurrences win when the current snapshot is
// valid; lexical fallbacks remain for incomplete editor text that cannot yet be
// represented by the parser.
func (s *Server) contextAt(document Document, offset int) cursorContext {
	var module *goxcompiler.Module
	if snapshot := s.analysisSnapshot(document); snapshot != nil && snapshot.Err == nil {
		module = snapshot.Module
	}
	return classifyCursorContext(document.Text, offset, module)
}

func classifyCursorContext(source string, offset int, module *goxcompiler.Module) cursorContext {
	if offset < 0 || offset > len(source) {
		return cursorContext{Kind: cursorContextUnknown}
	}

	// A parsed template occurrence is the strongest source of truth. It wins
	// over text heuristics such as client/import so strings or text inside a
	// template cannot accidentally switch language surfaces.
	if occurrence := templateOccurrenceAt(module, offset); occurrence != nil {
		if kind, ok := templateEmbeddedContentContextAt(source, offset, occurrence.Source.Range.Start, occurrence.Source.Range.End, true); ok {
			return cursorContext{Kind: kind}
		}
		if templateOccurrenceExpressionAt(occurrence, offset) {
			return goCursorContext(cursorContextTemplateExpression, source, offset)
		}
		if sigil, ok := rawTemplateSigilContextAt(source, offset); ok && rawTemplateSigilValueContext(source, sigil, offset) && templateSigilOwnsCursor(source, sigil, true) {
			return cursorContext{Kind: cursorContextTemplateSigil, Sigil: sigil, HasSigil: true}
		}
		if attribute, ok := rawTemplateAttributeContextAt(source, offset); ok {
			return cursorContext{Kind: cursorContextTemplateAttribute, Attribute: attribute, HasAttribute: true}
		}
		return cursorContext{Kind: cursorContextTemplateMarkup}
	}

	// A broken/incomplete template may not exist in the current Module. Preserve
	// the passive embedded boundary lexically too: GOX deliberately provides no
	// language intelligence inside <style> or <script> content, even for {{ }}.
	if kind, ok := templateEmbeddedContentContextAt(source, offset, 0, len(source), false); ok {
		return cursorContext{Kind: kind}
	}

	// Keep a narrow source fallback so editor completion still understands
	// {{ ... }} while the user types outside passive embedded content. Expression
	// ownership is checked before quoted-path heuristics because it is an explicit
	// GOX language boundary.
	if rawTemplateExpressionContextAt(source, offset) {
		return goCursorContext(cursorContextTemplateExpression, source, offset)
	}

	// These contexts live inside quoted text and therefore must be decided
	// before ordinary Go lexical state, but only after template ownership.
	if client, ok := rawClientSourceCompletionContextAt(source, offset); ok {
		return cursorContext{Kind: cursorContextClientSource, ClientSource: client, HasClient: true}
	}
	if imported, ok := rawImportPathCompletionContextAt(source, offset); ok {
		return cursorContext{Kind: cursorContextImportPath, ImportPath: imported, HasImport: true}
	}
	if imported, ok := rawBareImportPathCompletionContextAt(source, offset); ok {
		return cursorContext{Kind: cursorContextImportPath, ImportPath: imported, HasImport: true}
	}
	if sigil, ok := rawTemplateSigilContextAt(source, offset); ok && rawTemplateSigilValueContext(source, sigil, offset) && templateSigilOwnsCursor(source, sigil, rawTemplateMarkupContextAt(source, sigil.ReplaceStart)) {
		return cursorContext{Kind: cursorContextTemplateSigil, Sigil: sigil, HasSigil: true}
	}
	if attribute, ok := rawTemplateAttributeContextAt(source, offset); ok {
		return cursorContext{Kind: cursorContextTemplateAttribute, Attribute: attribute, HasAttribute: true}
	}
	if rawTemplateMarkupContextAt(source, offset) {
		return cursorContext{Kind: cursorContextTemplateMarkup}
	}

	if rawGoLexicalContext(source, offset) {
		return goCursorContext(cursorContextGo, source, offset)
	}
	return cursorContext{Kind: cursorContextUnknown}
}

func templateEmbeddedContentContextAt(source string, offset, start, end int, trustedTemplate bool) (cursorContextKind, bool) {
	if offset < start || offset > end || start < 0 || end < start || end > len(source) {
		return cursorContextUnknown, false
	}
	for cursor := start; cursor < end; {
		relative := strings.IndexByte(source[cursor:end], '<')
		if relative < 0 {
			break
		}
		tagStart := cursor + relative
		tag, tagEnd, ok := scanTemplateTag(source, tagStart)
		if !ok || tagEnd > end {
			cursor = tagStart + 1
			continue
		}
		name := strings.ToLower(tag.Name)
		if tag.Closing || tag.SelfClosed || name != "style" && name != "script" {
			cursor = tagEnd
			continue
		}
		if !trustedTemplate && !rawTemplateValuePosition(source, tagStart) && !rawTemplateMarkupContextAt(source, tagStart) {
			cursor = tagEnd
			continue
		}

		closeStart, closeEnd := embeddedClosingTag(source, tagEnd, end, name)
		contentEnd := end
		if closeStart >= 0 {
			contentEnd = closeStart
		}
		// For an unterminated embedded block, EOF is still part of its content:
		// the editor cursor commonly sits exactly at len(source) while typing.
		// For a terminated block, the closing tag itself is markup again.
		insideContent := offset >= tagEnd && (offset < contentEnd || closeStart < 0 && offset == contentEnd)
		if insideContent {
			if name == "style" {
				return cursorContextEmbeddedStyle, true
			}
			return cursorContextEmbeddedScript, true
		}
		if closeStart < 0 {
			break
		}
		cursor = closeEnd
	}
	return cursorContextUnknown, false
}

func embeddedClosingTag(source string, start, end int, name string) (int, int) {
	for cursor := start; cursor < end; {
		relative := strings.IndexByte(source[cursor:end], '<')
		if relative < 0 {
			break
		}
		tagStart := cursor + relative
		tag, tagEnd, ok := scanTemplateTag(source, tagStart)
		if ok && tagEnd <= end && tag.Closing && strings.EqualFold(tag.Name, name) {
			return tagStart, tagEnd
		}
		cursor = tagStart + 1
	}
	return -1, -1
}

func goCursorContext(kind cursorContextKind, source string, offset int) cursorContext {
	context := cursorContext{Kind: kind}
	if lexical, ok := rawLexicalCompletionContextAt(source, offset); ok {
		context.Lexical, context.HasLexical = lexical, true
	}
	if qualified, ok := rawQualifiedCompletionContextAt(source, offset); ok {
		context.Qualified, context.HasQualified = qualified, true
	}
	return context
}

func templateOccurrenceAt(module *goxcompiler.Module, offset int) *goxcompiler.TemplateOccurrence {
	if module == nil {
		return nil
	}
	for index := range module.TemplateOccurrences {
		occurrence := &module.TemplateOccurrences[index]
		if sourceRangeContainsOffset(occurrence.Source, offset) {
			return occurrence
		}
	}
	return nil
}

func templateOccurrenceExpressionAt(occurrence *goxcompiler.TemplateOccurrence, offset int) bool {
	if occurrence == nil || occurrence.Template == nil {
		return false
	}
	return templateNodeExpressionAt(occurrence.Template.Root, offset)
}

func templateNodeExpressionAt(node goxcompiler.TemplateNode, offset int) bool {
	switch value := node.(type) {
	case *goxcompiler.ExprNode:
		return sourceRangeContainsOffset(value.Source, offset)
	case *goxcompiler.FragmentNode:
		return templateChildrenExpressionAt(value.Children, offset)
	case *goxcompiler.ElementNode:
		return templateAttrsExpressionAt(value.Attrs, offset) || templateChildrenExpressionAt(value.Children, offset)
	case *goxcompiler.DirectiveNode:
		return templateAttrsExpressionAt(value.Attrs, offset) || templateChildrenExpressionAt(value.Children, offset)
	case *goxcompiler.ComponentNode:
		return templateAttrsExpressionAt(value.Attrs, offset) || templateChildrenExpressionAt(value.Children, offset)
	case *goxcompiler.ClientNode:
		return templateAttrsExpressionAt(value.Attrs, offset) || templateChildrenExpressionAt(value.Children, offset)
	default:
		return false
	}
}

func templateChildrenExpressionAt(children []goxcompiler.TemplateNode, offset int) bool {
	for _, child := range children {
		if templateNodeExpressionAt(child, offset) {
			return true
		}
	}
	return false
}

func templateAttrsExpressionAt(attributes []goxcompiler.TemplateAttr, offset int) bool {
	for _, attribute := range attributes {
		if attribute.ExprSource.URI != "" && sourceRangeContainsOffset(attribute.ValueSource, offset) {
			return true
		}
	}
	return false
}

func sourceRangeContainsOffset(source goxmap.SourceRange, offset int) bool {
	return source.URI != "" && source.Range.Start <= offset && offset < source.Range.End
}

func templateSigilOwnsCursor(source string, sigil templateSigilContext, inTemplateMarkup bool) bool {
	if sigil.Sigil != '&' {
		return true
	}
	// `&` is a Go unary operator. Only the explicit <& template sigil owns
	// the cursor outside a known template expression; foo(&value) must remain
	// Go and {{ &value }} is already classified as TemplateExpression first.
	return inTemplateMarkup || sigil.ReplaceStart >= 0 && sigil.ReplaceStart < len(source) && source[sigil.ReplaceStart] == '<'
}

func rawTemplateExpressionContextAt(source string, offset int) bool {
	if !rawInsideTemplateExpression(source, offset) {
		return false
	}
	open := strings.LastIndex(source[:offset], "{{")
	if open < 0 {
		return false
	}
	return incompleteTemplateTagStart(source, open) >= 0 || rawTemplateMarkupContextAt(source, open)
}

func rawTemplateValuePosition(source string, offset int) bool {
	if expectedTemplateValue(source, offset) {
		return true
	}
	// Context ownership must recognize syntax the compiler already accepts,
	// even when Completion intentionally chooses not to offer a template
	// snippet there. A return expression is therefore a template-value
	// boundary regardless of the declared result type.
	before := skipSourceSpacesBackward(source, offset)
	const keyword = "return"
	if before < len(keyword) || source[before-len(keyword):before] != keyword {
		return false
	}
	return before == len(keyword) || !completionIdentifierRuneBefore(source, before-len(keyword))
}

// rawTemplateMarkupContextAt is intentionally stricter than the old
// insideOpenTemplateMarkup helper. A root tag only starts template markup when
// it appears in a syntactic template-value position; nested tags then inherit
// that context. This prevents ordinary Go such as `a < b > c` from becoming
// template markup merely because it contains angle brackets.
func rawTemplateMarkupContextAt(source string, offset int) bool {
	if offset < 0 || offset > len(source) {
		return false
	}
	depth := 0
	for index := 0; index < offset; index++ {
		if source[index] != '<' {
			continue
		}
		endRelative := strings.IndexByte(source[index:offset], '>')
		if endRelative < 0 {
			if depth > 0 || rawTemplateValuePosition(source, index) {
				return true
			}
			break
		}
		end := index + endRelative
		body := strings.TrimSpace(source[index+1 : end])
		if strings.HasPrefix(body, "!") || strings.HasPrefix(body, "?") {
			index = end
			continue
		}
		closing := strings.HasPrefix(body, "/")
		if closing {
			if depth > 0 {
				depth--
			}
			index = end
			continue
		}
		name := templateMarkupTagName(body)
		fragment := body == ""
		if !fragment && name == "" {
			continue
		}
		if depth == 0 && !rawTemplateValuePosition(source, index) {
			index = end
			continue
		}
		selfClosing := strings.HasSuffix(body, "/") || (!fragment && htmlVoidElements[name])
		if !selfClosing {
			depth++
		}
		index = end
	}
	return depth > 0
}
