package gox

import (
	"go/ast"
	"strings"
	"testing"
)

func TestActionFileReturnUsesDedicatedActionWriter(t *testing.T) {
	out := compileActionContractSource(t, `
	file, err := gox.NewFile(gox.FileConfig{Name: "report.txt", Content: strings.NewReader("report")})
	if err != nil { return err }
	return file`)

	if !strings.Contains(out, `gox.WriteActionFile(ctx, file)`) {
		t.Fatalf("File return does not use dedicated Action writer:\n%s", out)
	}
	if !strings.Contains(out, `if goxActionFileErr := gox.WriteActionFile(ctx, file); goxActionFileErr != nil`) {
		t.Fatalf("File writer error is not handled:\n%s", out)
	}
	if !strings.Contains(out, `ctx.Response.Headers.Set("Action-Response", "error")`) {
		t.Fatalf("File writer error does not produce an Action error response:\n%s", out)
	}
	if !strings.Contains(out, `ctx.Writer.WriteError(goxActionFileErr)`) {
		t.Fatalf("File writer error is not written through ctx.Writer:\n%s", out)
	}
	if strings.Contains(out, `ctx.Writer.WriteRawAny(file)`) || strings.Contains(out, `ctx.Writer.WriteStream(file)`) {
		t.Fatalf("File return was routed through a generic writer:\n%s", out)
	}
}

func TestActionFilesReturnUsesDedicatedMultipartWriter(t *testing.T) {
	out := compileActionContractSource(t, `
	first, err := gox.NewFile(gox.FileConfig{Name: "first.txt", Content: strings.NewReader("first")})
	if err != nil { return err }
	second, err := gox.NewFile(gox.FileConfig{Name: "second.txt", Content: strings.NewReader("second")})
	if err != nil { return err }
	files := []gox.File{first, second}
	return files`)

	if !strings.Contains(out, `gox.WriteActionFiles(ctx, files)`) {
		t.Fatalf("[]File return does not use dedicated multipart Action writer:\n%s", out)
	}
	if !strings.Contains(out, `if goxActionFilesErr := gox.WriteActionFiles(ctx, files); goxActionFilesErr != nil`) {
		t.Fatalf("[]File writer error is not handled:\n%s", out)
	}
	if !strings.Contains(out, `ctx.Writer.WriteError(goxActionFilesErr)`) {
		t.Fatalf("[]File writer error is not written through ctx.Writer:\n%s", out)
	}
	if strings.Contains(out, `ctx.Writer.WriteRawAny(files)`) || strings.Contains(out, `ctx.Writer.WriteTextAny(files)`) {
		t.Fatalf("[]File return was routed through a generic writer:\n%s", out)
	}
}

func TestActionFileTypesAreNotClassifiedAsJSON(t *testing.T) {
	if got := actionTypeForType("gox.File"); got == "json" {
		t.Fatalf("gox.File must not be classified as JSON")
	}
	if got := actionTypeForType("[]gox.File"); got == "json" {
		t.Fatalf("[]gox.File must not be classified as JSON")
	}
}

func TestActionFileTypeRecognitionIsExact(t *testing.T) {
	for _, typeName := range []string{"File", "gox.File", "github.com/gox-lang/gox.File"} {
		if !isTemplateFileType(typeName) {
			t.Fatalf("%q must be recognized as gox.File", typeName)
		}
	}
	for _, typeName := range []string{"other.File", "example.com/other.File", "[]gox.File"} {
		if isTemplateFileType(typeName) {
			t.Fatalf("%q must not be recognized as a single gox.File", typeName)
		}
	}
	for _, typeName := range []string{"[]File", "[]gox.File", "[]github.com/gox-lang/gox.File"} {
		if !isTemplateFilesType(typeName) {
			t.Fatalf("%q must be recognized as []gox.File", typeName)
		}
	}
	if isTemplateFilesType("[]other.File") {
		t.Fatal("[]other.File must not be recognized as []gox.File")
	}
}

func TestActionFileWriterErrorBuilderDoesNotDependOnStatementSlicing(t *testing.T) {
	writeOnly := buildActionErrorWriteStmts(nil)
	full := buildActionErrorResponseStmts(nil)
	if len(writeOnly) != 4 {
		t.Fatalf("write-only statements = %d, want 4", len(writeOnly))
	}
	if len(full) != 5 {
		t.Fatalf("full statements = %d, want 5", len(full))
	}
	if _, ok := full[len(full)-1].(*ast.ReturnStmt); !ok {
		t.Fatalf("full error response must end in return, got %T", full[len(full)-1])
	}
	if _, ok := writeOnly[len(writeOnly)-1].(*ast.ReturnStmt); ok {
		t.Fatal("write-only error statements must not include return")
	}
}
