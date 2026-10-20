package control

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestProjectTaskFailureMetadataIsBoundedWithoutChangingFinalOutcome(t *testing.T) {
	f := newTaskFixture(t)
	accepted := f.submit(t, "bounded-failure-original")
	reason := strings.Repeat("项目<\n\x1b", 4096) + "\xff"
	if err := f.service.store.saveTaskFailure(t.Context(), accepted.Attempt.ID, "failed", reason); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.store.taskResult(t.Context(), f.bound.Project.ID, accepted.Task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	check := func(value string) {
		t.Helper()
		if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
			t.Fatalf("unbounded or unsafe task metadata: %d bytes", len(value))
		}
	}
	check(result.Attempt.Reason)
	if result.Attempt.Status != "failed" || result.Attempt.FinishedAt == nil {
		t.Fatalf("missing final failure: %+v", result.Attempt)
	}
	if err := f.service.store.saveTaskFailure(t.Context(), accepted.Attempt.ID, "unknown", reason); err != nil {
		t.Fatal(err)
	}
	updated, err := f.service.store.taskResult(t.Context(), f.bound.Project.ID, accepted.Task.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	check(updated.Attempt.SyncError)
	if updated.Attempt.Status != "failed" || updated.Attempt.Reason != result.Attempt.Reason || !updated.Attempt.FinishedAt.Equal(*result.Attempt.FinishedAt) {
		t.Fatalf("synchronization failure changed final outcome: %+v", updated.Attempt)
	}
}
