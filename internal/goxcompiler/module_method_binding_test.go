package gox

import (
	"bytes"
	"go/format"
	"go/token"
	"strings"
	"testing"
)

func TestTemplateComponentCallBindsModuleMethod(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

var hola gox.Component = <span>Mundo</span>

func GET(ctx *gox.Context) gox.Page {
	return <h1>Hello <&hola /> <&Comp /></h1>
}

func Comp() gox.Component {
	return <span>Hola desde Comp</span>
}
`

	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), mod.File); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, "m.hola(ctx.Writer)") {
		t.Fatalf("field component call was not receiver-qualified:\n%s", out)
	}

	if !strings.Contains(out, "m.Comp(ctx.Writer)") {
		t.Fatalf("module component function call was not receiver-qualified:\n%s", out)
	}

	if strings.Contains(out, "\n\tComp(ctx.Writer)") {
		t.Fatalf("module component function leaked as global call:\n%s", out)
	}
}

func TestModuleMethodReferenceBindsAsValue(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	ctx.Response.DoneWith(403, Comp)
	return <h1>Hello</h1>
}

func Comp() gox.Component {
	return <span>Forbidden</span>
}
`

	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), mod.File); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, "ctx.Response.DoneWith(403, m.Comp)") {
		t.Fatalf("module method reference was not receiver-qualified:\n%s", out)
	}

	if strings.Contains(out, "ctx.Response.DoneWith(403, Comp)") {
		t.Fatalf("module method reference leaked as global identifier:\n%s", out)
	}
}

func TestTemplateRejectsNormalFunctionAsComponent(t *testing.T) {
	src := `module main

import "github.com/gox-lang/gox"

var hola gox.Component = <span>Mundo</span>

func GET(ctx *gox.Context) gox.Page {
	ctx.Response.DoneWith(403, hola)
	return <h1>Hello <&hola /> <&Comp /></h1>
}

func Comp() string {
	return "hola mundo"
}
`

	_, err := ParseModule("pages/index.gox", src)
	if err == nil {
		t.Fatal("expected normal function component usage to fail")
	}

	if !strings.Contains(err.Error(), "function Comp is not a Gox component") {
		t.Fatalf("unexpected error: %v", err)
	}
}
