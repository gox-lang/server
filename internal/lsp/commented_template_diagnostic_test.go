package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCommentedTemplateReturnsDoNotProduceTemplateDiagnostics(t *testing.T) {
	for _, body := range []string{
		"// return<>\n// <p>hola mundo</p>\n// </>",
		"// return <p>hola mundo</p>",
		"/* return <>\n<p>hola mundo</p>\n</> */",
	} {
		path := filepath.Join(t.TempDir(), "index.gox")
		document := Document{URI: FileURI(path), Path: path, Version: 1, Text: "module index\nimport gox \"github.com/gox-lang/gox\"\nexported func GET(ctx *gox.Context) gox.Page {\n" + body + "\n}\n"}
		for _, diagnostic := range NewServer().diagnostics(document) {
			if strings.Contains(diagnostic.Message, "empty template") || strings.Contains(diagnostic.Message, "expected ';'") || diagnostic.Range.Start.Line == 0 {
				t.Fatalf("comment produced spurious diagnostic: %#v", diagnostic)
			}
		}
	}
}
