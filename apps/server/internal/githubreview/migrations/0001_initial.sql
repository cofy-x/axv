-- AXR3 identifies the consolidated GitHub Review baseline independently of migration versions.
PRAGMA application_id = 0x41585233;

CREATE TABLE review_settings (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    repositories TEXT NOT NULL DEFAULT '[]',
    interval_minutes INTEGER NOT NULL DEFAULT 10 CHECK (interval_minutes BETWEEN 1 AND 1440),
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    github_account TEXT NOT NULL DEFAULT '',
    codex_connected INTEGER NOT NULL DEFAULT 0 CHECK (codex_connected IN (0, 1)),
    daily_limit INTEGER NOT NULL DEFAULT 10 CHECK (daily_limit BETWEEN 1 AND 1000),
    repository_policies TEXT NOT NULL DEFAULT '{}',
    watch_only INTEGER NOT NULL DEFAULT 0 CHECK (watch_only IN (0, 1))
);

INSERT INTO review_settings (singleton) VALUES (1);

CREATE TABLE review_jobs (
    account TEXT NOT NULL COLLATE NOCASE,
    repository TEXT NOT NULL COLLATE NOCASE,
    number INTEGER NOT NULL CHECK (number > 0),
    title TEXT NOT NULL,
    base_sha TEXT NOT NULL,
    head_sha TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('repository', 'requested')),
    status TEXT NOT NULL CHECK (status IN ('queued', 'reviewing', 'analyzed', 'publishing', 'published', 'clean', 'stale', 'failed')),
    findings TEXT NOT NULL DEFAULT '[]',
    comment_url TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    policy TEXT,
    checks TEXT NOT NULL DEFAULT '{"status":"queued"}',
    tests TEXT NOT NULL DEFAULT '[]',
    checks_due INTEGER,
    test_due INTEGER,
    evidence_key TEXT,
    review_stage TEXT NOT NULL DEFAULT '{}',
    merge_base_sha TEXT NOT NULL DEFAULT '',
    observed_base_sha TEXT NOT NULL DEFAULT '',
    equivalent_base_sha TEXT NOT NULL DEFAULT '',
    manual_review INTEGER NOT NULL DEFAULT 0 CHECK (manual_review IN (0, 1)),
    comparison_saved_status TEXT NOT NULL DEFAULT '',
    selected_for_head INTEGER NOT NULL DEFAULT 1 CHECK (selected_for_head IN (0, 1)),
    PRIMARY KEY (account, repository, number, head_sha, base_sha),
    CHECK (selected_for_head = 1 OR status = 'stale')
);

CREATE UNIQUE INDEX review_jobs_selected_head ON review_jobs (account, repository, number, head_sha) WHERE selected_for_head = 1;
CREATE INDEX review_jobs_status ON review_jobs (account, status, updated_at);
CREATE INDEX review_checks_due ON review_jobs (account, checks_due) WHERE checks_due IS NOT NULL;
CREATE INDEX review_test_due ON review_jobs (account, test_due) WHERE test_due IS NOT NULL;

CREATE TABLE review_attempts (
    started_at INTEGER NOT NULL
);

CREATE INDEX review_attempts_started_at ON review_attempts (started_at);

CREATE TABLE review_candidates (
    id TEXT PRIMARY KEY,
    account TEXT NOT NULL COLLATE NOCASE,
    repository TEXT NOT NULL COLLATE NOCASE,
    number INTEGER NOT NULL,
    base_sha TEXT NOT NULL,
    head_sha TEXT NOT NULL,
    review_key TEXT NOT NULL,
    job_order INTEGER NOT NULL,
    match_key TEXT NOT NULL,
    track_id TEXT NOT NULL,
    finding TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'false_positive')),
    note TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '[]',
    revision INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX review_candidate_pr ON review_candidates (account, repository, number, job_order);
CREATE INDEX review_candidate_match ON review_candidates (account, repository, number, match_key);
CREATE INDEX review_candidate_scenario_reference
ON review_candidates (account, repository, COALESCE(json_extract(finding, '$.verificationScenarioId'), ''))
WHERE COALESCE(json_extract(finding, '$.verificationScenarioId'), '') <> '';
CREATE INDEX review_candidate_scenario_title
ON review_candidates (account, repository, match_key)
WHERE COALESCE(json_extract(finding, '$.verificationScenarioId'), '') = '';

CREATE TABLE review_candidate_followups (
    id TEXT PRIMARY KEY REFERENCES review_candidates(id),
    origin_id TEXT NOT NULL REFERENCES review_candidates(id),
    resolved INTEGER NOT NULL DEFAULT 0 CHECK (resolved IN (0, 1))
);

CREATE TABLE review_candidate_comparisons (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL REFERENCES review_candidates(id),
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'error', 'canceled')),
    updated_at INTEGER NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    result TEXT NOT NULL CHECK (json_valid(result))
);

CREATE INDEX review_candidate_comparison_history ON review_candidate_comparisons (candidate_id);
CREATE UNIQUE INDEX review_candidate_comparison_active ON review_candidate_comparisons (candidate_id) WHERE status IN ('queued', 'running');
CREATE INDEX review_candidate_comparison_queue ON review_candidate_comparisons (updated_at) WHERE status IN ('queued', 'running');

CREATE TABLE review_candidate_comment_drafts (
    candidate_id TEXT NOT NULL PRIMARY KEY REFERENCES review_candidates(id),
    body TEXT NOT NULL,
    anchor_sha TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    reviewed_at INTEGER,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0)
);

CREATE TABLE review_comparison_scenarios (
    id TEXT NOT NULL PRIMARY KEY,
    account TEXT NOT NULL COLLATE NOCASE,
    repository TEXT NOT NULL COLLATE NOCASE,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    title TEXT NOT NULL,
    side TEXT NOT NULL CHECK (side IN ('base', 'head')),
    match_key TEXT NOT NULL,
    source_comparison_id TEXT NOT NULL REFERENCES review_candidate_comparisons(id),
    UNIQUE (account, repository, source_comparison_id)
);

CREATE INDEX review_comparison_scenario_match ON review_comparison_scenarios (account, repository, match_key);

CREATE TABLE review_candidate_validations (
    candidate_id TEXT NOT NULL PRIMARY KEY REFERENCES review_candidates(id),
    scenario_id TEXT NOT NULL REFERENCES review_comparison_scenarios(id),
    comparison_id TEXT REFERENCES review_candidate_comparisons(id),
    state TEXT NOT NULL CHECK (state IN ('queued', 'needs_review', 'verified')),
    reason TEXT NOT NULL,
    reused INTEGER NOT NULL CHECK (reused IN (0, 1)),
    updated_at INTEGER NOT NULL
);
