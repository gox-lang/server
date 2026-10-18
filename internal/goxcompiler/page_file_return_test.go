package gox

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"strings"
	"testing"
)

func compilePageFileSource(t *testing.T, body string) string {
	t.Helper()
	src := `module main

import (
	"io"
	"strings"
	"github.com/gox-lang/gox"
)

func GET(ctx *gox.Context) gox.Page {
` + body + `
}
`
	mod, err := ParseModule("pages/index.gox", src)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	if err != nil {
		t.Fatal(err)
	}
	file := &ast.File{Name: ast.NewIdent("main"), Decls: bundle.Decls}
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), file); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestPageFileReturnUsesDedicatedFileWriter(t *testing.T) {
	out := compilePageFileSource(t, `
	file, err := gox.NewFile(gox.FileConfig{Name: "report.txt", Content: strings.NewReader("report")})
	if err != nil { return err }
	return file`)

	if !strings.Contains(out, `gox.WritePageFile(ctx, file)`) {
		t.Fatalf("File return does not use dedicated page writer:\n%s", out)
	}
	if strings.Contains(out, `ctx.Writer.WriteStream(file)`) {
		t.Fatalf("File return was classified as a generic stream:\n%s", out)
	}
	if strings.Contains(out, `ctx.Writer.WriteRawAny(file)`) {
		t.Fatalf("File return was routed through WriteRawAny:\n%s", out)
	}
}

func TestPageIOReaderStillUsesGenericStreamWriter(t *testing.T) {
	out := compilePageFileSource(t, `
	var reader io.Reader = strings.NewReader("stream")
	return reader`)
	if !strings.Contains(out, `ctx.Writer.WriteStream(reader)`) {
		t.Fatalf("io.Reader no longer uses generic stream writer:\n%s", out)
	}
	if strings.Contains(out, `gox.WritePageFile(ctx, reader)`) {
		t.Fatalf("io.Reader was incorrectly classified as File:\n%s", out)
	}
}

func TestPageFilesReturnIsRejected(t *testing.T) {
	src := `module main

import (
	"strings"
	"github.com/gox-lang/gox"
)

func GET(ctx *gox.Context) gox.Page {
	first, err := gox.NewFile(gox.FileConfig{Name: "first.txt", Content: strings.NewReader("first")})
	if err != nil { return err }
	second, err := gox.NewFile(gox.FileConfig{Name: "second.txt", Content: strings.NewReader("second")})
	if err != nil { return err }
	files := []gox.File{first, second}
	return files
}
`
	mod, err := ParseModule("pages/index.gox", src)
	if err == nil {
		_, err = BundleModules(&ModuleGraph{Entry: mod, Modules: []*Module{mod}, ByPath: map[string]*Module{mod.Path: mod}})
	}
	if err == nil {
		t.Fatal("expected []gox.File page return to be rejected")
	}
	if !strings.Contains(err.Error(), "pages cannot return []gox.File") {
		t.Fatalf("unexpected error: %v", err)
	}
}
