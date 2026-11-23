-- Preserve rowids because candidate occurrences and inbox cursors refer to them.
CREATE TABLE review_jobs_exact (
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
    CHECK (selected_for_head=1 OR status='stale')
);

INSERT INTO review_jobs_exact (
    rowid, account, repository, number, title, base_sha, head_sha, source, status, findings,
    comment_url, error, attempts, updated_at, policy, checks, tests, checks_due, test_due,
    evidence_key, review_stage, merge_base_sha, observed_base_sha, equivalent_base_sha,
    manual_review, comparison_saved_status
)
SELECT rowid, account, repository, number, title, base_sha, head_sha, source, status, findings,
    comment_url, error, attempts, updated_at, policy, checks, tests, checks_due, test_due,
    evidence_key, review_stage, merge_base_sha, observed_base_sha, equivalent_base_sha,
    manual_review, comparison_saved_status
FROM review_jobs;

DROP TABLE review_jobs;
ALTER TABLE review_jobs_exact RENAME TO review_jobs;

CREATE UNIQUE INDEX review_jobs_selected_head ON review_jobs (account, repository, number, head_sha) WHERE selected_for_head=1;
CREATE INDEX review_jobs_status ON review_jobs (account, status, updated_at);
CREATE INDEX review_checks_due ON review_jobs (account, checks_due) WHERE checks_due IS NOT NULL;
CREATE INDEX review_test_due ON review_jobs (account, test_due) WHERE test_due IS NOT NULL;
