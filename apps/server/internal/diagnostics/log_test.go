package diagnostics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
)

func events(t *testing.T, output []byte) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(output), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("invalid event %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func TestEventsRedactAndBoundDiagnostics(t *testing.T) {
	t.Setenv("AXV_TEST_SECRET", "fixture-environment-secret")
	t.Setenv("HTTPS_PROXY", "socks5://proxy-user:fixture-proxy-password@example.test:1080")
	var output, fallback bytes.Buffer
	logger := diagnostics.New("host", &output, &fallback)
	logger.With("operationId", "op-123", "token", "unknown-sensitive-value").Error("download.failed",
		fmt.Errorf("TLS certificate verify failed at https://user:fixture-password@example.test/a?signature=private-query#private-fragment; socks5://proxy-user:proxy-value@example.test:1080; proxy connection user:fixture-proxy-password; fixture-environment-secret; Authorization: Bearer bearer-not-known; Authorization: Basic basic-not-known"),
		"diagnostic", "first\nsecond", "long", strings.Repeat("x", 12<<10), "payload", map[string]string{"text": "private payload"})
	logger.Info("service.ready")
	records := events(t, output.Bytes())
	if len(records) != 2 || records[0]["operationId"] != "op-123" || records[0]["role"] != "host" || records[0]["level"] != "error" || records[0]["diagnostic"] != "first\nsecond" {
		t.Fatalf("lost event context: %+v", records)
	}
	if _, exists := records[1]["operationId"]; exists {
		t.Fatal("derived fields changed the original logger")
	}
	if records[0]["time"] == nil || records[0]["pid"] == nil || records[1]["level"] != "info" {
		t.Fatalf("missing event metadata: %+v", records)
	}
	for _, secret := range []string{"fixture-environment-secret", "fixture-password", "private-query", "private-fragment", "proxy-value", "fixture-proxy-password", "unknown-sensitive-value", "bearer-not-known", "basic-not-known", "private payload"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("diagnostic retained %q", secret)
		}
	}
	if !strings.Contains(output.String(), "TLS certificate verify failed") || !strings.Contains(output.String(), "truncated") {
		t.Fatal("useful failure cause or truncation marker lost")
	}
	if fallback.Len() != 0 || logger.Status() != "" {
		t.Fatalf("healthy output reported failure: %q %q", fallback.String(), logger.Status())
	}
}

func TestOversizedEventIsReplacedWithBoundedDiagnostic(t *testing.T) {
	var output bytes.Buffer
	logger := diagnostics.New("host", &output, io.Discard)
	logger.With("operationId", "op-large").Info("operation.progress", "one", strings.Repeat("a", 10<<10), "two", strings.Repeat("b", 10<<10), "three", strings.Repeat("c", 10<<10), "four", strings.Repeat("d", 10<<10))
	records := events(t, output.Bytes())
	if len(records) != 1 || output.Len() > 32<<10 || records[0]["error"] != "operational event exceeded record limit" || records[0]["event"] != "operation.progress" || records[0]["role"] != "host" {
		t.Fatalf("oversized record was not bounded: %d bytes, %+v", output.Len(), records)
	}
}

func TestNilLoggerAndContext(t *testing.T) {
	var logger *diagnostics.Logger
	logger.Info("ignored")
	logger.Error("ignored", errors.New("ignored"))
	if logger.With("id", 1) != nil || logger.Status() != "" {
		t.Fatal("nil logger acquired state")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if diagnostics.Context(ctx, nil) != ctx || diagnostics.FromContext(ctx) != nil {
		t.Fatal("absent logger changed context")
	}
	logger = diagnostics.New("host", io.Discard, io.Discard).With("operationId", "op-context")
	withLogger := diagnostics.Context(ctx, logger)
	if diagnostics.FromContext(withLogger) != logger {
		t.Fatal("context lost its logger")
	}
	cancel()
	if !errors.Is(withLogger.Err(), context.Canceled) {
		t.Fatal("logger context lost cancellation")
	}
}

func TestDerivedLoggersSerializeConcurrentEvents(t *testing.T) {
	var output bytes.Buffer
	logger := diagnostics.New("host", &output, io.Discard)
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			derived := logger.With("worker", worker)
			for index := range 40 {
				derived.Info("operation.progress", "index", index)
			}
		}()
	}
	workers.Wait()

	records := events(t, output.Bytes())
	seen := map[string]bool{}
	for _, record := range records {
		key := fmt.Sprint(record["worker"], "/", record["index"])
		if seen[key] || record["role"] != "host" {
			t.Fatalf("duplicate or invalid event: %v", record)
		}
		seen[key] = true
	}
	if len(seen) != 320 {
		t.Fatalf("lost concurrent events: %d", len(seen))
	}
}

type failingWriter struct {
	bytes.Buffer
	failure error
	short   bool
}

func (w *failingWriter) Write(record []byte) (int, error) {
	if w.failure != nil {
		return 0, w.failure
	}
	if w.short {
		return len(record) - 1, nil
	}
	return w.Buffer.Write(record)
}

func TestDerivedLoggersShareFailureAndRecovery(t *testing.T) {
	t.Setenv("AXV_TEST_SECRET", "fixture-writer-secret")
	output := &failingWriter{failure: errors.New("write failed: fixture-writer-secret")}
	var fallback bytes.Buffer
	logger := diagnostics.New("host", output, &fallback)
	derived := logger.With("operationId", "op-committed")
	derived.Info("operation.complete")
	if logger.Status() == "" || logger.Status() != derived.Status() || !strings.Contains(fallback.String(), "op-committed") {
		t.Fatal("failed output lost shared status or fallback event")
	}
	if strings.Contains(logger.Status(), "fixture-writer-secret") || strings.Contains(fallback.String(), "fixture-writer-secret") {
		t.Fatal("output failure leaked an environment secret")
	}
	output.failure = nil
	logger.Info("logging.recovered")
	if derived.Status() != "" {
		t.Fatal("successful output retained failure status")
	}
	if records := events(t, output.Bytes()); len(records) != 1 || records[0]["event"] != "logging.recovered" {
		t.Fatalf("recovered writer did not receive event: %v", records)
	}
	output.short = true
	derived.Info("operation.complete")
	if logger.Status() != io.ErrShortWrite.Error() {
		t.Fatalf("short write was not reported: %q", logger.Status())
	}
}

func TestStderrLeavesWriteFailuresToSupervisor(t *testing.T) {
	for _, output := range []*failingWriter{{failure: errors.New("supervisor output unavailable")}, {short: true}} {
		logger := diagnostics.Stderr("host", output)
		logger.Info("operation.complete")
		if logger.Status() != "" {
			t.Fatalf("stderr logger reported a profile sink failure: %q", logger.Status())
		}
	}
}
