package githubreview

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

const route = "/api/github-review"

var errVMRegistryUnavailable = errors.New("GitHub Review could not load its VM registry. Check AXV_REVIEW_VM_CONFIG and restart AXV.")

func (s *Service) Register(mux *http.ServeMux) {
	register(mux, s.serve)
}

func RegisterUnavailable(mux *http.ServeMux, vmRegistryInvalid bool) {
	failure := errStore
	if vmRegistryInvalid {
		failure = errVMRegistryUnavailable
	}
	register(mux, func(w http.ResponseWriter, _ *http.Request) {
		writeFailure(w, failure)
	})
}

func register(mux *http.ServeMux, handler http.HandlerFunc) {
	local := func(w http.ResponseWriter, r *http.Request) {
		if !httpaccess.LocalRequest(r, r.Method != http.MethodGet) {
			writeResponse(w, http.StatusForbidden, map[string]string{
				"code": "github_review_local_only", "error": "Open GitHub Review on the computer running AXV.",
			})
			return
		}
		handler(w, r)
	}
	mux.HandleFunc(route, local)
	mux.HandleFunc(route+"/", local)
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, route)
	if r.Method == http.MethodGet {
		switch path {
		case "":
			if r.URL.RawQuery == "summary=1" {
				view, err := s.readSnapshot(r.Context(), true)
				writeReviewRead(w, view, err)
				return
			}
		case "/inbox":
			s.serveInbox(w, r)
			return
		case "/job":
			s.serveReviewJob(w, r)
			return
		case "/vm-readiness":
			query, err := allowedQuery(r.URL.RawQuery, "repository")
			repository := query.Get("repository")
			if err != nil || !validRepository(repository) {
				writeFailure(w, ErrInvalid)
				return
			}
			writeResponse(w, http.StatusOK, s.reviewVMReadiness(r.Context(), repository))
			return
		}
	}
	if r.URL.RawQuery != "" {
		writeFailure(w, ErrInvalid)
		return
	}
	ctx := r.Context()
	var err error
	var selected selectedReview
	switch {
	case r.Method == http.MethodGet && path == "":
	case r.Method == http.MethodPut && path == "/config":
		var config Config
		config, err = parseConfig(w, r)
		if err == nil {
			err = s.configure(ctx, config)
		}
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/candidate-comparison/"):
		var run CandidateComparison
		run, err = s.comparisonDetail(ctx, strings.TrimPrefix(path, "/candidate-comparison/"))
		if err == nil {
			writeResponse(w, http.StatusOK, run)
			return
		}
	case r.Method == http.MethodPost && path == "/candidate-comparison":
		var q comparisonRequest
		err = readObject(w, r, 64<<10, map[string]any{"account": &q.Account, "repository": &q.Repository, "number": &q.Number,
			"baseSha": &q.BaseSHA, "headSha": &q.HeadSHA, "revision": &q.Revision, "candidateId": &q.CandidateID,
			"requestId": &q.RequestID, "runtimeId": &q.RuntimeID, "assertion": &q.Assertion})
		if err == nil {
			err = s.requestComparison(ctx, q)
		}
	case r.Method == http.MethodPost && path == "/approve-comparison-scenario":
		var req approveScenarioRequest
		err = readObject(w, r, 4<<10, map[string]any{
			"account": &req.Account, "repository": &req.Repository, "number": &req.Number, "baseSha": &req.BaseSHA,
			"headSha": &req.HeadSHA, "revision": &req.Revision, "candidateId": &req.CandidateID,
			"comparisonId": &req.ComparisonID, "name": &req.Name, "enable": &req.Enable,
		})
		if err == nil {
			err = s.approveComparisonScenario(ctx, req)
		}
	case r.Method == http.MethodPost && path == "/candidate-followup":
		var req candidateDecision
		err = readObject(w, r, 2<<10, map[string]any{"account": &req.Account, "repository": &req.Repository, "number": &req.Number, "baseSha": &req.BaseSHA, "headSha": &req.HeadSHA, "previousCandidateId": &req.PreviousID})
		if err == nil {
			err = s.store.followCandidate(ctx, req)
		}
	case r.Method == http.MethodPost && path == "/candidate-draft":
		var req candidateCommentDraftRequest
		err = readObject(w, r, 64<<10, map[string]any{"account": &req.Account, "repository": &req.Repository, "number": &req.Number,
			"baseSha": &req.BaseSHA, "headSha": &req.HeadSHA, "candidateId": &req.CandidateID, "revision": &req.Revision, "body": &req.Body})
		if err == nil {
			err = s.store.saveCandidateCommentDraft(ctx, req, time.Now())
		}
	case r.Method == http.MethodPost && path == "/candidate-draft-reviewed":
		var req candidateCommentDraftRequest
		var reviewed bool
		err = readObject(w, r, 2<<10, map[string]any{"account": &req.Account, "repository": &req.Repository, "number": &req.Number,
			"baseSha": &req.BaseSHA, "headSha": &req.HeadSHA, "candidateId": &req.CandidateID, "revision": &req.Revision, "reviewed": &reviewed})
		if err == nil {
			err = s.store.setCandidateCommentDraftReviewed(ctx, req, reviewed, time.Now())
		}
	case r.Method == http.MethodPost && path == "/candidate":
		var req candidateDecision
		err = readObject(w, r, 32<<10, map[string]any{"account": &req.Account, "repository": &req.Repository, "number": &req.Number,
			"baseSha": &req.BaseSHA, "headSha": &req.HeadSHA, "candidateId": &req.CandidateID, "revision": &req.Revision, "status": &req.Status,
			"note": &req.Note, "evidenceIds": &req.EvidenceIDs, "previousCandidateId": &req.PreviousID})
		if err == nil {
			err = s.store.saveCandidate(ctx, req)
		}
	case r.Method == http.MethodPost && path == "/run":
		err = s.requestRun(ctx)
	case r.Method == http.MethodPost && path == "/review-pr":
		var request reviewPRRequest
		err = readObject(w, r, 2<<10, map[string]any{
			"repository": &request.Repository, "number": &request.Number, "revision": &request.Revision,
		})
		if err == nil {
			selected, err = s.reviewSelected(ctx, request, nil)
		}
	case r.Method == http.MethodPost && path == "/confirm-test-recommendation":
		var request RetryRequest
		var recommendationID string
		err = readObject(w, r, 2<<10, map[string]any{
			"repository": &request.Repository, "number": &request.Number, "baseSha": &request.BaseSHA,
			"headSha": &request.HeadSHA, "revision": &request.Revision, "recommendationId": &recommendationID,
		})
		if err == nil {
			err = s.confirmQuickTest(ctx, request, recommendationID)
		}
	case r.Method == http.MethodPost && (path == "/review-latest" || path == "/retry" || path == "/retry-test" || path == "/refresh-checks"):
		var request RetryRequest
		var attempt int
		var testID string
		var optional []string
		fields := map[string]any{
			"repository": &request.Repository, "number": &request.Number, "baseSha": &request.BaseSHA,
			"headSha": &request.HeadSHA, "revision": &request.Revision,
		}
		if path == "/retry-test" {
			fields["attempt"] = &attempt
			fields["testId"] = &testID
			optional = []string{"testId"}
		}
		err = readObject(w, r, 2<<10, fields, optional...)
		if err == nil && path == "/review-latest" {
			err = s.reviewLatest(ctx, request)
		} else if err == nil && path == "/retry-test" {
			err = s.retrySelectedTest(ctx, request, attempt, testID)
		} else if err == nil && path == "/refresh-checks" {
			err = s.refreshChecks(ctx, request)
		} else if err == nil {
			err = s.requestRetry(ctx, request)
		}
	case r.Method == http.MethodPost && (path == "/connect/github" || path == "/connect/codex"):
		err = s.connect(ctx, strings.TrimPrefix(path, "/connect/"))
		var login *connectionError
		if errors.As(err, &login) {
			writeResponse(w, http.StatusServiceUnavailable, map[string]string{"code": "github_review_connection_failed", "error": login.Error()})
			return
		}
	case r.Method == http.MethodPost && (path == "/disconnect/github" || path == "/disconnect/codex"):
		err = s.disconnect(ctx, strings.TrimPrefix(path, "/disconnect/"))
	default:
		writeResponse(w, http.StatusNotFound, map[string]string{"code": "not_found", "error": "Unknown GitHub Review operation."})
		return
	}
	if err != nil {
		writeFailure(w, err)
		return
	}
	view, err := s.snapshot(ctx)
	if err != nil {
		writeFailure(w, errStore)
		return
	}
	if path == "/review-pr" {
		writeResponse(w, http.StatusOK, struct {
			Snapshot
			SelectedReview selectedReview `json:"selectedReview"`
		}{view, selected})
		return
	}
	writeResponse(w, http.StatusOK, view)
}

