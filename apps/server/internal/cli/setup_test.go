package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/app/launch"
	"github.com/cofy-x/axv/apps/server/internal/compute/prepare"
)

func setupDesktop(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("AXV_HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/runtime" {
			_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": "setup-test"})
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	if err := os.Mkdir(filepath.Join(home, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(launch.State{URL: server.URL, InstanceID: "setup-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "run", "desktop.json"), state, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSetupCLIRequiresYesForSuppliedNonterminalInput(t *testing.T) {
	setupDesktop(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/compute/preparation/check":
			_ = json.NewEncoder(w).Encode(prepare.Check{})
		case "/api/compute/preparation/plan":
			_ = json.NewEncoder(w).Encode(prepare.Plan{ID: "reviewed", CanStart: true})
		default:
			t.Errorf("unapproved setup submitted %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	input := strings.NewReader("yes\n")
	var output, diagnostic bytes.Buffer
	err := Execute(context.Background(), []string{"setup"}, input, &output, &diagnostic, "dev")
	if err == nil || !strings.Contains(err.Error(), "--yes") || strings.Contains(output.String(), "Type yes:") {
		t.Fatalf("nonterminal approval = %v, output = %q", err, output.String())
	}
	if input.Len() != len("yes\n") {
		t.Fatal("setup consumed nonterminal input")
	}
}

func TestSetupCLIRequestsUseTheCallerCancellation(t *testing.T) {
	checking := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	setupDesktop(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/compute/preparation/check" {
			t.Errorf("unexpected setup request: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		close(checking)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Execute(ctx, []string{"setup", "--yes"}, strings.NewReader(""), io.Discard, io.Discard, "dev")
	}()
	select {
	case <-checking:
	case <-time.After(2 * time.Second):
		t.Fatal("setup did not reach the existing desktop")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || ExitCode(err) != 130 {
			t.Fatalf("cancelled setup = %v, exit = %d", err, ExitCode(err))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("setup ignored caller cancellation")
	}
}
