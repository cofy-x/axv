ALTER TABLE review_settings
    ADD COLUMN watch_only INTEGER NOT NULL DEFAULT 0 CHECK (watch_only IN (0, 1));
