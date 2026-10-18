package providerutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCommandPreservesDiagnosticsExitStatusAndOutputLimit(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AXV_PROVIDER_COMMAND_TEST", "1")
	output, err := Command(context.Background(), executable, "-test.run=^TestCommandProcess$", "--", "failure")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || !strings.Contains(string(output), "provider stdout") || !strings.Contains(err.Error(), "provider stderr") {
		t.Fatalf("provider diagnostic or exit status was lost: %q, %v", output, err)
	}
	output, err = Command(context.Background(), executable, "-test.run=^TestCommandProcess$", "--", "large")
	if err != nil || string(output) != strings.Repeat("x", 64<<10) {
		t.Fatalf("bounded successful output = %d bytes, %v", len(output), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Command(ctx, executable, "-test.run=^TestCommandProcess$", "--", "large"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled command = %v", err)
	}
}

func TestCommandProcess(t *testing.T) {
	if os.Getenv("AXV_PROVIDER_COMMAND_TEST") != "1" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "failure":
		fmt.Fprintln(os.Stdout, "provider stdout")
		fmt.Fprintln(os.Stderr, "provider stderr")
		os.Exit(7)
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 128<<10))
		os.Exit(0)
	default:
		t.Fatal("unknown provider fixture command")
	}
}
