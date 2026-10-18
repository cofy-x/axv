package prepare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPreparationFailurePreservesRetryPolicyAndStoredShape(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		code      string
		retryable bool
	}{
		{"invalid input", problem(KindInvalidInput, "confirmation_required", "Original detail."), "confirmation_required", false},
		{"missing plan", problem(KindNotFound, "plan_not_found", "Original detail."), "plan_not_found", false},
		{"identity conflict", problem(KindConflict, "host_changed", "Original detail."), "host_changed", false},
		{"unavailable", problem(KindUnavailable, "preparation_stopping", "Original detail."), "preparation_stopping", true},
		{"activation", problem(KindUnavailable, "activation_failed", "Original detail."), "activation_failed", true},
		{"download exception", problem(KindConflict, "download_failed", "Original detail."), "download_failed", true},
		{"activation exception", problem(KindConflict, "activation_failed", "Original detail."), "activation_failed", true},
		{"internal", problem(KindInternal, "preparation_failed", "Original detail."), "preparation_failed", true},
		{"unexpected", errors.New("Original detail."), "preparation_failed", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := failure(fmt.Errorf("wrapped: %w", tc.err))
			if result.Code != tc.code || result.Retryable != tc.retryable {
				t.Fatalf("unexpected failure: %+v", result)
			}
			if tc.name != "unexpected" && result.Message != "Original detail." {
				t.Fatalf("domain error message changed: %s", result.Message)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var stored map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &stored); err != nil {
				t.Fatal(err)
			}
			if len(stored) != 3 || stored["code"] == nil || stored["message"] == nil || stored["retryable"] == nil {
				t.Fatalf("persisted failure shape changed: %s", encoded)
			}
		})
	}
}

func TestActivationIdentityConflictCannotBeRetried(t *testing.T) {
	e, _ := testEngine(t, func(context.Context, Activation) (Binding, error) {
		binding := testBinding()
		binding.HostID = "replacement-host"
		return binding, nil
	})
	socket := testHost(t, func(w http.ResponseWriter, _ *http.Request) { hostResponse(w) })
	job := startPlan(t, e, planFor(t, e, socket), "identity-conflict-start")
	failed := awaitJob(t, e, job.ID, "failed")
	if !failed.Committed || failed.CanRetry || failed.CanCancel || failed.Error == nil || failed.Error.Code != "host_changed" || failed.Error.Retryable {
		t.Fatalf("invalid identity conflict state: %+v", failed)
	}
	_, err := e.Retry(context.Background(), job.ID, ActionRequest{RequestID: "identity-conflict-retry", ExpectedRevision: failed.Revision})
	assertCode(t, err, "retry_unavailable")
	if !strings.Contains(failed.Error.Message, "different identity") {
		t.Fatalf("unexpected failure: %+v", failed.Error)
	}
}
