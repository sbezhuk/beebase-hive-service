-- Serves ListByApiary, which drives DeleteByApiary's cascade (deleting an
-- apiary hard-deletes every hive under it, and everything under each of
-- those). Unlike every other index on this table, this one is NOT partial
-- (no WHERE deleted_at IS NULL): the cascade must also find hives a prior
-- soft-delete already marked gone, so it can finish purging their
-- remaining inspections/media rather than leaving them orphaned forever.
CREATE INDEX idx_hives_apiary_id ON hives (apiary_id, user_id);
