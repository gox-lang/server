package gox

import (
	"go/ast"
	"go/token"
	"testing"

	"github.com/gox-lang/server/internal/goxerrors"
)

func TestRepeatWithoutBindingGeneratesRangeWithoutIndex(t *testing.T) {
	stmts := generateDirectiveTestStmts(t, `<:repeat times={{ count }}></:repeat>`)
	rangeStmt := firstRangeStmt(t, stmts)

	if rangeStmt.Key != nil || rangeStmt.Value != nil {
		t.Fatalf("repeat without binding must not declare range variables: key=%#v value=%#v", rangeStmt.Key, rangeStmt.Value)
	}
	if rangeStmt.Tok != token.ILLEGAL {
		t.Fatalf("repeat without binding token = %v, want ILLEGAL", rangeStmt.Tok)
	}
	if id, ok := rangeStmt.X.(*ast.Ident); !ok || id.Name != "count" {
		t.Fatalf("repeat range expression = %#v, want count", rangeStmt.X)
	}
}

func TestRepeatWithBindingGeneratesIntegerRangeBinding(t *testing.T) {
	stmts := generateDirectiveTestStmts(t, `<:repeat index times={{ count }}>{{ index }}</:repeat>`)
	rangeStmt := firstRangeStmt(t, stmts)

	key, ok := rangeStmt.Key.(*ast.Ident)
	if !ok || key.Name != "index" {
		t.Fatalf("repeat key = %#v, want index", rangeStmt.Key)
	}
	if rangeStmt.Value != nil {
		t.Fatalf("repeat value = %#v, want nil", rangeStmt.Value)
	}
	if rangeStmt.Tok != token.DEFINE {
		t.Fatalf("repeat token = %v, want :=", rangeStmt.Tok)
	}
}

func TestRepeatWithoutBindingDoesNotPublishImplicitIndexType(t *testing.T) {
	tpl, err := ParseTemplate(`<:repeat times={{ count }}></:repeat>`)
	if err != nil {
		t.Fatal(err)
	}
	directive := tpl.Root.(*DirectiveNode)
	binding, err := parseRepeatIndexBinding(directive)
	if err != nil {
		t.Fatal(err)
	}
	if binding != "" {
		t.Fatalf("repeat without binding = %q, want empty", binding)
	}

	env := NewTemplateTypeEnv()
	repeatEnv := templateTypeEnvWithRepeatBinding(env, binding)
	if _, ok := repeatEnv.Vars["i"]; ok {
		t.Fatal("repeat without binding must not expose an implicit i variable")
	}
}

func TestSwitchCaseSupportsExpressionListAndFallthrough(t *testing.T) {
	stmts := generateDirectiveTestStmts(t, `<:switch value={{ status }}>
		<:case is={{ StatusPending, StatusQueued, StatusRetrying }} fallthrough></:case>
		<:case is={{ StatusReady }}></:case>
	</:switch>`)

	switchStmt := firstSwitchStmt(t, stmts)
	if len(switchStmt.Body.List) != 2 {
		t.Fatalf("switch clauses = %d, want 2", len(switchStmt.Body.List))
	}

	first, ok := switchStmt.Body.List[0].(*ast.CaseClause)
	if !ok {
		t.Fatalf("first switch clause = %T", switchStmt.Body.List[0])
	}
	if len(first.List) != 3 {
		t.Fatalf("first case expressions = %d, want 3", len(first.List))
	}
	for i, want := range []string{"StatusPending", "StatusQueued", "StatusRetrying"} {
		id, ok := first.List[i].(*ast.Ident)
		if !ok || id.Name != want {
			t.Fatalf("case expression %d = %#v, want %s", i, first.List[i], want)
		}
	}
	if len(first.Body) == 0 {
		t.Fatal("fallthrough case has empty body")
	}
	branch, ok := first.Body[len(first.Body)-1].(*ast.BranchStmt)
	if !ok || branch.Tok != token.FALLTHROUGH {
		t.Fatalf("last case statement = %#v, want fallthrough", first.Body[len(first.Body)-1])
	}
}

