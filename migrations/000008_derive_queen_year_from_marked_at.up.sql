-- year is now always derived from marked_at (never stored, so the two can't drift).
-- marked_at becomes required: a queen may be marked before she is introduced into
-- this specific hive, so backfill any existing NULL marked_at from introduced_at
-- before enforcing NOT NULL.
UPDATE hive_queens SET marked_at = introduced_at WHERE marked_at IS NULL;

ALTER TABLE hive_queens
    ALTER COLUMN marked_at SET NOT NULL,
    DROP COLUMN year;
