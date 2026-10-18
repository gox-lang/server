package goxerrors

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gox-lang/gox"
)

func TestGoxErrorSnapshotCompileGoMessageIsPreserved(t *testing.T) {
	cause := errors.New("pages/index.gox:9: undefined: fmt")
	err := New(TypeCompiler, gox.GoxErrorCompile, "undefined: fmt", cause)
	err.Kind = "undefined-identifier"
	err.File = "pages/index.gox"
	err.Line = 9
	err.Hint = `Import package "fmt".`

	got := snapshot(err)
	want := "type=compiler\nstage=compile\nkind=undefined-identifier\nfile=pages/index.gox\nline=9\ncolumn=0\nmessage=undefined: fmt\nhint=Import package \"fmt\".\nstatus=500\nstatusMessage=Internal Server Error\ncause=pages/index.gox:9: undefined: fmt"
	if got != want {
		t.Fatalf("snapshot mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestGoxErrorSnapshotTemplateMessage(t *testing.T) {
	err := New(TypeTemplate, gox.GoxErrorCompile, "expected closing tag </div>, got </span>", nil)
	err.Kind = "tag-mismatch"
	err.File = "pages/index.gox"
	err.Line = 12
	err.Column = 4
	err.Expected = "</div>"
	err.Found = "</span>"
	err.Hint = "Close </div> before </span> or change the closing tag."

	got := snapshot(err)
	want := "type=template\nstage=compile\nkind=tag-mismatch\nfile=pages/index.gox\nline=12\ncolumn=4\nmessage=expected closing tag </div>, got </span>\nhint=Close </div> before </span> or change the closing tag.\nexpected=</div>\nfound=</span>\nstatus=500\nstatusMessage=Internal Server Error"
	if got != want {
		t.Fatalf("snapshot mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestGoxErrorSnapshotResponseStatus(t *testing.T) {
	err := New(TypeResponse, gox.GoxErrorWriter, "invalid response mutation after write", nil)
	err.Kind = "invalid-response-mutation-after-write"
	err.StatusCode = http.StatusInternalServerError
	err.EnsureStatus()

	got := snapshot(err)
	want := "type=response\nstage=writer\nkind=invalid-response-mutation-after-write\nfile=\nline=0\ncolumn=0\nmessage=invalid response mutation after write\nhint=\nstatus=500\nstatusMessage=Internal Server Error"
	if got != want {
		t.Fatalf("snapshot mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func snapshot(err *GoxError) string {
	out := "type=" + string(err.Type) +
		"\nstage=" + string(err.Stage) +
		"\nkind=" + err.Kind +
		"\nfile=" + err.File +
		"\nline=" + itoa(err.Line) +
		"\ncolumn=" + itoa(err.Column) +
		"\nmessage=" + err.Message +
		"\nhint=" + err.Hint
	if err.Expected != "" {
		out += "\nexpected=" + err.Expected
	}
	if err.Found != "" {
		out += "\nfound=" + err.Found
	}
	out += "\nstatus=" + itoa(err.StatusCode) +
		"\nstatusMessage=" + err.StatusMessage
	if err.Cause != nil {
		out += "\ncause=" + err.Cause.Error()
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
