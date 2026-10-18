//go:build linux || darwin

package prepare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
)

func TestPreparationFailureLogsDurableCorrelationAndRetry(t *testing.T) {
	e, home := testEngine(t, func(context.Context, Activation) (Binding, error) {
		return Binding{}, fmt.Errorf("activation fixture failed")
	})
	logger, err := logs.Open(home, "desktop", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.options.Logger = logger.Logger
	socket := testHost(t, func(w http.ResponseWriter, r *http.Request) { hostResponse(w) })
	plan := planFor(t, e, socket)
	job := startPlan(t, e, plan, "log-start-request")
	failed := awaitJob(t, e, job.ID, "failed")
	if failed.Error == nil || !strings.Contains(failed.Error.Message, "activation fixture failed") {
		t.Fatalf("durable failure missing: %+v", failed)
	}
	if _, err = e.Retry(context.Background(), job.ID, ActionRequest{RequestID: "log-retry-request", ExpectedRevision: failed.Revision}); err != nil {
		t.Fatal(err)
	}
	awaitJob(t, e, job.ID, "failed")
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"preparation.failed", "preparation.retry", "activation fixture failed", job.ID, plan.ID, "log-start-request", "log-retry-request", "\"attempt\":2"} {
		if !bytes.Contains(b, []byte(value)) {
			t.Fatalf("missing %q in %s", value, b)
		}
	}
}

func TestLoggingFailureDoesNotUndoPreparationMutation(t *testing.T) {
	e, home := testEngine(t, nil)
	var fallback bytes.Buffer
	logger, err := logs.Open(home, "setup", &fallback)
	if err != nil {
		t.Fatal(err)
	}
	e.options.Logger = logger.Logger
	socket := testHost(t, func(w http.ResponseWriter, r *http.Request) { hostResponse(w) })
	plan := planFor(t, e, socket)
	if err := os.Rename(filepath.Join(home, "logs"), filepath.Join(home, "logs-before-failure")); err != nil {
		t.Fatal(err)
	}
	job := startPlan(t, e, plan, "logging-failure-start")
	final := awaitJob(t, e, job.ID, "succeeded")
	if !final.Committed || final.Error != nil {
		t.Fatalf("log failure altered committed task: %+v", final)
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	if logger.Status() == "" || !strings.Contains(fallback.String(), "preparation.succeeded") {
		t.Fatal("runtime logging failure not observable")
	}
}

func TestAcceptedCancellationIsNotLoggedAsPreparationFailure(t *testing.T) {
	var probes atomic.Int32
	entered := make(chan struct{})
	socket := testHost(t, func(w http.ResponseWriter, r *http.Request) {
		if probes.Add(1) > 1 {
			close(entered)
			<-r.Context().Done()
			return
		}
		hostResponse(w)
	})
	e, home := testEngine(t, nil)
	logger, err := logs.Open(home, "desktop", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.options.Logger = logger.Logger
	job := startPlan(t, e, planFor(t, e, socket), "log-cancel-start")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not enter the cancellable probe")
	}
	current, err := e.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Cancel(context.Background(), job.ID, ActionRequest{RequestID: "log-cancel-action", ExpectedRevision: current.Revision}); err != nil {
		t.Fatal(err)
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range bytes.Split(bytes.TrimSpace(contents), []byte{'\n'}) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record["level"] == "error" {
			t.Fatalf("accepted cancellation logged as a failure: %s", line)
		}
		if record["event"] == "preparation.cancelled" && record["jobId"] == job.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("cancellation event is missing: %s", contents)
	}
}
