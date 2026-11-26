package githubreview

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCandidateCommentDraftRevisionsAndHistory(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "review.sqlite")
	db := testReviewStore(t, filename, true)
	first := workerTestTarget("owner/alpha", 7, sourceRepository)
	finding := Finding{Path: "app/config.go", Side: "base", Line: 80, Title: "Missing defaults prevent initialization", Body: "Unconfigured instances fail to start.", Severity: "medium"}
	original := reviewedCandidates(t, db, first, []Finding{finding}).Candidates[0]
	assessment := candidateDecision{Account: "owner", Target: first, CandidateID: original.ID, Revision: original.Revision, Status: "confirmed", Note: "My internal note", EvidenceIDs: []string{}}
	if err := db.saveCandidate(ctx, assessment); err != nil {
		t.Fatal(err)
	}
	req := candidateCommentDraftRequest{Account: "owner", Target: first, CandidateID: original.ID, Body: "Example\r\n\r\n    go test ./app\r\n"}
	now := time.Now().Truncate(time.Millisecond)
	if err := db.saveCandidateCommentDraft(ctx, req, now); !errors.Is(err, errCandidateDraftConflict) {
		t.Fatalf("accepted a base-side draft with no recorded diff merge base: %v", err)
	}
	merge := strings.Repeat("c", 40)
	if _, err := db.db.ExecContext(ctx, "UPDATE review_jobs SET merge_base_sha=? WHERE number=?", merge, first.Number); err != nil {
		t.Fatal(err)
	}
	s := newService(ctx, func() {}, db, &workerGitHub{}, connectedCLI{}, &countedReviewer{})
	mux := http.NewServeMux()
	s.Register(mux)
	params := map[string]any{"account": req.Account, "repository": req.Repository, "number": req.Number, "baseSha": req.BaseSHA, "headSha": req.HeadSHA, "revision": req.Revision, "candidateId": req.CandidateID, "body": req.Body}
	post := func(origin string, want int) {
		t.Helper()
		body, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		res := reviewRequest(t, mux, "POST", route+"/candidate-draft", origin, "127.0.0.1:19792", string(body))
		if res.Code != want {
			t.Fatalf("draft save returned %d, want %d: %s", res.Code, want, res.Body.String())
		}
	}
	post("https://untrusted.example", http.StatusForbidden)
	params["path"] = "other.go"
	post("http://localhost", http.StatusBadRequest)
	delete(params, "path")
	post("http://localhost", http.StatusOK)
	post("http://localhost", http.StatusConflict)
	saved := candidateTestView(t, db, first).Candidates[0]
	draft := saved.CommentDraft
	if draft == nil || draft.Stale || draft.Revision != 1 || draft.AnchorSHA != merge || draft.Body != "Example\n\n    go test ./app\n" || draft.BaseSHA != first.BaseSHA || draft.HeadSHA != first.HeadSHA || draft.Path != finding.Path || draft.Line != finding.Line || draft.Side != "base" {
		t.Fatalf("invalid pinned draft: %+v", draft)
	}
	if saved.Note != assessment.Note || saved.Status != assessment.Status || saved.Revision != original.Revision+1 {
		t.Fatalf("draft save changed assessment: %+v", saved)
	}
	req.Revision = 1
	req.Body = strings.Repeat("é", maxCommentDraftBytes/2+1)
	if err := db.saveCandidateCommentDraft(ctx, req, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("accepted draft over UTF-8 byte limit: %v", err)
	}
	req.Body = "A revised draft"
	if err := db.saveCandidateCommentDraft(ctx, req, now); err != nil {
		t.Fatal(err)
	}
	if err := db.db.Close(); err != nil {
		t.Fatal(err)
	}
	db = testReviewStore(t, filename, false)
	second := first
	second.HeadSHA = strings.Repeat("d", 40)
	finding.Line++
	current := reviewedCandidates(t, db, second, []Finding{finding}).Candidates[0]
	if current.CommentDraft != nil || len(current.History) != 1 || current.History[0].CommentDraft == nil {
		t.Fatalf("draft history lost or wrongly carried forward: %+v", current)
	}
	history := current.History[0].CommentDraft
	if !history.Stale || history.Revision != 2 || history.Body != req.Body || history.Line != finding.Line-1 || !history.UpdatedAt.Equal(now) {
		t.Fatalf("draft history changed: %+v", history)
	}
	req.Revision = 2
	if err := db.saveCandidateCommentDraft(ctx, req, now); !errors.Is(err, errCandidateDraftConflict) {
		t.Fatalf("accepted a draft save for an old revision: %v", err)
	}
}