func allowedQuery(raw string, keys ...string) (url.Values, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return nil, ErrInvalid
	}
	for key, values := range query {
		if len(values) != 1 {
			return nil, ErrInvalid
		}
		found := false
		for _, option := range keys {
			if key == option {
				found = true
				break
			}
		}
		if !found {
			return nil, ErrInvalid
		}
	}
	return query, nil
}

func (s *Service) serveInbox(w http.ResponseWriter, r *http.Request) {
	query, err := allowedQuery(r.URL.RawQuery, "view", "cursor")
	view := inboxView(query.Get("view"))
	if err != nil || !view.valid() {
		writeFailure(w, ErrInvalid)
		return
	}
	page, err := s.store.inboxPage(r.Context(), view, query.Get("cursor"), s.scenarioVMReady)
	writeReviewRead(w, page, err)
}

func (s *Service) serveReviewJob(w http.ResponseWriter, r *http.Request) {
	query, err := allowedQuery(r.URL.RawQuery, "repository", "number", "baseSha", "headSha")
	number, numberErr := strconv.Atoi(query.Get("number"))
	target := Target{Repository: query.Get("repository"), Number: number, BaseSHA: query.Get("baseSha"), HeadSHA: query.Get("headSha")}
	if err != nil || numberErr != nil || strconv.Itoa(number) != query.Get("number") || !target.valid() {
		writeFailure(w, ErrInvalid)
		return
	}
	view, err := s.exactReviewJob(r.Context(), target)
	writeReviewRead(w, view, err)
}

