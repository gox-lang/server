package gox

import (
	"go/ast"
	"testing"
)

func TestPhase2RecoveryPhase3SemanticMarkerIdentity(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	mod, err := ParseModule("pages/index.gox", `module index

type Component interface{}

func LocalOnly() Component { return nil }
`)
	if err != nil {
		t.Fatalf("local type homonym should parse as ordinary Go: %v", err)
	}
	if got := phase2ModuleFuncKind(t, mod, "LocalOnly"); got != FuncNormal {
		t.Fatalf("local Component homonym classified as %q, want %q", got, FuncNormal)
	}
}

func TestPhase2RecoveryPhase4SDKImportAliases(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	t.Run("PageAlias", func(t *testing.T) {
		phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import gx "github.com/gox-lang/gox"

func GET(ctx *gx.Context) gx.Page {
	return <main>Hello</main>
}
`,
		})
	})

	t.Run("ComponentAlias", func(t *testing.T) {
		phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import gx "github.com/gox-lang/gox"

func GET(ctx *gx.Context) gx.Page {
	return <&Card />
}

func Card() gx.Component {
	return <article>Card</article>
}
`,
		})
	})

	t.Run("ServerConfigAlias", func(t *testing.T) {
		mod, err := ParseModule("app/config.gox", `module app
import srv "github.com/gox-lang/gox/server"
func SetGoxConfig() srv.Config { return srv.Config{} }
`)
		if err != nil {
			t.Fatalf("server.Config alias rejected: %v", err)
		}
		if got := phase2ModuleFuncKind(t, mod, "SetGoxConfig"); got != FuncConfig {
			t.Fatalf("aliased server.Config classified as %q, want %q", got, FuncConfig)
		}
	})
}

func TestPhase2RecoveryPhase14AdvancedForContracts(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	t.Run("RangeExpressionEvaluatedOnceWithoutLen", func(t *testing.T) {
		tpl, err := ParseTemplate(`<:for index user range={{ getUsers() }}>
	<:empty>No users</:empty>
	<:each>{{ user }}</:each>
</:for>`)
		if err != nil {
			t.Fatal(err)
		}
		stmts, err := GenerateTemplateStmts(tpl, &TemplateGenContext{TypeEnv: NewTemplateTypeEnv()})
		if err != nil {
			t.Fatal(err)
		}
		root := &ast.BlockStmt{List: stmts}
		if got := phase2CountCallsByName(root, "getUsers"); got != 1 {
			t.Fatalf("advanced <:for> evaluates range expression %d times, want exactly 1", got)
		}
		if phase2ContainsCallByName(root, "len") {
			t.Fatal("advanced <:for> must not add len(rangeExpr); it must preserve general Go range semantics")
		}
	})

	t.Run("IntegerRangeRemainsValid", func(t *testing.T) {
		phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return <:for i range={{ 3 }}>
		<:empty>Empty</:empty>
		<:each><span>{{ i }}</span></:each>
	</:for>
}
`,
		})
	})
}

func TestPhase2RecoveryPhase15SwitchAttributeContracts(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	tests := []struct {
		name string
		src  string
	}{
		{
			name: "SwitchRejectsUnknownAttribute",
			src: `<:switch value={{ status }} extra>
	<:case is={{ 1 }}></:case>
</:switch>`,
		},
		{
			name: "CaseRejectsUnknownAttribute",
			src: `<:switch value={{ status }}>
	<:case is={{ 1 }} extra></:case>
</:switch>`,
		},
		{
			name: "DefaultRejectsAnyAttribute",
			src: `<:switch value={{ status }}>
	<:default extra></:default>
</:switch>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tpl, err := ParseTemplate(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = GenerateTemplateStmts(tpl, &TemplateGenContext{TypeEnv: NewTemplateTypeEnv()})
			phase2RequireError(t, err)
		})
	}
}

func TestPhase2RecoveryPhase16ClientTransitionContract(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	_, err := ParseModule("pages/index.gox", `module index
import "github.com/gox-lang/gox"
client Menu react { src: "menu/menu.jsx" }
func GET(ctx *gox.Context) gox.Page {
	return <@Menu transition />
}
`)
	phase2RequireError(t, err)
}

func TestPhase2RecoveryPhase17BundlerIsolationContracts(t *testing.T) {
	phase2RequireRecoveryContracts(t)

	t.Run("GoImportAliasesRemainModuleLocal", func(t *testing.T) {
		phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import A "./a.gox"
import B "./b.gox"
import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return A.Message() + B.Message()
}
`,
			files: map[string]string{
				"pages/a.gox": `module a
import x "fmt"
func Message() string { return x.Sprint("a") }
`,
				"pages/b.gox": `module b
import x "strings"
func Message() string { return x.ToUpper("b") }
`,
			},
		})
	})

	t.Run("InternalModuleIdentityDoesNotCollideOnDeclaredName", func(t *testing.T) {
		phase1CompileAndGoTest(t, phase1GeneratedGoFixture{
			entryRel: "pages/index.gox",
			source: `module index

import A "./a.gox"
import B "./b.gox"
import "github.com/gox-lang/gox"

func GET(ctx *gox.Context) gox.Page {
	return A.Message() + B.Message()
}
`,
			files: map[string]string{
				"pages/a.gox": `module shared
func Message() string { return "a" }
`,
				"pages/b.gox": `module shared
func Message() string { return "b" }
`,
			},
		})
	})
}
