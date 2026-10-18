package lsp

import (
	"strings"
	"testing"
)

func TestHoverAndDefinitionResolveTypedFieldsAndMethods(t *testing.T) {
	source := `module profile
type User struct { Name string }
func (user User) GetName() string { return user.Name }
func (user *User) setName(name string) {
	user.Name = name
	_ = user.GetName()
}
`
	document := Document{URI: "file:///project/profile.gox", Path: "/project/profile.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	pointerReceiver := strings.Index(source, "user.Name =") + 1
	assertLocalHover(t, server, document, pointerReceiver, "var user *User")
	if hover := hoverAtOffset(t, server, document, strings.Index(source, "user.Name =")+len("user.")+1); hover == nil || !strings.Contains(hover.Contents.Value, "field Name string") {
		t.Fatalf("field hover = %#v", hover)
	}
	assertDefinitionOffset(t, server, document, strings.Index(source, "user.Name =")+len("user.")+1, strings.Index(source, "Name string"))
	if hover := hoverAtOffset(t, server, document, strings.LastIndex(source, "GetName")+1); hover == nil || !strings.Contains(hover.Contents.Value, "func (user User) GetName() string") {
		t.Fatalf("method hover = %#v", hover)
	}
	assertDefinitionOffset(t, server, document, strings.LastIndex(source, "GetName")+1, strings.Index(source, "GetName"))
}