func TestSwitchCaseCanFallthroughIntoDefault(t *testing.T) {
	stmts := generateDirectiveTestStmts(t, `<:switch value={{ status }}>
		<:case is={{ StatusUnknown }} fallthrough></:case>
		<:default></:default>
	</:switch>`)

	switchStmt := firstSwitchStmt(t, stmts)
	first := switchStmt.Body.List[0].(*ast.CaseClause)
	branch, ok := first.Body[len(first.Body)-1].(*ast.BranchStmt)
	if !ok || branch.Tok != token.FALLTHROUGH {
		t.Fatalf("last case statement = %#v, want fallthrough", first.Body[len(first.Body)-1])
	}
	last := switchStmt.Body.List[1].(*ast.CaseClause)
	if last.List != nil {
		t.Fatalf("default list = %#v, want nil", last.List)
	}
}

func TestSwitchDirectiveStructuralValidation(t *testing.T) {
	tests := []struct {
		name string
		src  string
		kind string
	}{
		{
			name: "empty switch",
			src:  `<:switch value={{ status }}></:switch>`,
			kind: "empty-switch",
		},
		{
			name: "default must be last",
			src: `<:switch value={{ status }}>
				<:default></:default>
				<:case is={{ StatusReady }}></:case>
			</:switch>`,
			kind: "default-not-last",
		},
		{
			name: "duplicate default",
			src: `<:switch value={{ status }}>
				<:default></:default>
				<:default></:default>
			</:switch>`,
			kind: "duplicate-default-block",
		},
		{
			name: "fallthrough on final case",
			src: `<:switch value={{ status }}>
				<:case is={{ StatusReady }} fallthrough></:case>
			</:switch>`,
			kind: "fallthrough-last-case",
		},
		{
			name: "fallthrough on default",
			src: `<:switch value={{ status }}>
				<:default fallthrough></:default>
			</:switch>`,
			kind: "fallthrough-on-default",
		},
		{
			name: "fallthrough must be bare",
			src: `<:switch value={{ status }}>
				<:case is={{ StatusPending }} fallthrough={{ true }}></:case>
				<:case is={{ StatusReady }}></:case>
			</:switch>`,
			kind: "invalid-fallthrough-attribute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tpl, err := ParseTemplate(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = GenerateTemplateStmts(tpl, &TemplateGenContext{TypeEnv: NewTemplateTypeEnv()})
			requireDirectiveTemplateErrorKind(t, err, tt.kind)
		})
	}
}

func generateDirectiveTestStmts(t *testing.T, src string) []ast.Stmt {
	t.Helper()
	tpl, err := ParseTemplate(src)
	if err != nil {
		t.Fatal(err)
	}
	stmts, err := GenerateTemplateStmts(tpl, &TemplateGenContext{TypeEnv: NewTemplateTypeEnv()})
	if err != nil {
		t.Fatal(err)
	}
	return stmts
}

func firstRangeStmt(t *testing.T, stmts []ast.Stmt) *ast.RangeStmt {
	t.Helper()
	for _, stmt := range stmts {
		if value, ok := stmt.(*ast.RangeStmt); ok {
			return value
		}
	}
	t.Fatalf("range statement missing from %#v", stmts)
	return nil
}

func firstSwitchStmt(t *testing.T, stmts []ast.Stmt) *ast.SwitchStmt {
	t.Helper()
	for _, stmt := range stmts {
		if value, ok := stmt.(*ast.SwitchStmt); ok {
			return value
		}
	}
	t.Fatalf("switch statement missing from %#v", stmts)
	return nil
}

func requireDirectiveTemplateErrorKind(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected template error %q", want)
	}
	goxErr, ok := err.(*goxerrors.GoxError)
	if !ok {
		t.Fatalf("error = %T %v, want *goxerrors.GoxError", err, err)
	}
	if goxErr.Kind != want {
		t.Fatalf("error kind = %q, want %q: %v", goxErr.Kind, want, err)
	}
}
