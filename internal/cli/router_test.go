package cli

import (
	"bytes"
	"fmt"
	"testing"
)

type testCommand struct{}

func (testCommand) Name() string                { return "test" }
func (testCommand) Description() string         { return "Test command" }
func (testCommand) Help() string                { return "test help" }
func (testCommand) Run(Context, []string) error { return nil }

type nestedTestCommand struct{ testCommand }

func (nestedTestCommand) Name() string { return "client" }
func (nestedTestCommand) Help() string { return "client help" }
func (nestedTestCommand) HelpFor(args []string) (string, error) {
	if len(args) == 0 {
		return "client help", nil
	}
	if len(args) == 1 && args[0] == "add" {
		return "client add help", nil
	}
	return "", fmt.Errorf("unknown client command")
}

func runRouter(t *testing.T, r *Router, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := r.Run(Context{Stdout: &out, Stderr: &out}, args); err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}
	return out.String()
}

func TestRootHelpFormsMatch(t *testing.T) {
	r := NewRouter(testCommand{})
	expected := runRouter(t, r, "help")
	for _, args := range [][]string{nil, {"-h"}, {"--help"}, {"help", "-h"}, {"help", "--help"}} {
		if got := runRouter(t, r, args...); got != expected {
			t.Fatalf("help mismatch for %v", args)
		}
	}
}

func TestCommandHelpFormsMatch(t *testing.T) {
	r := NewRouter(testCommand{})
	expected := runRouter(t, r, "help", "test")
	for _, args := range [][]string{{"test", "-h"}, {"test", "--help"}, {"test", "--run", "Index", "--help"}, {"help", "test", "--run", "Index"}} {
		if got := runRouter(t, r, args...); got != expected {
			t.Fatalf("help mismatch for %v: %q != %q", args, got, expected)
		}
	}
}

func TestContextualHelpFormsMatch(t *testing.T) {
	r := NewRouter(nestedTestCommand{})
	expected := runRouter(t, r, "help", "client", "add")
	for _, args := range [][]string{{"client", "add", "-h"}, {"client", "add", "--help"}, {"client", "add", "react", "--help"}, {"help", "client", "add", "--framework", "react"}} {
		if got := runRouter(t, r, args...); got != expected {
			t.Fatalf("help mismatch for %v: %q != %q", args, got, expected)
		}
	}
}
