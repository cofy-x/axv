package githubreview

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/cofy-x/axv/apps/server/internal/httpaccess"
	"github.com/cofy-x/axv/apps/server/internal/jsonunicode"
)

const route = "/api/github-review"

func (s *Service) Register(mux *http.ServeMux) {
	register(mux, s.serve)
}

func RegisterUnavailable(mux *http.ServeMux) {
	register(mux, func(w http.ResponseWriter, _ *http.Request) {
		writeFailure(w, errStore)
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
	if r.URL.RawQuery != "" {
		writeFailure(w, ErrInvalid)
		return
	}
	ctx := r.Context()
	var err error
	path := strings.TrimPrefix(r.URL.Path, route)
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
	case r.Method == http.MethodPost && path == "/candidate-followup":
		var req candidateDecision
		err = readObject(w, r, 2<<10, map[string]any{"account": &req.Account, "repository": &req.Repository, "number": &req.Number, "baseSha": &req.BaseSHA, "headSha": &req.HeadSHA, "previousCandidateId": &req.PreviousID})
		if err == nil {
			err = s.store.followCandidate(ctx, req)
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
	writeResponse(w, http.StatusOK, view)
}

func parseConfig(w http.ResponseWriter, r *http.Request) (Config, error) {
	var config Config
	err := readObject(w, r, 2<<20, map[string]any{
		"repositories": &config.Repositories, "intervalMinutes": &config.IntervalMinutes,
		"enabled": &config.Enabled, "publishRepositories": &config.PublishRepositories,
		"repositoryPolicies": &config.RepositoryPolicies, "dailyLimit": &config.DailyLimit, "revision": &config.Revision,
	})
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
	return decodeFields(data, map[string]any{"rules": &p.Rules, "testCommand": &p.TestCommand, "testProfiles": &p.TestProfiles, "comparisonRuntimes": &p.ComparisonRuntimes}, "testProfiles", "comparisonRuntimes")
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
	case errors.Is(err, errComparisonNotFound):
		status, code, message = http.StatusNotFound, "github_review_comparison_not_found", errComparisonNotFound.Error()
	case errors.Is(err, errComparisonBusy), errors.Is(err, errComparisonVM):
		status, code, message = http.StatusConflict, "github_review_comparison_unavailable", err.Error()
	case errors.Is(err, errCandidateConflict):
		status, code, message = http.StatusConflict, "github_review_candidate_conflict", errCandidateConflict.Error()
	case errors.Is(err, ErrInvalid):
		status, code, message = http.StatusBadRequest, "github_review_invalid", "Provide valid GitHub Review settings or an exact pull request revision."
	case errors.Is(err, ErrConflict):
		status, code, message = http.StatusConflict, "github_review_conflict", "GitHub Review settings changed. Reload them before saving."
	case errors.Is(err, errReviewDisconnected):
		status, code, message = http.StatusConflict, "github_review_disconnected", errReviewDisconnected.Error()
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