func writeReviewRead(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeResponse(w, http.StatusOK, value)
}

func parseConfig(w http.ResponseWriter, r *http.Request) (Config, error) {
	var config Config
	err := readObject(w, r, 2<<20, map[string]any{
		"repositories": &config.Repositories, "intervalMinutes": &config.IntervalMinutes,
		"enabled": &config.Enabled, "publishRepositories": &config.PublishRepositories,
		"repositoryPolicies": &config.RepositoryPolicies, "dailyLimit": &config.DailyLimit, "revision": &config.Revision,
		"watchOnly": &config.WatchOnly,
	}, "watchOnly")
	if err != nil || config.Revision == 0 || len(config.PublishRepositories) > 0 {
		return Config{}, ErrInvalid
	}
	return config.normalized()
}

func readObject(w http.ResponseWriter, r *http.Request, maxBytes int64, fields map[string]any, optional ...string) error {
	if strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0] != "application/json" {
		return ErrInvalid
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if err != nil || !jsonunicode.Valid(data) {
		return ErrInvalid
	}
	return decodeFields(data, fields, optional...)
}

// The policy is both user configuration and a durable execution recipe.
// A misspelled command field must not silently disable verification.
func (p *RepositoryPolicy) UnmarshalJSON(data []byte) error {
	return decodeFields(data, map[string]any{"rules": &p.Rules, "testCommand": &p.TestCommand, "testProfiles": &p.TestProfiles,
		"comparisonRuntimes": &p.ComparisonRuntimes, "autoCandidateValidation": &p.AutoCandidateValidation,
		"comparisonScenarios": &p.ComparisonScenarios}, "testProfiles", "comparisonRuntimes", "autoCandidateValidation", "comparisonScenarios")
}

