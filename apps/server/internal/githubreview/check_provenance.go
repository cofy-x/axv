package githubreview

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

type actionsStep struct {
	Name       string
	Number     int
	Conclusion string
	Started    string
	Finished   string
}

type actionsJob struct {
	ID      int64
	RunID   int64
	HeadSHA string
	HTMLURL string
	Steps   []actionsStep
}

// Upstream job details are optional evidence. A private, expired or unsupported
// log never changes the conclusion supplied by the checks endpoint.
func (g GitHubCLI) observeActionsJobs(ctx context.Context, token string, target Target, runs []CheckRun, actions map[string]int64, previous Checks) {
	old := map[string]CheckRun{}
	for _, run := range previous.Runs {
		old[run.URL] = run
	}
	var workers sync.WaitGroup
	slots := make(chan struct{}, 4)
	observed := 0
	for i := range runs {
		run := &runs[i]
		id, ok := actions[run.URL]
		if !ok {
			continue
		}
		if cached := old[run.URL]; cached.ExecutionSHA != "" && cached.Name == run.Name && cached.Status == run.Status && cached.Conclusion == run.Conclusion {
			run.ExecutionSHA, run.ExecutionRef, run.ProvenanceURL, run.Scope = cached.ExecutionSHA, cached.ExecutionRef, cached.ProvenanceURL, cached.Scope
			continue
		}
		if observed >= 24 || ctx.Err() != nil {
			continue
		}
		observed++
		slots <- struct{}{}
		workers.Go(func() {
			defer func() { <-slots }()
			g.observeActionsJob(ctx, token, target, run, id)
		})
	}
	workers.Wait()
}

func (g GitHubCLI) observeActionsJob(ctx context.Context, token string, target Target, run *CheckRun, id int64) {
	u, err := url.Parse(run.URL)
	if err != nil || u.RawQuery != "" || u.Fragment != "" {
		return
	}
	prefix := "/" + strings.ToLower(target.Repository) + "/actions/runs/"
	suffix, ok := strings.CutPrefix(strings.ToLower(u.Path), prefix)
	if !ok {
		return
	}
	runText, jobText, ok := strings.Cut(suffix, "/job/")
	runID, err := strconv.ParseInt(runText, 10, 64)
	if !ok || err != nil || runID <= 0 || jobText != strconv.FormatInt(id, 10) {
		return
	}
	endpoint := fmt.Sprintf("repos/%s/actions/jobs/%d", target.Repository, id)
	var job actionsJob
	if err := g.api(ctx, token, &job, nil, endpoint, "--jq", `{id,runId:.run_id,headSha:.head_sha,htmlUrl:.html_url,steps:[.steps[]|{name,number,conclusion,started:.started_at,finished:.completed_at}]}`); err != nil {
		return
	}
	if job.ID != id || job.RunID != runID || job.HeadSHA != target.HeadSHA || job.HTMLURL != run.URL || len(job.Steps) < 2 {
		return
	}
	var scope []string
	for _, step := range job.Steps {
		if step.Number <= 1 || strings.HasPrefix(step.Name, "Post ") || step.Name == "Complete job" || len(step.Name) > 160 || strings.IndexFunc(step.Name, unicode.IsControl) >= 0 {
			continue
		}
		switch step.Conclusion {
		case "success":
			scope = append(scope, step.Name)
		case "failure":
			scope = append(scope, step.Name+" (failed)")
		}
		if len(scope) == 32 {
			break
		}
	}
	run.Scope = strings.Join(scope, " · ")
	// Only accept a primary checkout performed as the first action in the job.
	// Later shell steps can print arbitrary Git commands and object IDs.
	step := job.Steps[1]
	if step.Number != 2 || step.Conclusion != "success" {
		return
	}
	sha, ref := g.jobCheckout(ctx, token, endpoint+"/logs", target, step)
	if sha != "" {
		run.ExecutionSHA, run.ExecutionRef = sha, ref
		run.ProvenanceURL = fmt.Sprintf("%s#step:%d:1", run.URL, step.Number)
	}
}

