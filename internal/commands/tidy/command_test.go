package tidy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/cli"
)

func TestCommandMetadata(t *testing.T) {
	cmd := New()
	if got := cmd.Name(); got != "tidy" {
		t.Fatalf("Name() = %q, want %q", got, "tidy")
	}
	if !strings.Contains(cmd.Help(), "gox tidy") {
		t.Fatalf("Help() does not document gox tidy:\n%s", cmd.Help())
	}
}

func TestCommandRejectsArguments(t *testing.T) {
	cmd := New()
	err := cmd.Run(cli.Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, []string{"extra"})
	if err == nil || err.Error() != "usage: gox tidy" {
		t.Fatalf("Run() error = %v, want %q", err, "usage: gox tidy")
	}
}
