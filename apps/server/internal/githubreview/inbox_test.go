package githubreview

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func addInboxJob(t *testing.T, db *store, account string, target Target, status, observedBase string, updated time.Time) {
	t.Helper()
	_, err := db.db.ExecContext(t.Context(), `INSERT INTO review_jobs(
		account,repository,number,title,base_sha,head_sha,source,status,updated_at,policy,checks,review_stage,observed_base_sha
	) VALUES(?,?,?,?,?,?,?,?,?,'{"rules":"","testCommand":[]}','{"status":"empty"}','{"status":"completed"}',?)`,
		account, target.Repository, target.Number, "Handle empty configuration", target.BaseSHA, target.HeadSHA, target.Source, status, updated.UnixMilli(), observedBase)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInboxRetainsOldDraftsAndPagesCompletedReviews(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	target := workerTestTarget("owner/alpha", 1, sourceRepository)
	finding := Finding{Path: "config/defaults.go", Side: "head", Line: 17, Title: "Initialize optional defaults", Severity: "medium"}
	current := reviewedCandidates(t, db, target, []Finding{finding}).Candidates[0]
	decision := candidateDecision{Account: "owner", Target: target, CandidateID: current.ID, Revision: current.Revision, Status: "confirmed", EvidenceIDs: []string{}}
	if err := db.saveCandidate(ctx, decision); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	draft := candidateCommentDraftRequest{Account: "owner", Target: target, CandidateID: current.ID, Body: "The empty group should receive defaults."}
	if err := db.saveCandidateCommentDraft(ctx, draft, now); err != nil {
		t.Fatal(err)
	}
	// Complete the old job's independent stages. Its unread comment is the only action.
	_, err := db.db.ExecContext(ctx, `UPDATE review_jobs SET checks='{"status":"empty"}',checks_due=NULL,
		tests=json_array(json_object('status','passed','checkoutSha',head_sha,'log','The older receipt remains available.'),json_object('status','passed','checkoutSha',head_sha,'log','Latest receipt.'))
		WHERE account='owner' AND repository=? AND number=?`, target.Repository, target.Number)
	if err != nil {
		t.Fatal(err)
	}
	for number := 2; number <= 140; number++ {
		recent := workerTestTarget("owner/alpha", number, sourceRepository)
		addInboxJob(t, db, "owner", recent, "clean", "", now.Add(time.Duration(number)*time.Second))
	}
	addInboxJob(t, db, "different-owner", target, "failed", "", now.Add(time.Hour))

	legacy, err := db.history(ctx, "owner")
	if err != nil || len(legacy) != 100 {
		t.Fatalf("legacy snapshot jobs = %d, %v", len(legacy), err)
	}
	for _, job := range legacy {
		if job.Number == target.Number {
			t.Fatal("fixture did not age beyond the legacy window")
		}
	}
	action, err := db.inboxPage(ctx, inboxAction, "", nil)
	if err != nil || action.Account != "owner" || action.NextCursor != "" || len(action.Jobs) != 1 || action.Jobs[0].Number != target.Number || action.Jobs[0].InboxGroup != "action" {
		t.Fatalf("old local draft lost: %+v, %v", action, err)
	}
	summary := action.Jobs[0]
	if len(summary.Evidence.Tests) != 1 || summary.Evidence.Tests[0].Log != "" || len(summary.Candidates) != 1 || summary.Candidates[0].CommentDraft.Body != "" {
		t.Fatalf("inbox summary contains full detail: %+v", summary)
	}
	original, err := db.inboxJob(ctx, target, nil)
	if err != nil || original.Account != "owner" || len(original.Job.Evidence.Tests) != 2 || original.Job.Evidence.Tests[0].Log != "The older receipt remains available." ||
		original.Job.Candidates[0].CommentDraft.Body != draft.Body {
		t.Fatalf("original evidence is unavailable: %+v, %v", original, err)
	}
	draft.Revision = 1
	if err := db.setCandidateCommentDraftReviewed(ctx, draft, true, now); err != nil {
		t.Fatal(err)
	}
	action, err = db.inboxPage(ctx, inboxAction, "", nil)
	if err != nil || len(action.Jobs) != 0 || action.NextCursor != "" {
		t.Fatalf("reviewed draft still blocks the inbox: %+v, %v", action, err)
	}
	first, err := db.inboxPage(ctx, inboxHistory, "", nil)
	if err != nil || len(first.Jobs) != inboxPageSize || first.NextCursor == "" {
		t.Fatalf("wrong first history page: %+v, %v", first, err)
	}
	if _, err := db.inboxPage(ctx, inboxActive, first.NextCursor, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("history cursor accepted as active: %v", err)
	}
	nextNumber := 140
	for page := first; ; {
		for _, job := range page.Jobs {
			if job.Number != nextNumber || job.InboxGroup != "recent" {
				t.Fatalf("lost or reordered review history: got #%d (%s), expected #%d", job.Number, job.InboxGroup, nextNumber)
			}
			nextNumber--
		}
		if page.NextCursor == "" {
			break
		}
		// Updates cannot move earlier jobs across the insertion cursor.
		if _, err := db.db.ExecContext(ctx, "UPDATE review_jobs SET updated_at=? WHERE account='owner' AND number=1", now.Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
		page, err = db.inboxPage(ctx, inboxHistory, page.NextCursor, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if nextNumber != 0 {
		t.Fatalf("history stopped before all jobs were returned: #%d", nextNumber)
	}
	if err := db.attach(ctx, "github", "different-owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.inboxPage(ctx, inboxHistory, first.NextCursor, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cursor accepted with different account: %v", err)
	}
}

func TestInboxRoutesResolveExactRevisionAndStaleTargets(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	now := time.Now()
	old := workerTestTarget("owner/alpha", 1, sourceRepository)
	replacement := old
	replacement.HeadSHA = strings.Repeat("d", 40)
	blocked := workerTestTarget("owner/beta", 1, sourceRequested)
	active := workerTestTarget("owner/alpha", 2, sourceRepository)
	addInboxJob(t, db, "owner", old, "stale", strings.Repeat("e", 40), now)
	addInboxJob(t, db, "owner", replacement, "clean", "", now.Add(time.Second))
	addInboxJob(t, db, "owner", blocked, "stale", strings.Repeat("e", 40), now)
	addInboxJob(t, db, "owner", active, "queued", "", now)
	if _, err := db.db.ExecContext(ctx, "UPDATE review_jobs SET policy=NULL,review_stage='{}' WHERE number=2"); err != nil {
		t.Fatal(err)
	}
	service := newService(ctx, func() {}, db, &workerGitHub{}, connectedCLI{}, &countedReviewer{})
	mux := http.NewServeMux()
	service.Register(mux)
	get := func(path string, status int, payload any) {
		t.Helper()
		r := reviewRequest(t, mux, http.MethodGet, route+path, "", "127.0.0.1:19792", "")
		if r.Code != status {
			t.Fatalf("GET %s returned %d, want %d: %s", path, r.Code, status, r.Body.String())
		}
		if payload != nil && json.Unmarshal(r.Body.Bytes(), payload) != nil {
			t.Fatalf("GET %s returned an invalid body: %s", path, r.Body.String())
		}
	}
	var summary Snapshot
	get("?summary=1", http.StatusOK, &summary)
	if summary.ReviewAccount != "owner" || !summary.HasJobs || summary.Jobs == nil || len(summary.Jobs) != 0 {
		t.Fatalf("summary is not the cheap account snapshot: %+v", summary)
	}
	var page InboxPage
	get("/inbox?view=action", http.StatusOK, &page)
	if len(page.Jobs) != 1 || page.Jobs[0].Repository != blocked.Repository || page.Jobs[0].InboxGroup != "action" {
		t.Fatalf("wrong stale blocker: %+v", page)
	}
	get("/inbox?view=active", http.StatusOK, &page)
	if len(page.Jobs) != 1 || page.Jobs[0].Number != active.Number || page.Jobs[0].InboxGroup != "active" {
		t.Fatalf("wrong active item: %+v", page)
	}
	get("/inbox?view=history", http.StatusOK, &page)
	if len(page.Jobs) != 2 || page.Jobs[0].HeadSHA != replacement.HeadSHA || page.Jobs[1].HeadSHA != old.HeadSHA || page.Jobs[1].InboxGroup != "earlier" {
		t.Fatalf("superseded review no longer reachable: %+v", page)
	}
	identity := url.Values{"repository": {old.Repository}, "number": {strconv.Itoa(old.Number)}, "headSha": {old.HeadSHA}, "baseSha": {old.BaseSHA}}
	var detail ReviewJobView
	get("/job?"+identity.Encode(), http.StatusOK, &detail)
	if detail.Account != "owner" || detail.Job.Status != "stale" || detail.Job.HeadSHA != old.HeadSHA {
		t.Fatalf("exact old revision missing: %+v", detail)
	}
	identity.Set("baseSha", strings.Repeat("f", 40))
	get("/job?"+identity.Encode(), http.StatusNotFound, nil)
	get("/inbox?view=action&view=history", http.StatusBadRequest, nil)
	get("/inbox?view=action&account=different-owner", http.StatusBadRequest, nil)
	get("/job?number=1", http.StatusBadRequest, nil)
	get("?summary=1&other=1", http.StatusBadRequest, nil)
	remote := reviewRequest(t, mux, http.MethodGet, route+"/inbox?view=action", "", "192.0.2.32:19792", "")
	if remote.Code != http.StatusForbidden {
		t.Fatalf("remote inbox response = %d", remote.Code)
	}
}
