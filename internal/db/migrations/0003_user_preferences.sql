-- Per-user UI preferences as a JSON blob (theme now; room for locale/a11y later).
ALTER TABLE users ADD COLUMN preferences TEXT NOT NULL DEFAULT '{}';