// Keep provider logs out of storage. Some CI logs are hundreds of MiB; the
// checkout belongs to the beginning, so stop the subprocess after a bounded read.
func (g GitHubCLI) jobCheckout(parent context.Context, token, endpoint string, target Target, step actionsStep) (string, string) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	cmd, err := g.command(ctx, token, "api", "--hostname", githubHost, "--allow-escape-sequences", "-H", "Range: bytes=0-131071", endpoint)
	if err != nil {
		return "", ""
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return "", ""
	}
	sha, ref := checkoutReceipt(io.LimitReader(pipe, 128<<10), target, step)
	cancel()
	_ = cmd.Wait()
	return sha, ref
}

func checkoutReceipt(raw io.Reader, target Target, step actionsStep) (string, string) {
	start, e1 := time.Parse(time.RFC3339, step.Started)
	end, e2 := time.Parse(time.RFC3339, step.Finished)
	if e1 != nil || e2 != nil || end.Before(start) {
		return "", ""
	}
	// The job steps API exposes seconds while the runner timestamps each line
	// more precisely. Include the final second and stop at the next action header.
	end = end.Add(time.Second)
	var checkout, repoMatched, awaitSHA bool
	var sourceSHA, ref string
	scanner := bufio.NewScanner(raw)
	for scanner.Scan() {
		ts, msg, ok := strings.Cut(scanner.Text(), " ")
		if !ok {
			continue
		}
		ts = strings.TrimPrefix(ts, "\ufeff")
		at, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || at.Before(start) {
			continue
		}
		if !at.Before(end) {
			break
		}
		if strings.HasPrefix(msg, "##[group]Run ") {
			if checkout || !strings.HasPrefix(msg, "##[group]Run actions/checkout@") {
				break
			}
			checkout = true
			continue
		}
		if !checkout {
			continue
		}
		if strings.HasPrefix(msg, "  path: ") && strings.TrimSpace(strings.TrimPrefix(msg, "  path: ")) != "." {
			return "", ""
		}
		if strings.HasPrefix(msg, "Syncing repository: ") {
			if !strings.EqualFold(strings.TrimPrefix(msg, "Syncing repository: "), target.Repository) {
				return "", ""
			}
			repoMatched = true
		}
		if !repoMatched {
			continue
		}
		if awaitSHA {
			if !commitSHA.MatchString(msg) {
				return "", ""
			}
			if sourceSHA != msg {
				ref = ""
			}
			return msg, ref
		}
		if strings.HasPrefix(msg, "[command]") && strings.Contains(msg, " fetch ") {
			fields := strings.Fields(msg)
			last := fields[len(fields)-1]
			rawSHA, remoteRef, ok := strings.Cut(last, ":refs/remotes/")
			if ok && remoteRef == fmt.Sprintf("pull/%d/merge", target.Number) {
				sourceSHA = strings.TrimPrefix(rawSHA, "+")
				ref = "refs/" + remoteRef
			}
		}
		awaitSHA = strings.HasPrefix(msg, "[command]") && (strings.HasSuffix(msg, "/git log -1 --format=%H") || msg == "[command]git log -1 --format=%H")
	}
	return "", ""
}

func (s *Service) refreshChecks(ctx context.Context, req RetryRequest) error {
	if !req.valid() {
		return ErrInvalid
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return errStore
	}
	defer tx.Rollback()
	settings, err := readSettings(ctx, tx)
	if err != nil {
		return errStore
	}
	if settings.Revision != req.Revision {
		return ErrConflict
	}
	job, err := readJob(ctx, tx, settings.account, req.Repository, req.Number, req.HeadSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRetryStale
	}
	if err != nil {
		return errStore
	}
	if job.Evidence == nil || job.Status == "stale" || job.BaseSHA != req.BaseSHA || settings.account == "" {
		return ErrRetryStale
	}
	_, err = tx.ExecContext(ctx, `UPDATE review_jobs SET checks_due=COALESCE(checks_due, ?) WHERE account=? AND repository=? AND number=? AND head_sha=?`, time.Now().UnixMilli(), settings.account, job.Repository, job.Number, job.HeadSHA)
	if err != nil {
		return errStore
	}
	return tx.Commit()
}
