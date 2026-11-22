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
