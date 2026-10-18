package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/gox-lang/server/internal/logs"
)

func TestWriteCLIErrorUsesPlainText(t *testing.T) {
	var output bytes.Buffer

	writeCLIError(&output, errors.New("route not found: /asd"))

	const want = "route not found: /asd\n"
	if output.String() != want {
		t.Fatalf("writeCLIError() = %q, want %q", output.String(), want)
	}
}

func TestConfigureCLILoggingDisablesOperationalLogs(t *testing.T) {
	previous := logs.Enabled()
	t.Cleanup(func() { logs.SetEnabled(previous) })

	logs.SetEnabled(true)
	configureCLILogging()

	if logs.Enabled() {
		t.Fatal("CLI operational logs remain enabled")
	}
}
