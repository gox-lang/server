package update

import (
	"github.com/gox-lang/server/internal/cli"
	"testing"
)

func TestRejectsUnexpectedArguments(t *testing.T) {
	if err := New().Run(cli.Context{}, []string{"unexpected"}); err == nil {
		t.Fatal("accepted arguments")
	}
}
