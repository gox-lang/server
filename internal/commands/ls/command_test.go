package ls

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/cli"
)

func TestCommandRunsLanguageServerOnConfiguredStreams(t *testing.T) {
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	shutdown := `{"jsonrpc":"2.0","id":2,"method":"shutdown"}`
	exit := `{"jsonrpc":"2.0","method":"exit"}`
	input := frame(initialize) + frame(shutdown) + frame(exit)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := New().Run(cli.Context{Stdin: strings.NewReader(input), Stdout: &stdout, Stderr: &stderr}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout.String(), "Content-Length:") ||
		!strings.Contains(stdout.String(), `"positionEncoding":"utf-16"`) ||
		!strings.Contains(stdout.String(), `"textDocumentSync":{"change":1,"openClose":true}`) {
		t.Fatalf("stdout is not an LSP stream: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestCommandRejectsArguments(t *testing.T) {
	err := New().Run(cli.Context{Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}}, []string{"unexpected"})
	if err == nil || err.Error() != "usage: gox ls" {
		t.Fatalf("error = %v", err)
	}
}

func frame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}
