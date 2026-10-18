//go:build linux || darwin

package control

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/compute"
	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
)

type failingOperationDriver struct{ configurationDriver }

func (d *failingOperationDriver) Start(context.Context, compute.Machine, compute.Image, compute.Volume) error {
	return fmt.Errorf("provider startup failed at https://user:provider-password@example.test/vm?secret=query-secret")
}
func (d *failingOperationDriver) Inspect(context.Context, compute.Machine) (compute.Observation, error) {
	return compute.Observation{Status: "stopped"}, nil
}

func TestOperationFailureIsDurableAndLoggedEvenWhenSinkFails(t *testing.T) {
	for _, failSink := range []bool{false, true} {
		t.Run(fmt.Sprint(failSink), func(t *testing.T) {
			store, config, machine := configurationFixture(t)
			home := t.TempDir()
			var fallback bytes.Buffer
			logger, err := logs.Open(home, "host", &fallback)
			if err != nil {
				t.Fatal(err)
			}
			if failSink {
				if err := os.Rename(filepath.Join(home, "logs"), filepath.Join(home, "old-logs")); err != nil {
					t.Fatal(err)
				}
			}
			result, err := store.Action(context.Background(), config.Capacity, machine.ID, compute.ActionRequest{RequestID: "logging-operation-start", Revision: machine.Revision, Action: "start"})
			if err != nil {
				t.Fatal(err)
			}
			service := &Service{config: config, store: store, drivers: map[string]compute.Driver{"lima": &failingOperationDriver{}}, Logger: logger.Logger}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); service.worker(ctx) }()
			var operation operationRecord
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				operation, err = store.Operation(context.Background(), result.Operation.ID)
				if err != nil || operation.Status == "failed" {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			<-done
			if err != nil || operation.Status != "failed" || !strings.Contains(operation.Error, "provider startup failed") {
				t.Fatalf("operation result changed: %+v %v", operation, err)
			}
			var data []byte
			if failSink {
				data = fallback.Bytes()
				if logger.Status() == "" {
					t.Fatal("runtime log failure not observable")
				}
			} else {
				data, err = os.ReadFile(logger.Path())
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, value := range []string{"operation.started", "operation.phase", "operation.failed", operation.ID, machine.ID, "logging-operation-start", "provider startup failed"} {
				if !bytes.Contains(data, []byte(value)) {
					t.Fatalf("missing event correlation %q in %s", value, data)
				}
			}
			if bytes.Contains(data, []byte("provider-password")) || bytes.Contains(data, []byte("query-secret")) {
				t.Fatalf("provider error leaked credentials: %s", data)
			}
		})
	}
}

type closeStoreDriver struct {
	configurationDriver
	store *Store
}

func (d *closeStoreDriver) Start(context.Context, compute.Machine, compute.Image, compute.Volume) error {
	return d.store.Close()
}
func (d *closeStoreDriver) Inspect(context.Context, compute.Machine) (compute.Observation, error) {
	return compute.Observation{Status: "stopped"}, nil
}

func TestCancellationDuringFailedPersistenceDoesNotLogTerminalOutcome(t *testing.T) {
	store, config, machine := configurationFixture(t)
	logger, err := logs.Open(t.TempDir(), "host", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Action(context.Background(), config.Capacity, machine.ID, compute.ActionRequest{RequestID: "persistence-cancel-test", Revision: machine.Revision, Action: "start"}); err != nil {
		t.Fatal(err)
	}
	service := &Service{config: config, store: store, drivers: map[string]compute.Driver{"lima": &closeStoreDriver{store: store}}, Logger: logger.Logger}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); service.worker(ctx) }()
	observed := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		data, _ := os.ReadFile(logger.Path())
		if bytes.Contains(data, []byte(`"event":"operation.persistence_failed"`)) {
			observed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if !observed {
		t.Fatal("persistence failure was not recorded")
	}
	data, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, terminal := range []string{`"event":"operation.completed"`, `"event":"operation.failed"`} {
		if bytes.Contains(data, []byte(terminal)) {
			t.Fatalf("uncommitted operation reported terminal outcome: %s", data)
		}
	}
}
