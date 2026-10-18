package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gox-lang/server/internal/cli"
	"github.com/gox-lang/server/internal/logs"
)

func parseTestOptions(args ...string) (runOptions, error) {
	return parseOptions(cli.Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, args)
}

func TestParseRunHostAndPortOverrides(t *testing.T) {
	options, err := parseTestOptions("--dev", "--host", "127.0.0.1", "--port", "3001")
	if err != nil {
		t.Fatal(err)
	}
	if !options.dev || !options.hostSet || options.host != "127.0.0.1" || !options.portSet || options.port != 3001 {
		t.Fatalf("options = %#v", options)
	}
}

func TestParseRunOptionsSupportsEqualsSyntax(t *testing.T) {
	options, err := parseTestOptions("--host=localhost", "--port=8080")
	if err != nil {
		t.Fatal(err)
	}
	if options.host != "localhost" || options.port != 8080 {
		t.Fatalf("options = %#v", options)
	}
}

func TestParseRunOptionsRejectsInvalidValues(t *testing.T) {
	tests := [][]string{
		{"--host", ""},
		{"--host", "bad host"},
		{"--port", "0"},
		{"--port", "65536"},
		{"--port", "not-a-number"},
		{"unexpected"},
		{"--host", "localhost", "--host", "127.0.0.1"},
		{"--port=3000", "--port", "3001"},
		{"-dev"},
		{"-host", "localhost"},
		{"--dev=false"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if _, err := parseTestOptions(args...); err == nil {
				t.Fatalf("arguments %q were accepted", args)
			}
		})
	}
}

func TestParseRunOptionsRejectsUnknownOptions(t *testing.T) {
	for _, args := range [][]string{{"--helplplp"}, {"--unknown"}, {"-x"}} {
		if _, err := parseTestOptions(args...); err == nil {
			t.Fatalf("arguments %q were accepted", args)
		}
	}
}

func TestRunDevCreatesPersistentRuntimeOverride(t *testing.T) {
	options, err := parseTestOptions("--dev")
	if err != nil {
		t.Fatal(err)
	}
	overrides := options.runtimeOverrides()
	if !overrides.DevEnabledSet || !overrides.DevEnabled {
		t.Fatalf("overrides = %#v", overrides)
	}
}

func TestValidRunHostSupportsHostnameIPv4AndIPv6(t *testing.T) {
	for _, host := range []string{"localhost", "api.internal", "127.0.0.1", "0.0.0.0", "::1"} {
		if !validHost(host) {
			t.Fatalf("host %q was rejected", host)
		}
	}
}

func TestRunEnablesOperationalLogs(t *testing.T) {
	previous := logs.Enabled()
	t.Cleanup(func() { logs.SetEnabled(previous) })

	logs.SetEnabled(false)
	command := New()
	err := command.Run(cli.Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, []string{"unexpected"})
	if err == nil {
		t.Fatal("Run() unexpectedly accepted invalid arguments")
	}
	if !logs.Enabled() {
		t.Fatal("run command did not enable operational logs")
	}
}