func TestCandidateCommentDraftReviewedIsExactAndLocal(t *testing.T) {
	ctx := t.Context()
	db := testReviewStore(t, filepath.Join(t.TempDir(), "review.sqlite"), true)
	first := workerTestTarget("owner/alpha", 7, sourceRepository)
	finding := Finding{Path: "main.go", Side: "head", Line: 12, Title: "Handle empty input", Body: "An empty data set skips initialization.", Severity: "medium"}
	candidate := reviewedCandidates(t, db, first, []Finding{finding}).Candidates[0]
	req := candidateCommentDraftRequest{Account: "owner", Target: first, CandidateID: candidate.ID, Body: "A local comment draft"}
	now := time.Now().Truncate(time.Millisecond)
	if err := db.saveCandidateCommentDraft(ctx, req, now); err != nil {
		t.Fatal(err)
	}
	s := newService(ctx, func() {}, db, &workerGitHub{}, connectedCLI{}, &countedReviewer{})
	mux := http.NewServeMux()
	s.Register(mux)
	params := map[string]any{"account": req.Account, "repository": req.Repository, "number": req.Number, "baseSha": req.BaseSHA, "headSha": req.HeadSHA,
		"candidateId": req.CandidateID, "revision": 1, "reviewed": true}
	post := func(origin string, want int) {
		t.Helper()
		body, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		res := reviewRequest(t, mux, "POST", route+"/candidate-draft-reviewed", origin, "127.0.0.1:19792", string(body))
		if res.Code != want {
			t.Fatalf("acknowledgment returned %d, want %d: %s", res.Code, want, res.Body.String())
		}
	}
	post("https://untrusted.example", http.StatusForbidden)
	delete(params, "reviewed")
	post("http://localhost", http.StatusBadRequest)
	params["reviewed"] = true
	params["account"] = "another-owner"
	post("http://localhost", http.StatusConflict)
	params["account"] = "owner"
	post("http://localhost", http.StatusOK)
	firstDraft := candidateTestView(t, db, first).Candidates[0].CommentDraft
	if firstDraft == nil || firstDraft.ReviewedAt == nil || firstDraft.Body != req.Body || firstDraft.Revision != 1 || !firstDraft.UpdatedAt.Equal(now) {
		t.Fatalf("acknowledgment changed draft contents or missed timestamp: %+v", firstDraft)
	}
	post("http://localhost", http.StatusOK)
	if read := candidateTestView(t, db, first).Candidates[0].CommentDraft.ReviewedAt; read == nil || !read.Equal(*firstDraft.ReviewedAt) {
		t.Fatalf("replayed acknowledgment changed its timestamp: %v", read)
	}
	params["reviewed"] = false
	post("http://localhost", http.StatusOK)
	if read := candidateTestView(t, db, first).Candidates[0].CommentDraft.ReviewedAt; read != nil {
		t.Fatalf("undo left acknowledgment: %v", read)
	}
	params["reviewed"] = true
	post("http://localhost", http.StatusOK)
	req.Revision, req.Body = 1, "Edited draft"
	if err := db.saveCandidateCommentDraft(ctx, req, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	post("http://localhost", http.StatusConflict)
	editedDraft := candidateTestView(t, db, first).Candidates[0].CommentDraft
	if editedDraft.ReviewedAt != nil || editedDraft.Revision != 2 || editedDraft.Body != req.Body {
		t.Fatalf("editing didn't return the draft for review: %+v", editedDraft)
	}
	second := first
	second.HeadSHA = strings.Repeat("d", 40)
	reviewedCandidates(t, db, second, []Finding{finding})
	params["revision"] = 2
	post("http://localhost", http.StatusConflict)
}
