package processrestart

import (
	"errors"
	"reflect"
	"testing"
)

func TestReplacePreservesExecutableArgumentsAndEnvironment(t *testing.T) {
	wantArgs := []string{"/opt/gox/gox", "run", "--host", "127.0.0.1", "--port", "3001"}
	wantEnvironment := []string{"GOX_WORKDIR=/srv/noti-milo", "PATH=/usr/bin"}
	called := false

	err := replace(
		func() (string, error) { return "/opt/gox/gox", nil },
		func(executable string, args, environment []string) error {
			called = true
			if executable != "/opt/gox/gox" {
				t.Fatalf("executable = %q", executable)
			}
			if !reflect.DeepEqual(args, wantArgs) {
				t.Fatalf("args = %#v, want %#v", args, wantArgs)
			}
			if !reflect.DeepEqual(environment, wantEnvironment) {
				t.Fatalf("environment = %#v, want %#v", environment, wantEnvironment)
			}
			return nil
		},
		wantArgs,
		wantEnvironment,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("process replacement was not invoked")
	}
}

func TestReplaceDoesNotExecWhenExecutableCannotBeResolved(t *testing.T) {
	wantErr := errors.New("executable unavailable")
	called := false

	err := replace(
		func() (string, error) { return "", wantErr },
		func(string, []string, []string) error {
			called = true
			return nil
		},
		nil,
		nil,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if called {
		t.Fatal("process replacement ran after executable resolution failed")
	}
}

func TestReplaceReturnsExecFailure(t *testing.T) {
	wantErr := errors.New("exec failed")
	err := replace(
		func() (string, error) { return "/opt/gox/gox", nil },
		func(string, []string, []string) error { return wantErr },
		nil,
		nil,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