func decodeFields(data []byte, fields map[string]any, optional ...string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ErrInvalid
	}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || fields[name] == nil {
			return ErrInvalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || string(value) == "null" || json.Unmarshal(value, fields[name]) != nil {
			return ErrInvalid
		}
		delete(fields, name)
	}
	for _, key := range optional {
		delete(fields, key)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || len(fields) != 0 {
		return ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func writeFailure(w http.ResponseWriter, err error) {
	status, code, message := http.StatusServiceUnavailable, "github_review_unavailable", errStore.Error()
	switch {
	case errors.Is(err, errReviewJobNotFound):
		status, code, message = http.StatusNotFound, "github_review_job_not_found", errReviewJobNotFound.Error()
	case errors.Is(err, errComparisonNotFound):
		status, code, message = http.StatusNotFound, "github_review_comparison_not_found", errComparisonNotFound.Error()
	case errors.Is(err, errComparisonBusy), errors.Is(err, errComparisonVM):
		status, code, message = http.StatusConflict, "github_review_comparison_unavailable", err.Error()
	case errors.Is(err, errScenarioApproval):
		status, code, message = http.StatusConflict, "github_review_scenario_approval", err.Error()
	case errors.Is(err, errCandidateConflict):
		status, code, message = http.StatusConflict, "github_review_candidate_conflict", errCandidateConflict.Error()
	case errors.Is(err, errCandidateDraftConflict):
		status, code, message = http.StatusConflict, "github_review_draft_conflict", errCandidateDraftConflict.Error()
	case errors.Is(err, ErrInvalid):
		status, code, message = http.StatusBadRequest, "github_review_invalid", "Provide valid GitHub Review settings or an exact pull request revision."
	case errors.Is(err, ErrConflict):
		status, code, message = http.StatusConflict, "github_review_conflict", "GitHub Review settings changed. Reload them before saving."
	case errors.Is(err, errReviewDisconnected):
		status, code, message = http.StatusConflict, "github_review_disconnected", errReviewDisconnected.Error()
	case errors.Is(err, errReviewOutOfScope):
		status, code, message = http.StatusConflict, "github_review_out_of_scope", errReviewOutOfScope.Error()
	case errors.Is(err, errReviewNotReady):
		status, code, message = http.StatusConflict, "github_review_not_ready", errReviewNotReady.Error()
	case errors.Is(err, ErrGitHubNotFound):
		status, code, message = http.StatusNotFound, "github_review_pr_not_found", "GitHub could not find that pull request for the connected account."
	case errors.Is(err, errReviewLookup):
		status, code, message = http.StatusServiceUnavailable, "github_review_pr_lookup_failed", errReviewLookup.Error()
	case errors.Is(err, errVMRegistryUnavailable):
		status, code, message = http.StatusServiceUnavailable, "github_review_vm_registry_invalid", errVMRegistryUnavailable.Error()
	case errors.Is(err, ErrInactive):
		status, code, message = http.StatusConflict, "github_review_inactive", "Connect GitHub and Codex and enable polling first."
	case errors.Is(err, ErrRetryStale):
		status, code, message = http.StatusConflict, "github_review_retry_stale", "The selected review or pull request changed. Refresh History before retrying."
	case githubConnectionError(err):
		status, code, message = http.StatusServiceUnavailable, "github_review_connection_failed", err.Error()
	}
	writeResponse(w, status, map[string]string{"code": code, "error": message})
}

func writeResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
