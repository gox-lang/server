package gox

import (
	"bytes"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestBindModuleFieldsInDeferredAndGoCalls(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "test.go", `package p
func (m *Module) Run() {
 defer lock.Unlock()
 go consume(value)
 callback := func() { defer lock.Unlock(); go consume(value) }
 _ = callback
 {
  lock := local
  defer lock.Unlock()
 }
}`, 0)
	if err != nil {
		t.Fatal(err)
	}
	fields := []VarDecl{{Name: "lock"}, {Name: "value"}}
	if err := BindModuleFieldIdentifiers(file, fields, "m"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), file); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Count(got, "defer m.lock.Unlock()") != 2 || strings.Count(got, "go consume(m.value)") != 2 || !strings.Contains(got, "defer lock.Unlock()") {
		t.Fatalf("incorrect field binding:\n%s", got)
	}
}
