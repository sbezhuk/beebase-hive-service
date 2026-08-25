CREATE TABLE hives (
    id         UUID PRIMARY KEY,
    -- No foreign key to an apiaries table: apiaries live in a different
    -- service and database. apiary_id is opaque here; ownership was
    -- confirmed against apiary-service once, at creation time.
    apiary_id  UUID NOT NULL,
    -- Denormalized owner, copied from the verified apiary's owner at
    -- creation time. Immutable thereafter (apiary_id never changes), so
    -- every later read/write can be scoped by user_id directly, without
    -- another cross-service call.
    user_id    UUID NOT NULL,
    name       TEXT NOT NULL,
    location   TEXT NOT NULL DEFAULT '',
    notes      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

-- Serves GetByID/Update/Delete (id as an additional equality filter) and
-- ListByUser directly.
CREATE INDEX idx_hives_user_id ON hives (user_id) WHERE deleted_at IS NULL;
