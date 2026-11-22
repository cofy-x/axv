CREATE TABLE review_candidate_comment_drafts (
    candidate_id TEXT NOT NULL PRIMARY KEY REFERENCES review_candidates(id),
    body TEXT NOT NULL,
    anchor_sha TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0)
);
