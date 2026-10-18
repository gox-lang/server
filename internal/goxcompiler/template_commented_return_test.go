package gox

import "testing"

func TestTemplateReturnsIgnoreCommentsAndStrings(t *testing.T) {
	for name, body := range map[string]string{
		"line fragment": "// return<>\n// <p>hola mundo</p>\n// </>",
		"line element":  "// return <p>hola mundo</p>",
		"block":         "/* return <>\n<p>hola mundo</p>\n</> */",
		"string":        `_ = "return <p>hola mundo</p>"`,
		"raw string":    "_ = `return <>\n<p>hola mundo</p>\n</>`",
	} {
		t.Run(name, func(t *testing.T) {
			source := "module index\nimport gox \"github.com/gox-lang/gox\"\nfunc GET(ctx *gox.Context) gox.Page {\n" + body + "\n}\n"
			extracted, err := ExtractTemplateReturns("index", "index.gox", source)
			if err != nil {
				t.Fatal(err)
			}
			if len(extracted.Templates) != 0 || extracted.Source != source {
				t.Fatalf("comment/literal changed: %s", extracted.Source)
			}
			if _, err := ParseModule("index.gox", source); err != nil {
				t.Fatal(err)
			}
			active := "module index\nimport gox \"github.com/gox-lang/gox\"\nfunc GET(ctx *gox.Context) gox.Page {\n" + body + "\nreturn <p>active</p>\n}\n"
			mod, err := ParseModule("index.gox", active)
			if err != nil {
				t.Fatal(err)
			}
			if len(mod.Templates) != 1 {
				t.Fatalf("active templates = %d", len(mod.Templates))
			}
		})
	}
}
