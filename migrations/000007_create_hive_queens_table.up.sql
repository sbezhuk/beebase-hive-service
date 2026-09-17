CREATE TABLE hive_queens (
    id            UUID PRIMARY KEY,
    hive_id       UUID NOT NULL REFERENCES hives(id) ON DELETE CASCADE,
    year          INTEGER NOT NULL,
    marked_at     TIMESTAMPTZ,
    introduced_at TIMESTAMPTZ NOT NULL,
    removed_at    TIMESTAMPTZ,
    notes         TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Enforce strict chronological chain position: no two queens in the same hive can have the same introduced_at
CREATE UNIQUE INDEX idx_hive_queens_hive_introduced
    ON hive_queens (hive_id, introduced_at);

-- Enforce invariant: at most one active (current) queen per hive
CREATE UNIQUE INDEX idx_hive_queens_one_current_per_hive
    ON hive_queens (hive_id)
    WHERE removed_at IS NULL;

-- Efficient query for queen history by hive, ordered newest first
CREATE INDEX idx_hive_queens_hive_history
    ON hive_queens (hive_id, introduced_at DESC, created_at DESC);

