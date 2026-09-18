CREATE TABLE hive_queens (
    id            UUID PRIMARY KEY,
    hive_id       UUID NOT NULL REFERENCES hives(id) ON DELETE CASCADE,
    year          INTEGER NOT NULL,
    marked_at     TIMESTAMPTZ,
    introduced_at      TIMESTAMPTZ NOT NULL,
    removed_at         TIMESTAMPTZ,
    replacement_reason TEXT,
    notes              TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_hive_queens_active_has_no_replacement_reason
        CHECK (removed_at IS NOT NULL OR replacement_reason IS NULL),
    CONSTRAINT chk_hive_queens_replacement_reason_valid
        CHECK (replacement_reason IS NULL OR replacement_reason IN (
            'AGING_AND_WEAR',
            'LOW_EGG_LAYING',
            'INJURY_OR_MUTILATION',
            'DISEASE_OR_POOR_QUALITY',
            'NATURAL_SUPERSEDURE',
            'BREED_CHANGE_OR_AGGRESSIVENESS'
        ))
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

