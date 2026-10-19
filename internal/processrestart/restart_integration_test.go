//go:build !windows

package processrestart

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const processRestartTestStage = "GOX_PROCESS_RESTART_TEST_STAGE"

func TestCurrentReplacesProcessAndPreservesStandardIO(t *testing.T) {
	switch os.Getenv(processRestartTestStage) {
	case "before":
		fmt.Printf("before:%d\n", os.Getpid())
		fmt.Fprintln(os.Stderr, "stderr-before")
		if err := os.Setenv(processRestartTestStage, "after"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := Current(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(3)
	case "after":
		input, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Printf("after:%d:%s", os.Getpid(), input)
		fmt.Fprintln(os.Stderr, "stderr-after")
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestCurrentReplacesProcessAndPreservesStandardIO$")
	command.Env = append(os.Environ(), processRestartTestStage+"=before")
	command.Stdin = strings.NewReader("stdin-preserved\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("replacement subprocess failed: %v\n%s", err, output)
	}

	text := string(output)
	beforePID := restartTestPID(t, text, "before:")
	afterPID := restartTestPID(t, text, "after:")
	if beforePID != afterPID {
		t.Fatalf("PID changed across exec: before=%d after=%d\n%s", beforePID, afterPID, text)
	}
	for _, expected := range []string{
		"after:" + strconv.Itoa(afterPID) + ":stdin-preserved",
		"stderr-before",
		"stderr-after",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("replacement output does not contain %q:\n%s", expected, text)
		}
	}
}

func restartTestPID(t *testing.T, output, prefix string) int {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimPrefix(line, prefix)
		if separator := strings.IndexByte(value, ':'); separator >= 0 {
			value = value[:separator]
		}
		pid, err := strconv.Atoi(value)
		if err != nil {
			t.Fatalf("invalid PID in %q: %v", line, err)
		}
		return pid
	}
	t.Fatalf("output does not contain %q:\n%s", prefix, output)
	return 0
}
