package gox

import (
	"strings"

	"github.com/gox-lang/server/internal/goxmap"
)

type SourceReferenceKind string

const (
	ReferenceComponent SourceReferenceKind = "component"
	ReferenceClient    SourceReferenceKind = "client"
)

// SourceReference records a GOX-specific symbol use in original source.
type SourceReference struct {
	Name      string
	Qualifier string
	Kind      SourceReferenceKind
	Source    goxmap.SourceRange
	Container string
}

func collectTemplateSourceReferences(source, path string, templates []TemplateReturn) []SourceReference {
	index := goxmap.NewLineIndex(source)
	var references []SourceReference
	for _, template := range templates {
		if template.Template == nil || template.Template.Root == nil {
			continue
		}
		collectTemplateNodeReferences(index, path, template.FuncName, template.Template.Root, &references)
	}
	return references
}

func collectTemplateNodeReferences(index *goxmap.LineIndex, path, container string, node TemplateNode, references *[]SourceReference) {
	if node == nil {
		return
	}
	switch value := node.(type) {
	case *FragmentNode:
		collectTemplateChildrenReferences(index, path, container, value.Children, references)
	case *ElementNode:
		collectTemplateChildrenReferences(index, path, container, value.Children, references)
	case *DirectiveNode:
		collectTemplateChildrenReferences(index, path, container, value.Children, references)
	case *ComponentNode:
		appendTemplateReference(index, path, container, value.Name, ReferenceComponent, value.Pos, references)
		collectTemplateChildrenReferences(index, path, container, value.Children, references)
	case *ClientNode:
		appendTemplateReference(index, path, container, value.Name, ReferenceClient, value.Pos, references)
		collectTemplateChildrenReferences(index, path, container, value.Children, references)
	}
}

func collectTemplateChildrenReferences(index *goxmap.LineIndex, path, container string, children []TemplateNode, references *[]SourceReference) {
	for _, child := range children {
		collectTemplateNodeReferences(index, path, container, child, references)
	}
}

func appendTemplateReference(index *goxmap.LineIndex, path, container, fullName string, kind SourceReferenceKind, position TemplatePosition, references *[]SourceReference) {
	if position.Line <= 0 || position.Column <= 0 || fullName == "" {
		return
	}
	offset, err := index.ByteOffset(goxmap.BytePosition{Line: position.Line - 1, Column: position.Column - 1})
	if err != nil {
		return
	}
	name := fullName
	qualifier := ""
	if separator := strings.LastIndexByte(fullName, '.'); separator >= 0 {
		qualifier = fullName[:separator]
		name = fullName[separator+1:]
		offset += separator + 1
	}
	if name == "" {
		return
	}
	*references = append(*references, SourceReference{
		Name:      name,
		Qualifier: qualifier,
		Kind:      kind,
		Source:    goxmap.SourceRange{URI: path, Range: goxmap.Range{Start: offset, End: offset + len(name)}},
		Container: container,
	})
}
