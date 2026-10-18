package lsp

import "testing"

func TestDiagnosticsClauseLocalsAndCompositeConversions(t *testing.T) {
	source := `module page
func Build(value string, ch chan string) {
 switch {
 case len(value) > 0:
  prefix := "<"
  name := value[1:]
  _, _ = prefix, name
 default:
  prefix := ">"
  _ = prefix
 }
 select {
 case value := <-ch:
  name := value
  _ = name
 default:
  name := "none"
  _ = name
 }
 _ = len([]rune(value))
 _ = []byte(value)
 _ = (*int)(nil)
 _ = (chan int)(nil)
 _ = (map[string]int)(nil)
 _ = (func())(nil)
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range server.diagnostics(document) {
		if diagnostic.Code == "no-new-variables" || diagnostic.Code == "non-callable" {
			t.Errorf("false diagnostic: %#v", diagnostic)
		}
	}
}

func TestDiagnosticsStillRejectInvalidClauseDeclarationsAndSliceCalls(t *testing.T) {
	source := `module page
func Build(value string) {
 switch {
 case value != "":
  name := value
  name := value
  _ = name
 }
 runes := []rune(value)
 runes(value)
}
`
	document := Document{URI: "file:///workspace/page.gox", Path: "/workspace/page.gox", Text: source}
	server := NewServer()
	if err := server.symbols.UpdateOpen(document); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, diagnostic := range server.diagnostics(document) {
		found[diagnostic.Code] = true
	}
	for _, code := range []string{"no-new-variables", "non-callable"} {
		if !found[code] {
			t.Errorf("missing real diagnostic %s", code)
		}
	}
}
