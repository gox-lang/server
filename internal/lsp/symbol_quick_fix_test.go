package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSymbolQuickFixQualifiesAndImportsGoxSymbol(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\nfunc NewUser(name string) string { return name }\n")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, pagePath, "module page\nfunc Build() string { return NewUser(\"George\") }\n")
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, _ := server.symbols.Document(FileURI(pagePath))
	diagnostic := requireDiagnosticCode(t, server.diagnostics(document), "undefined-identifier", "undefined: NewUser")
	actions := server.codeActions(document, CodeActionParams{Context: CodeActionContext{Diagnostics: []Diagnostic{diagnostic}}})
	action := requireCodeAction(t, actions, "Qualify NewUser as users.NewUser and import users.gox")
	assertActionContainsEdits(t, action, "users.NewUser", `users "users.gox"`)
	if !action.IsPreferred {
		t.Fatal("unique symbol quick fix should be preferred")
	}
}

func TestSymbolQuickFixUsesCollisionFreeAlias(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\ntype User struct { Name string }\n")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, pagePath, "module page\nfunc Build(users string) { _ = User{Name: users} }\n")
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, _ := server.symbols.Document(FileURI(pagePath))
	diagnostic := requireDiagnosticCode(t, server.diagnostics(document), "undefined-identifier", "undefined: User")
	actions := server.codeActions(document, CodeActionParams{Context: CodeActionContext{Diagnostics: []Diagnostic{diagnostic}}})
	action := requireCodeAction(t, actions, "Qualify User as users2.User and import users.gox")
	assertActionContainsEdits(t, action, "users2.User", `users2 "users.gox"`)
}

func TestSymbolQuickFixRepairsWrongGoxQualifier(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\nfunc NewUser(name string) string { return name }\n")
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nimport users \"users.gox\"\nfunc Build() string { return user.NewUser(\"George\") }\n"
	writeWorkspaceGOX(t, pagePath, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, _ := server.symbols.Document(FileURI(pagePath))
	diagnostic := requireDiagnosticCode(t, server.diagnostics(document), "undefined-identifier", "undefined: user")
	actions := server.codeActions(document, CodeActionParams{Context: CodeActionContext{Diagnostics: []Diagnostic{diagnostic}}})
	action := requireCodeAction(t, actions, "Change user to users")
	assertActionContainsEdits(t, action, "users")
	if !action.IsPreferred {
		t.Fatal("unique alias correction should be preferred")
	}
}

func TestSymbolQuickFixChangesModuleWhenMemberBelongsToAnotherImport(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\ntype User struct { Name string }\n")
	writeWorkspaceGOX(t, filepath.Join(root, "profiles.gox"), "module profiles\ntype Profile struct { Name string }\n")
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nimport (\n\tusers \"users.gox\"\n\tprofiles \"profiles.gox\"\n)\nfunc Build() users.User { return profiles.User{Name: \"George\"} }\n"
	writeWorkspaceGOX(t, pagePath, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, _ := server.symbols.Document(FileURI(pagePath))
	diagnostic := requireDiagnosticCode(t, server.diagnostics(document), "undefined-selector", "")
	actions := server.codeActions(document, CodeActionParams{Context: CodeActionContext{Diagnostics: []Diagnostic{diagnostic}}})
	action := requireCodeAction(t, actions, "Change profiles to users")
	assertActionContainsEdits(t, action, "users")
}

func TestSymbolQuickFixRepairsWrongGoPackageQualifier(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	pagePath := filepath.Join(root, "page.gox")
	source := "module page\nimport strings \"strings\"\nfunc Build() string { return str.TrimSpace(\" George \") }\n"
	writeWorkspaceGOX(t, pagePath, source)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, _ := server.symbols.Document(FileURI(pagePath))
	diagnostic := requireDiagnosticCode(t, server.diagnostics(document), "undefined-identifier", "undefined: str")
	actions := server.codeActions(document, CodeActionParams{Context: CodeActionContext{Diagnostics: []Diagnostic{diagnostic}}})
	action := requireCodeAction(t, actions, "Change str to strings")
	assertActionContainsEdits(t, action, "strings")
}

func TestSymbolQuickFixLeavesAmbiguousSymbolsUnpreferred(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "users.gox"), "module users\nconst DefaultName = \"George\"\n")
	writeWorkspaceGOX(t, filepath.Join(root, "profiles.gox"), "module profiles\nconst DefaultName = \"Anonymous\"\n")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, pagePath, "module page\nfunc Build() string { return DefaultName }\n")
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, _ := server.symbols.Document(FileURI(pagePath))
	diagnostic := requireDiagnosticCode(t, server.diagnostics(document), "undefined-identifier", "undefined: DefaultName")
	actions := server.codeActions(document, CodeActionParams{Context: CodeActionContext{Diagnostics: []Diagnostic{diagnostic}}})
	matched := 0
	for _, action := range actions {
		if strings.HasPrefix(action.Title, "Qualify DefaultName as ") {
			matched++
			if action.IsPreferred {
				t.Fatalf("ambiguous action must not be preferred: %#v", action)
			}
		}
	}
	if matched != 2 {
		t.Fatalf("ambiguous quick fixes = %d, want 2 in %#v", matched, actions)
	}
}

func TestSymbolQuickFixQualifiesTemplateComponent(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceGOX(t, filepath.Join(root, "card.gox"), "module card\nimport gox \"github.com/gox-lang/gox\"\nfunc Card(name string) gox.Component { return <article>{{ name }}</article> }\n")
	pagePath := filepath.Join(root, "page.gox")
	writeWorkspaceGOX(t, pagePath, "module page\nimport gox \"github.com/gox-lang/gox\"\nfunc Page() gox.Component { return <main><&Card name={{ \"George\" }} /></main> }\n")
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, _ := server.symbols.Document(FileURI(pagePath))
	diagnostic := requireDiagnosticCode(t, server.diagnostics(document), "undefined-component", "undefined component: Card")
	actions := server.codeActions(document, CodeActionParams{Context: CodeActionContext{Diagnostics: []Diagnostic{diagnostic}}})
	action := requireCodeAction(t, actions, "Qualify component Card as card.Card and import card.gox")
	assertActionContainsEdits(t, action, "card.Card", `card "card.gox"`)
}

func requireCodeAction(t *testing.T, actions []CodeAction, title string) CodeAction {
	t.Helper()
	for _, action := range actions {
		if action.Title == title {
			return action
		}
	}
	t.Fatalf("missing code action %q in %#v", title, actions)
	return CodeAction{}
}

func assertActionContainsEdits(t *testing.T, action CodeAction, values ...string) {
	t.Helper()
	if action.Edit == nil {
		t.Fatalf("code action has no edit: %#v", action)
	}
	var text string
	for _, edits := range action.Edit.Changes {
		for _, edit := range edits {
			text += edit.NewText + "\n"
		}
	}
	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Fatalf("code action edits %q do not contain %q", text, value)
		}
	}
}
