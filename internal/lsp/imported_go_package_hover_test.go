package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoPackageHoverUsesRealPackageNameExportsAndMembers(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "library", "api.go"), `package api
type User struct { Name string; age int }
type Reader interface { Read() string; privateRead() string }
type UserAlias = User
const Limit = 10
var DefaultName string
func SplitName(fullname string) (string, string) { return fullname, fullname }
func privateFunction() {}
`)
	pagePath := filepath.Join(root, "page.gox")
	pageSource := `module page
import "example/library"
func Build() {
	var _ api.User
	_, _ = api.SplitName("George")
}
`
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}

	aliasHover := hoverAtOffset(t, server, document, strings.Index(pageSource, "api.User")+1)
	if aliasHover == nil {
		t.Fatal("Go package alias hover is nil")
	}
	for _, want := range []string{
		"package api {",
		"struct User",
		"interface Reader",
		"const Limit",
		"var DefaultName string",
		"func SplitName(fullname string) (string, string)",
		"inferred alias `api`",
		"`example/library`",
	} {
		if !strings.Contains(aliasHover.Contents.Value, want) {
			t.Errorf("package hover missing %q: %s", want, aliasHover.Contents.Value)
		}
	}
	if strings.Contains(aliasHover.Contents.Value, "privateFunction") {
		t.Fatalf("package hover exposed private declaration: %s", aliasHover.Contents.Value)
	}

	memberOffset := strings.Index(pageSource, "api.SplitName") + len("api.") + 1
	memberHover := hoverAtOffset(t, server, document, memberOffset)
	if memberHover == nil || !strings.Contains(memberHover.Contents.Value, "func SplitName(fullname string) (string, string)") {
		t.Fatalf("Go package member hover = %#v", memberHover)
	}
	userOffset := strings.Index(pageSource, "api.User") + len("api.") + 1
	userHover := hoverAtOffset(t, server, document, userOffset)
	if userHover == nil || !strings.Contains(userHover.Contents.Value, "type User struct {\n\tName string\n\t// contains filtered or unexported fields\n}") || strings.Contains(userHover.Contents.Value, "age int") {
		t.Fatalf("Go struct hover = %#v", userHover)
	}
}

func TestGoPackageHoverRespectsExplicitAlias(t *testing.T) {
	root := t.TempDir()
	writeGoPackageHoverFile(t, filepath.Join(root, "go.mod"), "module example\n\ngo 1.24\n")
	writeGoPackageHoverFile(t, filepath.Join(root, "library", "api.go"), "package api\nconst Limit = 10\n")
	pagePath := filepath.Join(root, "page.gox")
	pageSource := "module page\nimport library \"example/library\"\nfunc Build() { _ = library.Limit }\n"
	writeWorkspaceGOX(t, pagePath, pageSource)
	server := NewServer()
	server.indexWorkspaceRoots([]string{root})
	document, ok := server.symbols.Document(FileURI(pagePath))
	if !ok {
		t.Fatal("page document was not indexed")
	}
	hover := hoverAtOffset(t, server, document, strings.Index(pageSource, "library.Limit")+1)
	if hover == nil || !strings.Contains(hover.Contents.Value, "package api {") || !strings.Contains(hover.Contents.Value, "explicit alias `library`") {
		t.Fatalf("explicit Go package alias hover = %#v", hover)
	}
}

func writeGoPackageHoverFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
