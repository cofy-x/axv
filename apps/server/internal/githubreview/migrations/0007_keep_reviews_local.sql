UPDATE review_settings SET publish_repositories = '[]' WHERE publish_repositories <> '[]';

UPDATE review_jobs
SET status = 'analyzed', attempts = 0, error = ''
WHERE status = 'publishing' OR (status = 'failed' AND findings <> '[]' AND comment_url = '');
