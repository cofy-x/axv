//go:build linux || darwin

package artifactcache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/diagnostics"
	"github.com/cofy-x/axv/apps/server/internal/profile/logs"
)

func artifactLog(t *testing.T, fallback *bytes.Buffer) (context.Context, *logs.Log) {
	t.Helper()
	logger, err := logs.Open(t.TempDir(), "preparation", fallback)
	if err != nil {
		t.Fatal(err)
	}
	logger.Logger = logger.With("role", "desktop", "jobId", "job-reviewed", "requestId", "request-original")
	return diagnostics.Context(context.Background(), logger.Logger), logger
}

func artifactRecords(t *testing.T, logger *logs.Log) ([]map[string]any, string) {
	t.Helper()
	raw, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record["jobId"] != "job-reviewed" || record["requestId"] != "request-original" || record["artifactId"] != "fixture" {
			t.Fatalf("artifact event lost its task identity: %s", line)
		}
		records = append(records, record)
	}
	return records, string(raw)
}

func TestArtifactEventsCorrelateColdAndWarmCopiesWithoutPayloads(t *testing.T) {
	body := []byte("private artifact payload is not operational logging")
	server, artifact := fixture(t, body, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	artifact.URL += "/image?signature=transfer-secret#private-fragment"
	var fallback bytes.Buffer
	ctx, logger := artifactLog(t, &fallback)
	cache := testCache(t)
	for i := range 2 {
		destination := filepath.Join(t.TempDir(), fmt.Sprintf("image-%d", i))
		if _, err := cache.CopyTo(ctx, server.Client(), artifact, destination, destination+".part", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	records, contents := artifactRecords(t, logger)
	events := map[string]int{}
	for _, record := range records {
		events[record["event"].(string)]++
	}
	if events["artifact.cache.miss"] != 1 || events["artifact.cache.hit"] != 1 || events["artifact.download.complete"] != 1 || events["artifact.copy.complete"] != 2 {
		t.Fatalf("cold/warm events: %v", events)
	}
	for _, excluded := range []string{string(body), "transfer-secret", "private-fragment"} {
		if strings.Contains(contents, excluded) {
			t.Fatalf("log leaked non-diagnostic data: %s", excluded)
		}
	}
	if fallback.Len() != 0 {
		t.Fatalf("unexpected logging fallback: %s", fallback.String())
	}
}

type diagnosticTransport struct{}

func (diagnosticTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("proxyconnect tcp: connection refused through http://diagnostic-user:diagnostic-password@127.0.0.1:9?token=proxy-secret")
}

func TestTransportCauseIsLoggedBeforePublicErrorMapping(t *testing.T) {
	var fallback bytes.Buffer
	ctx, logger := artifactLog(t, &fallback)
	cache := testCache(t)
	artifact := Artifact{ID: "fixture", URL: "https://source.invalid/image?signature=transfer-secret", SHA256: strings.Repeat("a", 64), MaxBytes: 1024}
	_, err := cache.CopyTo(ctx, &http.Client{Transport: diagnosticTransport{}}, artifact, "", "", "", nil)
	if err == nil || strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "diagnostic-password") {
		t.Fatalf("public error contract changed: %v", err)
	}
	records, contents := artifactRecords(t, logger)
	found := false
	for _, record := range records {
		if record["event"] == "artifact.download.transport_failed" {
			found = strings.Contains(fmt.Sprint(record["error"]), "connection refused")
		}
	}
	if !found {
		t.Fatalf("underlying transport cause is missing: %s", contents)
	}
	for _, excluded := range []string{"diagnostic-user", "diagnostic-password", "proxy-secret", "transfer-secret"} {
		if strings.Contains(contents, excluded) {
			t.Fatalf("transport log leaked %s", excluded)
		}
	}
}

func TestLogFailureDoesNotChangeVerifiedCopyOutcome(t *testing.T) {
	body := []byte("verified download survives unavailable diagnostics")
	server, artifact := fixture(t, body, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	var fallback bytes.Buffer
	ctx, logger := artifactLog(t, &fallback)
	cache := testCache(t)
	if _, err := cache.CopyTo(ctx, server.Client(), artifact, "", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(logger.Path()), 0755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "image")
	status, err := cache.CopyTo(ctx, server.Client(), artifact, destination, destination+".part", "", nil)
	if err != nil || status.State != "ready" {
		t.Fatalf("logging changed successful operation: %+v %v", status, err)
	}
	if err := VerifyFile(ctx, destination, artifact.SHA256, artifact.MaxBytes); err != nil {
		t.Fatal(err)
	}
	if logger.Status() == "" || fallback.Len() == 0 {
		t.Fatal("logging failure was hidden")
	}
}
