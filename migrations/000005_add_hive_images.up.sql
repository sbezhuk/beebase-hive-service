-- hive-service now stores the set of attached media ids itself (source
-- of truth for Get/List/responses), rather than asking media-service for
-- it on every read.
ALTER TABLE hives ADD COLUMN images uuid[] NOT NULL DEFAULT '{}';
