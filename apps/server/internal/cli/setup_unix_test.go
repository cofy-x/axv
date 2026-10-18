//go:build darwin || linux

package cli

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

type setupPromptOutput struct {
	bytes.Buffer
	prompted chan struct{}
	once     sync.Once
}

func (o *setupPromptOutput) Write(p []byte) (int, error) {
	n, err := o.Buffer.Write(p)
	o.once.Do(func() { close(o.prompted) })
	return n, err
}

func TestSetupTerminalApprovalAndCancellationPreserveCallerInput(t *testing.T) {
	master, input, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer input.Close()
	output := setupPromptOutput{prompted: make(chan struct{})}
	confirm := setupConfirmation(input, &output)
	if confirm == nil {
		t.Fatal("supplied terminal was not recognized")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- confirm(ctx) }()
	select {
	case <-output.prompted:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal confirmation did not display its prompt")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled approval = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal confirmation ignored cancellation")
	}
	if _, err := input.Stat(); err != nil {
		t.Fatalf("confirmation closed the caller's terminal: %v", err)
	}
	if _, err := master.WriteString("yes\n"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := confirm(ctx); err != nil {
		t.Fatalf("cancelled reader consumed later terminal input: %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte("Type yes:")) {
		t.Fatal("confirmation prompt did not use the supplied output")
	}
}
