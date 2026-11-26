# GitHub Review API and ownership

[API index](README.md)

Local-only API family on the ordinary loopback desktop listener (excluded from remote Tailscale and Cloudflare listeners; mutations require exact local Origin). [`internal/githubreview`](../../apps/server/internal/githubreview) owns settings, CLI account attachment, scheduled PR discovery, Codex analysis, upstream Checks/checkout evidence, VM tests, candidate comparisons and local drafts, persisted in `data/github-review/reviews.sqlite` (format marker **AXR3**, [`0001_initial.sql`](../../apps/server/internal/githubreview/migrations/0001_initial.sql)). See [usage](../usage.md#github-review) and the [dedicated review VM recipe](../../recipes/github-review/README.md).

## Routes

| Method | Route | Contract |
| --- | --- | --- |
| GET | `/api/github-review` | Settings, CLI connection state, budget, discovery status and recent jobs (`?summary=1` omits job bodies) |
| GET | `/api/github-review/inbox` | Paginated `view=action\|active\|history` (up to 25 rows + `nextCursor`), scoped to attached account |
| GET | `/api/github-review/job` | Exact stored review by `repository`, `number`, `baseSha`, `headSha` (404 if absent) |
| GET | `/api/github-review/vm-readiness` | Non-mutating check of selected VM/project/Project Tasks capability for `repository` |
| PUT | `/api/github-review/config` | Revision-checked save of `repositories`, `repositoryPolicies`, `publishRepositories` (must be empty), `intervalMinutes`, `dailyLimit`, `enabled`, `watchOnly?` |
| POST | `/api/github-review/connect/{github\|codex}` | Attach host CLI login or start device-code flow |
| POST | `/api/github-review/disconnect/{github\|codex}` | Detach integration and cancel pending login |
| POST | `/api/github-review/run` | Wake enabled worker for an immediate scan |
| POST | `/api/github-review/review-pr` | Queue one open ready PR by `repository`, `number` (watched or direct request; works while polling paused) |
| POST | `/api/github-review/review-latest` | Check displayed PR against GitHub and queue its current comparison (works while paused) |
| POST | `/api/github-review/retry` | Retry failed/stale analysis (requires polling + both connections) |
| POST | `/api/github-review/refresh-checks` | Refresh GitHub Checks / checkout provenance for a captured PR (works while paused; preserves last success on error) |
| POST | `/api/github-review/retry-test` | Trigger automatic or one-time override quick test using latest finished `attempt` (works while paused) |
| POST | `/api/github-review/confirm-test-recommendation` | Confirm a `status: run` `quickTestRecommendation` |
| POST | `/api/github-review/candidate-comparison` | Queue two-leg assertion on diff merge base and PR head (`requestId` idempotent) |
| GET | `/api/github-review/candidate-comparison/{id}` | Read saved runtime, assertion and both Compute receipts for a comparison |
| POST | `/api/github-review/approve-comparison-scenario` | Approve a confirmed `head_only_failed` comparison as a reusable repository scenario |
| POST | `/api/github-review/candidate` | Revision-checked local disposition (`pending\|confirmed\|false_positive`), note, evidence links and optional cross-revision track link |
| POST | `/api/github-review/candidate-followup` | Idempotently open a local follow-up (`index: -1`) for an unreported historical PR issue |
| POST | `/api/github-review/candidate-draft` | Revision-checked local Markdown comment anchored to reviewed diff (up to 8 KiB) |
| POST | `/api/github-review/candidate-draft-reviewed` | Mark/unmark the exact saved draft revision as reviewed |

## Review, discovery, and budget invariants

- **Exact comparison identity:** Jobs key on `(githubLogin, repository, number, baseSha, headSha)`. Discovering a new head marks prior reviews stale while keeping exact job history readable; a base tip move with an equivalent three-dot merge-base diff keeps its completed review.
- **Untrusted PR context & isolated Codex input:** Captures PR title (1 KiB) and description (8 KiB) at review time as untrusted author context alongside the merge-base diff (192 KiB cap; 1,024-commit merge-base budget). Non-interactive Codex runs with no GitHub credentials, shell or external tools; finding lines must match changed diff lines.
- **Daily budget:** `dailyLimit` (1–1,000, default 10) reserves a slot transactionally when Codex analysis starts (persists across restarts and account switches). Reopening completed reviews, refreshing checks, running VM tests and editing local decisions/drafts consume no quota.
- **Local-only comments:** `publishRepositories` must remain empty; AXV never posts comments to GitHub.

## VM tests, comparisons, and candidate tracking

- **Quick-test selection & receipts:** Repositories configure either legacy `testCommand` or up to 8 ordered `testProfiles` (`entryPath` + optional `appliesTo` path prefixes; verified against GitHub/Git diff). Matching tests run as pinned [Compute Tasks](compute.md#vm-project-tasks) on the repository's configured review VM (`AXV_REVIEW_VM_CONFIG` / `AXV_REVIEW_VM_*`); unmatched profiles end as `not_applicable`. Exact verified receipts can be reused (`quickTestRecommendation.status: reuse`) without launching another task.
- **Two-leg candidate comparisons:** Runs an operator-confirmed assertion (up to 8 KiB) via a configured `comparisonRuntime` (default POSIX shell) on both the diff merge base and PR head in the same VM boot, reporting `both_passed`, `both_failed`, `head_only_failed`, `baseline_only_failed` or `inconclusive`.
- **Approved scenario validation:** A verified `head_only_failed` receipt on a confirmed candidate can be approved as a repository scenario for automatic validation on future reviews (exact match or Codex `verificationScenarioId`). Only verified common-ancestor pass + PR-head failure produces a suggested `confirmed` decision and fills an empty draft slot without overwriting human edits.
- **Cross-revision tracking & drafts:** Candidates carry opaque `id` + cross-version `trackId`, revision-checked local assessments, and anchored drafts (`base` or `head` side) that become read-only (`stale: true`) when superseded.

Exact validation bounds, error codes and schema details live in [`apps/server/internal/githubreview`](../../apps/server/internal/githubreview) and adjacent unit tests.
