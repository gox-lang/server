package gox

import "testing"

func TestPhase5ComponentSemanticAcceptsNamedLocalProps(t *testing.T) {
	_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Card(title string, count int) gox.Component {
	return <article>{{ title }} {{ count }}</article>
}
func GET(ctx *gox.Context) gox.Page {
	return <&Card title="Hello" count={{ 5 }} />
}
`)
	if err != nil {
		t.Fatalf("ParseModule() error = %v", err)
	}
}

func TestPhase5ComponentSemanticAcceptsSDKAlias(t *testing.T) {
	_, err := ParseModule("pages/index.gox", `module index
import gx "github.com/gox-lang/gox"
func Card(title string) gx.Component {
	return <article>{{ title }}</article>
}
func GET(ctx *gx.Context) gx.Page {
	return <&Card title="Hello" />
}
`)
	if err != nil {
		t.Fatalf("ParseModule() with SDK alias error = %v", err)
	}
}

func TestPhase5ComponentSemanticAcceptsFinalChildrenContract(t *testing.T) {
	_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Panel(title string, children gox.Component) gox.Component {
	return <section><h1>{{ title }}</h1><&children /></section>
}
func GET(ctx *gox.Context) gox.Page {
	return <&Panel title="Hello"><p>Body</p></&Panel>
}
`)
	if err != nil {
		t.Fatalf("ParseModule() valid children contract error = %v", err)
	}
}

func TestPhase5ComponentSemanticChildrenUsesSDKTypeIdentity(t *testing.T) {
	_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
type Component func(gox.Writer)
func Panel(children Component) gox.Component {
	return <section />
}
func GET(ctx *gox.Context) gox.Page {
	return <&Panel children={{ nil }} />
}
`)
	if err != nil {
		t.Fatalf("local Component homonym must remain an ordinary prop: %v", err)
	}
}

func TestPhase5ComponentSemanticToolingAllowsIncompleteProps(t *testing.T) {
	const src = `module index
import "github.com/gox-lang/gox"
func Card(name string, count int, children gox.Component) gox.Component {
	return <article>{{ name }} {{ count }}<&children /></article>
}
func GET(ctx *gox.Context) gox.Page {
	return <&Card name="George" />
}
`
	if _, err := ParseModuleForTooling("pages/index.gox", src); err != nil {
		t.Fatalf("ParseModuleForTooling() incomplete component error = %v", err)
	}
	if _, err := ParseModule("pages/index.gox", src); err == nil {
		t.Fatal("ParseModule() must remain strict for missing required props")
	}
}
