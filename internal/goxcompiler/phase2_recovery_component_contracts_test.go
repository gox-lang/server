package gox

import "testing"

func TestPhase2RecoveryPhase5ComponentSemanticContracts(t *testing.T) {

	t.Run("DuplicateProp", func(t *testing.T) {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Card(title string) gox.Component { return <article>{{ title }}</article> }
func GET(ctx *gox.Context) gox.Page { return <&Card title="A" title="B" /> }
`)
		phase2RequireErrorKind(t, err, "duplicate-component-prop")
	})

	t.Run("UnknownProp", func(t *testing.T) {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Card(title string) gox.Component { return <article>{{ title }}</article> }
func GET(ctx *gox.Context) gox.Page { return <&Card title="A" count={{ 1 }} /> }
`)
		phase2RequireErrorKind(t, err, "unknown-component-prop")
	})

	t.Run("MissingRequiredProp", func(t *testing.T) {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Card(title string, count int) gox.Component { return <article>{{ title }} {{ count }}</article> }
func GET(ctx *gox.Context) gox.Page { return <&Card title="A" /> }
`)
		phase2RequireErrorKind(t, err, "missing-component-prop")
	})

	t.Run("ChildrenRequireParameter", func(t *testing.T) {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Card(title string) gox.Component { return <article>{{ title }}</article> }
func GET(ctx *gox.Context) gox.Page { return <&Card title="A"><p>Body</p></&Card> }
`)
		phase2RequireErrorKind(t, err, "component-does-not-accept-children")
	})

	t.Run("ChildrenParameterMustBeLast", func(t *testing.T) {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func Panel(children gox.Component, title string) gox.Component {
	return <section><&children /><h1>{{ title }}</h1></section>
}
func GET(ctx *gox.Context) gox.Page { return <&Panel title="A"><p>Body</p></&Panel> }
`)
		phase2RequireErrorKind(t, err, "children-param-must-be-last")
	})
}

func TestPhase2RecoveryPhase6ComponentPropsBindByName(t *testing.T) {

	t.Run("PageTemplate", func(t *testing.T) {
		generated := phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <&Card count={{ 5 }} title="Hello" />
}

func Card(title string, count int) gox.Component {
	return <article>{{ title }} {{ count }}</article>
}
`,
		})
		phase2RequireGeneratedContains(t, generated, `m.Card(ctx.Writer, "Hello", 5)`)
	})

	t.Run("InlineTemplateValue", func(t *testing.T) {
		generated := phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "websocket/index.gox",
			source: `module websocket

import "github.com/gox-lang/gox"

func Card(title string, count int) gox.Component {
	return <article>{{ title }} {{ count }}</article>
}

func Index(socket *gox.Socket) gox.WebSocket {
	socket.Emit("card", <&Card count={{ 5 }} title="Hello" />)
}
`,
		})
		phase2RequireGeneratedContains(t, generated, `m.Card(goxWriter, "Hello", 5)`)
	})
}

func TestPhase2RecoveryPhase7ComponentChildrenContracts(t *testing.T) {

	t.Run("ChildrenOnly", func(t *testing.T) {
		phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <&Panel><p>Body</p></&Panel>
}

func Panel(children gox.Component) gox.Component {
	return <section><&children /></section>
}
`,
		})
	})

	t.Run("PropsAndChildren", func(t *testing.T) {
		generated := phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <&Panel title="Hello"><p>Body</p></&Panel>
}

func Panel(title string, children gox.Component) gox.Component {
	return <section><h1>{{ title }}</h1><&children /></section>
}
`,
		})
		phase2RequireGeneratedContains(t, generated, `m.Panel(ctx.Writer, "Hello", func(goxWriter gox.Writer)`)
	})

	t.Run("SelfClosingUsesChildrenFallback", func(t *testing.T) {
		phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <&Panel />
}

func Panel(children gox.Component) gox.Component {
	return <section><&children><p>Fallback</p></&children></section>
}
`,
		})
	})
}

func TestPhase2RecoveryPhase8ImportedAndInlineComponentContracts(t *testing.T) {

	t.Run("ImportedPropsBindByName", func(t *testing.T) {
		phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import UI "./ui.gox"
import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <&UI.Card count={{ 5 }} title="Hello" />
}
`,
			files: map[string]string{
				"pages/ui.gox": `module ui

import "github.com/gox-lang/gox"

func Card(title string, count int) gox.Component {
	return <article>{{ title }} {{ count }}</article>
}
`,
			},
		})
	})

	t.Run("ImportedMissingPropRejectedBeforeGoBuild", func(t *testing.T) {
		err := phase2CompileExpectError(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import UI "./ui.gox"
import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <&UI.Card title="Hello" />
}
`,
			files: map[string]string{
				"pages/ui.gox": `module ui

import "github.com/gox-lang/gox"

func Card(title string, count int) gox.Component {
	return <article>{{ title }} {{ count }}</article>
}
`,
			},
		})
		phase2RequireError(t, err)
	})

	t.Run("TypedInlineComponentCompiles", func(t *testing.T) {
		phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	var link gox.Component = <a href="/profile">Profile</a>
	return <&link />
}
`,
		})
	})

	t.Run("InlineComponentRejectsProps", func(t *testing.T) {
		_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page {
	link := <a href="/profile">Profile</a>
	return <&link class="primary" />
}
`)
		phase2RequireError(t, err)
	})
}

func TestPhase2RecoveryPhase9ComponentClientContextContract(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	phase2BundleSingleModuleAndGoTest(t, "pages/index.gox", `module index

import "github.com/gox-lang/gox"

client Menu react {
	src: "menu/menu.jsx"
}

func GET(ctx *gox.Context) gox.Page {
	return <&Navigation title="Main" />
}

func Navigation(title string) gox.Component {
	return <@Menu title={{ title }} />
}
`)
}
