CREATE INDEX review_candidate_scenario_reference
ON review_candidates (account, repository, COALESCE(json_extract(finding, '$.verificationScenarioId'), ''))
WHERE COALESCE(json_extract(finding, '$.verificationScenarioId'), '') <> '';

CREATE INDEX review_candidate_scenario_title
ON review_candidates (account, repository, match_key)
WHERE COALESCE(json_extract(finding, '$.verificationScenarioId'), '') = '';
