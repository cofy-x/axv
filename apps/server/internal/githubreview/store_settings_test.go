package githubreview

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewStoreRejectsAnIncompatibleProfileWithoutChangingIt(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "reviews.sqlite")
	old, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err := old.ExecContext(ctx, `
		PRAGMA application_id = 0x41585231;
		PRAGMA user_version = 12;
		CREATE TABLE previous_review_records (body TEXT);
		INSERT INTO previous_review_records VALUES ('retained review data');
	`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if ledger, err := openStore(ctx, filename); err == nil {
		defer ledger.db.Close()
		t.Fatal("opened an incompatible review profile")
	}
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("opening an incompatible profile changed its database")
	}
}

func TestReviewDailyLimitPersistsAcrossRestartAndAccountChange(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "jobs.sqlite")
	ledger, err := openStore(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.db.Close()
	if err := ledger.attach(ctx, "github", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.attach(ctx, "codex", "chatgpt"); err != nil {
		t.Fatal(err)
	}
	settings, err := ledger.settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config := settings.Config
	config.Repositories = []string{"owner/example"}
	config.Enabled = true
	config.DailyLimit = 1
	if err := ledger.configure(ctx, config); err != nil {
		t.Fatal(err)
	}

	local := time.FixedZone("workstation", 8*60*60)
	beforeMidnight := time.Date(2026, 11, 20, 23, 59, 0, 0, local)
	afterMidnight := beforeMidnight.Add(2 * time.Minute)
	target := Target{Repository: "owner/example", Number: 12,
		BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40), Source: sourceRepository}
	if err := ledger.enqueue(ctx, "owner", []Target{target}, []Target{target}, beforeMidnight); err != nil {
		t.Fatal(err)
	}
	job, err := ledger.job(ctx, "owner", target.Repository, target.Number, target.HeadSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.startReview(ctx, job, beforeMidnight); err != nil {
		t.Fatal(err)
	}
	if err := ledger.db.Close(); err != nil {
		t.Fatal(err)
	}

	ledger, err = openStore(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.db.Close()
	if err := ledger.attach(ctx, "github", "new-owner"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.enqueue(ctx, "new-owner", []Target{target}, []Target{target}, beforeMidnight); err != nil {
		t.Fatal(err)
	}
	job, err = ledger.job(ctx, "new-owner", target.Repository, target.Number, target.HeadSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.startReview(ctx, job, beforeMidnight); !errors.Is(err, ErrDailyLimit) {
		t.Fatalf("expected the previous account to exhaust the same budget, got %v", err)
	}
	budget, err := ledger.budget(ctx, beforeMidnight)
	if err != nil || budget.Used != 1 || !budget.ResetsAt.Equal(afterMidnight.Truncate(time.Hour)) {
		t.Fatalf("unexpected persisted budget: %+v, %v", budget, err)
	}
	budget, err = ledger.budget(ctx, afterMidnight)
	if err != nil || budget.Used != 0 {
		t.Fatalf("local midnight did not reset the budget: %+v, %v", budget, err)
	}
	if err := ledger.startReview(ctx, job, afterMidnight); err != nil {
		t.Fatal(err)
	}
	if err := ledger.startReview(ctx, job, afterMidnight); !errors.Is(err, ErrDailyLimit) {
		t.Fatalf("expected the new day's budget to be exhausted, got %v", err)
	}
}
